package av

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// missavEvalHTML 构造带 surrit uuid eval 混淆串的详情页 HTML。
// eval 串中段序需反转（extractMissAVUUID 会还原），因此这里预先反转。
func missavEvalHTML(uuid string) string {
	parts := strings.Split(uuid, "-")
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return `<html><body><script>eval(function(p,a,c,k){}("` +
		"m3u8|" + strings.Join(parts, "|") + "|com|surrit|https|video" +
		`"))</script></body></html>`
}

// missavStubPage 描述一个变体页：uuid（空表示 404）与页面标签。
type missavStubPage struct {
	uuid string
	tags []string
}

// stubMissAVSite 伺服 MissAV 变体页与对应 surrit 播放列表。
// pages: 变体路径 → uuid（uuid 为空表示返回 404）。
func stubMissAVSite(t *testing.T, pages map[string]string) *httptest.Server {
	t.Helper()
	converted := make(map[string]missavStubPage, len(pages))
	for path, uuid := range pages {
		converted[path] = missavStubPage{uuid: uuid}
	}
	return stubMissAVSiteWithTags(t, converted)
}

// stubMissAVSiteWithTags 同 stubMissAVSite，但页面可携带标签供变体判定测试。
func stubMissAVSiteWithTags(t *testing.T, pages map[string]missavStubPage) *httptest.Server {
	t.Helper()
	const master = "#EXTM3U\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360\n" +
		"360/video.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=2800000,RESOLUTION=1280x720\n" +
		"720/video.m3u8\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if page, ok := pages[r.URL.Path]; ok {
			if page.uuid == "" {
				http.NotFound(w, r)
				return
			}
			html := missavEvalHTML(page.uuid)
			if len(page.tags) > 0 {
				var b strings.Builder
				b.WriteString("<html><body>")
				for _, tag := range page.tags {
					b.WriteString(`<a class="tag" href="/tags/x">` + tag + "</a>")
				}
				b.WriteString(html + "</body></html>")
				html = b.String()
			}
			_, _ = w.Write([]byte(html))
			return
		}
		// surrit playlist 路径形如 /<uuid>/playlist.m3u8
		if strings.HasSuffix(r.URL.Path, "/playlist.m3u8") {
			_, _ = w.Write([]byte(master))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestMissAVResolveVariants 验证 Resolve 汇总无码/中字/普通多个片源并打上标记。
func TestMissAVResolveVariants(t *testing.T) {
	srv := stubMissAVSite(t, map[string]string{
		"/cn/dm5-001-uncensored-leak":  "aaaa1111-bbbb2222-cccc3333",
		"/cn/dm5-001-chinese-subtitle": "dddd4444-eeee5555-ffff6666",
		"/cn/dm5-001":                  "dddd1111-cccc2222-bbbb3333",
		"/dm13/cn/dm5-001":             "", // 镜像 404
	})
	hc, err := NewHTTPClient(Options{})
	if err != nil {
		t.Fatalf("http client: %v", err)
	}
	src := NewMissAVSource(hc, srv.URL)
	src.surritBase = srv.URL // 播放列表也指向 stub，避免请求真实 CDN

	streams, err := src.Resolve(context.Background(), "DM5-001")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(streams) != 6 { // 3 个变体 × 2 路清晰度
		t.Fatalf("streams = %d, want 6: %+v", len(streams), streams)
	}

	var unc, cn, normal int
	for _, s := range streams {
		switch {
		case s.Uncensored:
			unc++
			if s.CNSub {
				t.Fatalf("stream both uncensored and cnsub: %+v", s)
			}
			if !strings.Contains(s.URL, "aaaa1111-bbbb2222-cccc3333") {
				t.Fatalf("uncensored stream from wrong uuid: %s", s.URL)
			}
		case s.CNSub:
			cn++
			if !strings.Contains(s.URL, "dddd4444-eeee5555-ffff6666") {
				t.Fatalf("cnsub stream from wrong uuid: %s", s.URL)
			}
		default:
			normal++
			if !strings.Contains(s.URL, "dddd1111-cccc2222-bbbb3333") {
				t.Fatalf("normal stream from wrong uuid: %s", s.URL)
			}
		}
		if s.Source != "missav" || s.QualityHeight == 0 {
			t.Fatalf("bad stream: %+v", s)
		}
	}
	if unc != 2 || cn != 2 || normal != 2 {
		t.Fatalf("variant counts = (%d,%d,%d), want (2,2,2)", unc, cn, normal)
	}
}

// TestMissAVResolveCNSubByTag 验证普通页带「中文字幕」标签时按标签标为中字片源。
func TestMissAVResolveCNSubByTag(t *testing.T) {
	normalUUID := "dddd1111-cccc2222-bbbb3333"
	srv := stubMissAVSiteWithTags(t, map[string]missavStubPage{
		"/cn/dm5-001": {uuid: normalUUID, tags: []string{"剧情", "中文字幕"}},
		// 变体后缀页都不存在，普通页本身即中字视频
		"/cn/dm5-001-chinese-subtitle": {uuid: ""},
		"/cn/dm5-001-uncensored-leak":  {uuid: ""},
	})
	hc, err := NewHTTPClient(Options{})
	if err != nil {
		t.Fatalf("http client: %v", err)
	}
	src := NewMissAVSource(hc, srv.URL)
	src.surritBase = srv.URL

	streams, err := src.Resolve(context.Background(), "dm5-001")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(streams) != 2 {
		t.Fatalf("streams = %d, want 2: %+v", len(streams), streams)
	}
	for _, s := range streams {
		if !s.CNSub || s.Uncensored {
			t.Fatalf("stream should be cnsub-only by tag: %+v", s)
		}
		if !strings.Contains(s.URL, normalUUID) {
			t.Fatalf("stream from wrong uuid: %s", s.URL)
		}
	}
}

// TestMissAVResolveUncensoredByTag 验证页面带「无码流出」标签时按标签标为无码片源。
func TestMissAVResolveUncensoredByTag(t *testing.T) {
	normalUUID := "eeee1111-dddd2222-cccc3333"
	srv := stubMissAVSiteWithTags(t, map[string]missavStubPage{
		"/cn/dm5-001": {uuid: normalUUID, tags: []string{"独占", "无码流出"}},
	})
	hc, err := NewHTTPClient(Options{})
	if err != nil {
		t.Fatalf("http client: %v", err)
	}
	src := NewMissAVSource(hc, srv.URL)
	src.surritBase = srv.URL

	streams, err := src.Resolve(context.Background(), "dm5-001")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(streams) != 2 {
		t.Fatalf("streams = %d, want 2: %+v", len(streams), streams)
	}
	for _, s := range streams {
		if !s.Uncensored || s.CNSub {
			t.Fatalf("stream should be uncensored-only by tag: %+v", s)
		}
	}
}

// TestMissAVResolveDedupeUUID 验证镜像页与普通页 uuid 相同时去重。
func TestMissAVResolveDedupeUUID(t *testing.T) {
	same := "aaaa1111-bbbb2222-cccc3333"
	srv := stubMissAVSite(t, map[string]string{
		"/cn/dm5-001":      same,
		"/dm13/cn/dm5-001": same,
	})
	hc, err := NewHTTPClient(Options{})
	if err != nil {
		t.Fatalf("http client: %v", err)
	}
	src := NewMissAVSource(hc, srv.URL)
	src.surritBase = srv.URL // 播放列表也指向 stub，避免请求真实 CDN

	streams, err := src.Resolve(context.Background(), "dm5-001")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(streams) != 2 {
		t.Fatalf("streams = %d, want 2 (deduped): %+v", len(streams), streams)
	}
	for _, s := range streams {
		if s.Uncensored || s.CNSub {
			t.Fatalf("normal variant should not be flagged: %+v", s)
		}
	}
}

// TestMissAVResolveNotFound 验证全部变体不存在时报 ErrNotFound。
func TestMissAVResolveNotFound(t *testing.T) {
	srv := stubMissAVSite(t, map[string]string{})
	hc, err := NewHTTPClient(Options{})
	if err != nil {
		t.Fatalf("http client: %v", err)
	}
	src := NewMissAVSource(hc, srv.URL)
	src.surritBase = srv.URL // 播放列表也指向 stub，避免请求真实 CDN

	if _, err := src.Resolve(context.Background(), "xx-000"); !IsNotFound(err) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
