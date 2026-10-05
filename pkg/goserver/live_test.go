//go:build live

package goserver

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"videoviewer/pkg/aacg"
	"videoviewer/pkg/av/browser"
)

// 实网诊断：在真实站点上还原播放链路（probe → /api/hls/playlist 改写 →
// /api/hls/segment 转发），对多条视频做形态普查并检出路由错误，定位播放器
// Source error 的断点。所有日志脱敏（只留 scheme://host/path）。

// liveRedact 去掉 URL 的查询串（auth_key 等敏感参数），只保留 scheme://host/path。
func liveRedact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparsable>"
	}
	return u.Scheme + "://" + u.Host + u.Path
}

// liveScrubText 折叠任意文本中的查询串：上游错误信息可能内嵌含 auth_key
// 的完整 URL，凡打印响应正文处都须先经此函数。
var liveQueryRe = regexp.MustCompile(`\?[^ "\r\n]*`)

func liveScrubText(s string) string {
	return liveQueryRe.ReplaceAllString(s, "?…")
}

// liveRedactProxy 展示本代理 URL 时把 u 参数里的上游地址折叠为脱敏形式。
func liveRedactProxy(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparsable>"
	}
	inner := u.Query().Get("u")
	if inner == "" {
		return liveRedact(raw)
	}
	return fmt.Sprintf("%s?u=%s", u.Scheme+"://"+u.Host+u.Path, liveRedact(inner))
}

func liveProxyEndpoint(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Path[strings.LastIndex(u.Path, "/")+1:]
}

func liveProxyUpstream(proxyURL string) string {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("u")
}

// liveProxyGet 以与播放器相同的请求形态调用真实 HLS 代理处理函数。
// 单次调用限时 30s：上游挂起时静默卡死不可接受，限时后按 502 记录。
func (s *Server) liveProxyGet(t *testing.T, ctx context.Context, proxyURL string) *httptest.ResponseRecorder {
	t.Helper()
	u, err := url.Parse(proxyURL)
	if err != nil {
		t.Fatalf("bad proxy url: %v", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, u.Path+"?"+u.RawQuery, nil).WithContext(callCtx)
	req.Host = u.Host
	rr := httptest.NewRecorder()
	switch liveProxyEndpoint(proxyURL) {
	case "playlist", "playlist.m3u8":
		s.handleHlsPlaylist(rr, req)
	case "segment":
		s.handleHlsSegment(rr, req)
	default:
		t.Fatalf("unexpected proxy path: %s", u.Path)
	}
	return rr
}

func liveIsPlaylist(body []byte) bool {
	return bytes.HasPrefix(bytes.TrimSpace(body), []byte("#EXTM3U"))
}

func liveFirstLine(body []byte) string {
	for _, ln := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(ln)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			return trimmed
		}
	}
	return ""
}

// liveProxyChildren 从改写后的 playlist 正文提取播放器将访问的全部子资源
// 代理地址（普通行 + URI="..." 属性），与 media3 的解析路径一致。
func liveProxyChildren(t *testing.T, body string) []string {
	t.Helper()
	var children []string
	for _, ln := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			for _, m := range hlsURIRe.FindAllString(trimmed, -1) {
				inner := m[5 : len(m)-1]
				if !strings.HasPrefix(inner, "http") {
					t.Fatalf("URI attr not rewritten: %s", trimmed)
				}
				children = append(children, inner)
			}
			continue
		}
		if !strings.HasPrefix(trimmed, "http") {
			t.Fatalf("line not rewritten: %s", trimmed)
		}
		children = append(children, trimmed)
	}
	return children
}

