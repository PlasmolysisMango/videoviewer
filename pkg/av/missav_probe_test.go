package av

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// stubMissAVSearchSite 伺服 MissAV 搜索结果页并统计命中次数。
// 仅 searchPath 返回 resultsHTML（须含 og:title 以通过 fetchPage 有效性校验），其余 404。
func stubMissAVSearchSite(t *testing.T, searchPath, resultsHTML string) (*httptest.Server, *int64) {
	t.Helper()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == searchPath {
			atomic.AddInt64(&hits, 1)
			html := `<html><head><meta property="og:title" content="搜索结果 | MissAV"></head><body>` +
				resultsHTML + `</body></html>`
			_, _ = w.Write([]byte(html))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// TestMissAVProbeVariantsFromSearch 验证 Probe 用一次搜索请求从结果列表
// 链接归纳出全部变体（无码 > 中字 > 原片），且无关链接不误报。
func TestMissAVProbeVariantsFromSearch(t *testing.T) {
	srv, hits := stubMissAVSearchSite(t, "/cn/search/DM5-001", `
		<a href="/cn/dm5-001-uncensored-leak"><img src="/c1.jpg"></a>
		<a href="/cn/dm5-001-chinese-subtitle"><img src="/c2.jpg"></a>
		<a href="/cn/dm5-001"><img src="/c3.jpg"></a>
		<a href="/dm13/cn/dm5-001"><img src="/c3.jpg"></a>
		<a href="/cn/dm6-999"><img src="/other.jpg"></a>
		<a href="/search/DM5-001?page=2">2</a>
		<a href="/genres">分类</a>
	`)
	hc, err := NewHTTPClient(Options{})
	if err != nil {
		t.Fatalf("http client: %v", err)
	}
	src := NewMissAVSource(hc, srv.URL)

	res, err := src.Probe(context.Background(), "DM5-001")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if got := atomic.LoadInt64(hits); got != 1 {
		t.Fatalf("search requests = %d, want 1 (single-request probing)", got)
	}
	if res.Code != "DM5-001" || res.Source != "missav" {
		t.Fatalf("bad result header: %+v", res)
	}

	var kinds, labels []string
	for _, v := range res.Variants {
		if !v.Available {
			t.Fatalf("variant should be available: %+v", v)
		}
		kinds = append(kinds, v.Kind)
		labels = append(labels, v.Label)
	}
	if want := "uncensored,cnsub,normal"; strings.Join(kinds, ",") != want {
		t.Fatalf("kinds = %v, want [%s] (priority order)", kinds, want)
	}
	if want := "无码,中字,原片"; strings.Join(labels, ",") != want {
		t.Fatalf("labels = %v, want [%s]", labels, want)
	}
}

// TestMissAVProbeNotFound 验证搜索结果不含该番号时返回 ErrNotFound，且不反复请求。
func TestMissAVProbeNotFound(t *testing.T) {
	srv, hits := stubMissAVSearchSite(t, "/cn/search/XX-000", `
		<a href="/cn/dm6-999"><img src="/other.jpg"></a>
	`)
	hc, err := NewHTTPClient(Options{})
	if err != nil {
		t.Fatalf("http client: %v", err)
	}
	src := NewMissAVSource(hc, srv.URL)

	if _, err := src.Probe(context.Background(), "xx-000"); !IsNotFound(err) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if got := atomic.LoadInt64(hits); got != 1 {
		t.Fatalf("search requests = %d, want 1", got)
	}
}

// TestVariantFromSlug 验证链接末段与番号的变体匹配规则。
func TestVariantFromSlug(t *testing.T) {
	cases := []struct {
		slug, code, kind string
		ok               bool
	}{
		{"dm5-001", "DM5-001", "normal", true},
		{"dm5-001-uncensored-leak", "DM5-001", "uncensored", true},
		{"dm5-001-chinese-subtitle", "DM5-001", "cnsub", true},
		{"dm5-001-cd2", "DM5-001", "", false},      // 分片不是变体
		{"dm6-999", "DM5-001", "", false},          // 无关番号
		{"chinese-subtitle", "DM5-001", "", false}, // 纯后缀
		{"", "DM5-001", "", false},
	}
	for _, c := range cases {
		kind, ok := variantFromSlug(c.slug, c.code)
		if ok != c.ok || (ok && kind != c.kind) {
			t.Fatalf("variantFromSlug(%q, %q) = (%q, %v), want (%q, %v)",
				c.slug, c.code, kind, ok, c.kind, c.ok)
		}
	}
}