// liveCheckHlsChain 对一条视频执行完整代理链路：
// step1 拉主列表（经 playlist 端点）→ 按 media3 序列跟进子资源并分类校验；
// 检出“playlist 内容却落在 segment 端点”的路由错误（segment 处理器不改写
// 内层 URI，播放器将拿到 localhost 404 或直连 CDN 的分片地址）。
func liveCheckHlsChain(t *testing.T, s *Server, ctx context.Context, label, usable string, verbose bool) bool {
	t.Helper()
	playerURL := fmt.Sprintf("http://127.0.0.1:18888/api/hls/playlist.m3u8?%s",
		url.Values{"u": {usable}}.Encode())
	stepStart := time.Now()
	rr := s.liveProxyGet(t, ctx, playerURL)
	t.Logf("[%s] step1: playlist-host=%s status=%d bytes=%d elapsed=%s",
		label, hostOf(usable), rr.Code, rr.Body.Len(), time.Since(stepStart).Round(time.Millisecond))
	if rr.Code != http.StatusOK {
		t.Errorf("[%s] step1 non-200: %s", label, strings.TrimSpace(liveScrubText(rr.Body.String())))
		return false
	}
	body := rr.Body.String()
	shape := "media"
	if strings.Contains(body, "#EXT-X-STREAM-INF") {
		shape = "master"
	}
	// 记录播放列表形态：byterange/key/map 关系到播放器能否在分片层取到数据
	// （EXT-X-BYTERANGE 依赖 Range 透传，EXT-X-KEY 需要密钥经代理可取）。
	var feats []string
	for _, f := range []struct{ tag, name string }{
		{"#EXT-X-BYTERANGE", "byterange"},
		{"#EXT-X-KEY", "key"},
		{"#EXT-X-MAP", "map"},
		{"#EXT-X-ENDLIST", "vod"},
	} {
		if strings.Contains(body, f.tag) {
			feats = append(feats, f.name)
		}
	}
	children := liveProxyChildren(t, body)
	segChildren, plChildren, withQuery := 0, 0, 0
	for _, child := range children {
		if liveProxyEndpoint(child) == "segment" {
			segChildren++
		} else {
			plChildren++
		}
		if strings.Contains(liveProxyUpstream(child), "?") {
			withQuery++
		}
	}
	t.Logf("[%s] step1 shape=%s children=%d (segment=%d playlist=%d with-query=%d) feats=%s",
		label, shape, len(children), segChildren, plChildren, withQuery, strings.Join(feats, ","))

	ok := true
	followed := 0
	for _, child := range children {
		if followed >= 4 {
			break
		}
		followed++
		endpoint := liveProxyEndpoint(child)
		upstream := liveProxyUpstream(child)
		childStart := time.Now()
		rrChild := s.liveProxyGet(t, ctx, child)
		childElapsed := time.Since(childStart)
		childBody := rrChild.Body.Bytes()
		isPlaylist := liveIsPlaylist(childBody)
		head := childBody
		if len(head) > 12 {
			head = head[:12]
		}
		if verbose || isPlaylist || rrChild.Code != http.StatusOK {
			t.Logf("[%s] child: endpoint=%s upstream-host=%s status=%d ct=%q bytes=%d elapsed=%s head=%s suffix-m3u8=%t",
				label, endpoint, hostOf(upstream), rrChild.Code, rrChild.Header().Get("Content-Type"),
				len(childBody), childElapsed.Round(time.Millisecond), hex.EncodeToString(head), strings.HasSuffix(strings.ToLower(liveRedact(upstream)), ".m3u8"))
		}
		if endpoint == "segment" && isPlaylist {
			// 路由错误（主证据）：上游是 playlist 却走 segment 端点。
			ok = false
			t.Errorf("[%s] MISROUTE: playlist routed to segment endpoint: %s", label, liveRedactProxy(child))
			if inner := liveFirstLine(childBody); inner != "" {
				resolved := resolveAgainst(child, inner)
				t.Logf("[%s] media3 would then GET %s (is-local=%t allowed=%t)",
					label, liveRedact(resolved),
					strings.Contains(resolved, "127.0.0.1"), s.aacgMediaHostAllowed(hostOf(resolved)))
			}
			continue
		}
		if endpoint == "segment" && rrChild.Code != http.StatusOK {
			ok = false
			t.Errorf("[%s] segment fetch failed: %s -> %d %s", label, liveRedact(upstream), rrChild.Code, strings.TrimSpace(liveScrubText(string(childBody))))
			continue
		}
		if endpoint == "playlist" && isPlaylist {
			// 子播放列表：再跟进一层，验证更深层分片可用。
			innerChildren := liveProxyChildren(t, string(childBody))
			deep := 0
			for _, inner := range innerChildren {
				if deep >= 2 {
					break
				}
				deep++
				rrDeep := s.liveProxyGet(t, ctx, inner)
				deepBody := rrDeep.Body.Bytes()
				deepEP := liveProxyEndpoint(inner)
				if deepEP == "segment" && liveIsPlaylist(deepBody) {
					ok = false
					t.Errorf("[%s] DEEP MISROUTE: %s", label, liveRedactProxy(inner))
				} else if rrDeep.Code != http.StatusOK {
					ok = false
					t.Errorf("[%s] deep child failed: %s -> %d", label, liveRedactProxy(inner), rrDeep.Code)
				} else if verbose {
					t.Logf("[%s] deep child: endpoint=%s status=%d bytes=%d", label, deepEP, rrDeep.Code, len(deepBody))
				}
			}
		}
	}
	return ok
}

// TestLiveHlsProxyChain 在真实站点上批量还原播放链路，普查各 CDN 形态并
// 检出路由错误；发现即 t.Errorf（打印脱敏细节）。
func TestLiveHlsProxyChain(t *testing.T) {
	tr, err := browser.NewTransport(browser.Options{
		Profile: "chrome_150",
		Proxy:   os.Getenv("AACG_PROXY"),
		Timeout: 25 * time.Second,
	})
	if err != nil {
		t.Fatalf("browser transport init: %v", err)
	}
	c, err := aacg.New(aacg.Options{
		EntryURL:  os.Getenv("AACG_ENTRY_URL"),
		Timeout:   20 * time.Second,
		Transport: tr,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{aacg: c}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	// 目标选择快速路径：AACG_TARGET_URL 直接钉住；否则先试已知镜像，均不可用
	// 才走完整发现（多花几秒，仅作兜底）。
	var candidates []aacg.Target
	if raw := os.Getenv("AACG_TARGET_URL"); raw != "" {
		candidates = append(candidates, aacg.Target{URL: raw})
	}
	candidates = append(candidates, aacg.Target{URL: "https://www.tmcrjpanb.cc/"})
	var target aacg.Target
	for _, cand := range candidates {
		if health, herr := c.Check(ctx, cand); herr == nil && health.Usable {
			target = cand
			t.Logf("target pinned: %s", cand.URL)
			break
		} else {
			t.Logf("target %s unusable: %v", cand.URL, herr)
		}
	}
	if target.URL == "" {
		discovery, derr := c.Discover(ctx)
		if derr != nil {
			t.Fatalf("discovery failed: %v", derr)
		}
		for _, cand := range discovery.Targets {
			if health, herr := c.Check(ctx, cand); herr == nil && health.Usable {
				target = cand
				break
			}
		}
	}
	if target.URL == "" {
		t.Fatal("no usable target")
	}

	// 默认扫首页；AACG_SEARCH_Q 提供时改扫搜索结果（按关键词复现特定帖子的
	// 播放问题）。每条文章的所有视频都跑一遍链路，上限 8 条视频。
	var items []aacg.ArticleSummary
	if q := os.Getenv("AACG_SEARCH_Q"); q != "" {
		list, serr := c.Search(ctx, target.URL, q, 1)
		if serr != nil {
			t.Fatalf("search %q: %v", q, serr)
		}
		items = list.Items
		t.Logf("search %q: %d items", q, len(items))
	} else {
		feed, ferr := c.Feed(ctx, target.URL, 1)
		if ferr != nil {
			t.Fatalf("feed: %v", ferr)
		}
		items = feed.Items
	}

	const maxChains = 8
	checked := 0
	for i, item := range items {
		if i >= 12 || checked >= maxChains {
			break
		}
		detail, derr := c.Article(ctx, item.URL)
		if derr != nil || len(detail.Videos) == 0 {
			continue
		}
		s.registerAacgMediaHosts(aacgVideoHosts(detail)...)
		for vi, video := range detail.Videos {
			if checked >= maxChains {
				break
			}
			list := video.Sources
			if len(list) == 0 {
				list = []string{video.URL}
			}
			// 与真实播放同栈的两阶段探测：playlist=播放列表可取，deep=key/
			// 首分片在直连路径真正可取（菠萝啤类帖子预期 playlist=true、deep=false）。
			usable := ""
			for ci, raw := range list {
				probeStart := time.Now()
				playlistOK, deepOK := s.aacgProbeDeep(ctx, raw)
				t.Logf("article %q video[%d] cand[%d] host=%s playlist=%t deep=%t elapsed=%s",
					item.Title, vi, ci, hostOf(raw), playlistOK, deepOK, time.Since(probeStart).Round(time.Millisecond))
				if playlistOK {
					usable = raw
					break
				}
			}
			if usable == "" {
				t.Logf("article %q video[%d] type=%q: no usable source (%d candidates)", item.Title, vi, video.Type, len(list))
				continue
			}
			checked++
			label := fmt.Sprintf("v%d", checked)
			t.Logf("--- chain %d: article=%q video[%d] type=%q host=%s", checked, item.Title, vi, video.Type, hostOf(usable))
			liveCheckHlsChain(t, s, ctx, label, usable, false)
		}
	}
	if checked == 0 {
		t.Fatal("no video chain could be checked")
	}
	t.Logf("checked %d chains", checked)
}
