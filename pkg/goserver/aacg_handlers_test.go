package goserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"videoviewer/pkg/aacg"
)

// AACG handler 测试：fixture 形状对齐真实站点结构（同 pkg/aacg 测试约定），
// 域名一律 .invalid、正文中性。通过种子缓存跳过镜像发现，并给客户端注入
// httptest Transport（绕开 dialPublic 的 loopback 拦截），不触真实网络。

const (
	aacgTestBlogTitle = `<h1 class="blog-title">Fixture Site</h1>`
	aacgTestCardOne   = `<article itemscope itemtype="http://schema.org/BlogPosting"><meta itemprop="url mainEntityOfPage" content="/archives/11/" /><a href="/archives/11/"><div class="post-card" id="post-card-11"><script type="text/javascript">loadBannerDirect('https://pic.example.invalid/one.jpeg', '', document.querySelector('#post-card-11'));</script><h2 class="post-card-title" itemprop="headline">First headline</h2><span itemprop="datePublished" content="2026-10-03T17:37:24+00:00">2026 年 10 月 03 日</span></div></a></article>`
	aacgTestCardTwo   = `<article itemscope itemtype="http://schema.org/BlogPosting"><meta itemprop="url mainEntityOfPage" content="/archives/12/" /><a href="/archives/12/"><div class="post-card" id="post-card-12"><script type="text/javascript">loadBannerDirect('', '', document.querySelector('#post-card-12'));</script><h2 class="post-card-title" itemprop="headline">Second headline</h2><span itemprop="datePublished" content="2026-10-03T17:30:00+00:00">2026 年 10 月 03 日</span></div></a></article>`

	aacgTestHomeInfo1 = `<span class="page-info"> 1/3 </span>`
	aacgTestHomeInfo2 = `<span class="page-info"> 2/3 </span>`
	aacgTestHomeNav1  = `<ul class="page-navigator"><li class="active"><a href="/">1</a></li><li><a href="/page/2/">2</a></li><li class="next"><a href="/page/2/">next</a></li></ul>`
	aacgTestHomeNav2  = `<ul class="page-navigator"><li class="prev"><a href="/">prev</a></li><li><a href="/">1</a></li><li class="active"><a href="/page/2/">2</a></li><li class="next"><a href="/page/3/">next</a></li></ul>`

	aacgTestNavbar = `<nav id="navbar"><a href="/">Home</a><a href="/category/news/">News</a><a href="/category/fun/">Fun</a></nav>`

	aacgTestBreadcrumb   = `<div class="nav-breadcrumb-wrap"><a class="action-butn" href="/">Home</a><a class="action-butn" href="/category/news/">News</a></div>`
	aacgTestCategoryInfo = `<span class="page-info"> 1/5 </span>`
	aacgTestCategoryNav  = `<ul class="page-navigator"><li class="active"><a href="/category/news/">1</a></li><li class="next"><a href="/category/news/2/">next</a></li></ul>`

	aacgTestSearchHeading = `<h1 class="blog-title">Articles matching 51</h1>`
	aacgTestSearchCrumbs  = `<div class="archive-nav-breadcrumb-wrap"><div class="nav-breadcrumb-wrap"></div></div>`
	aacgTestSearchInfo    = `<span class="page-info"> 1/20 </span>`
	aacgTestSearchNav     = `<ul class="page-navigator"><li class="active"><a href="/search/51/">1</a></li><li class="next"><a href="/search/51/2/">next</a></li></ul>`

	aacgTestArticleHeadline = `<h1 class="post-title" itemprop="name headline">Fixture headline</h1>`
	aacgTestArticleDate     = `<meta itemprop="datePublished" content="2026-01-02T03:04:05+07:00" />`
	aacgTestArticleCover    = `<meta itemprop="image" content="https://img.example.invalid/cover.jpeg" />`
	aacgTestPlayerConfig    = `{"video":{"url":"https://cdn.example.invalid/main.m3u8","type":"hls"},"video_h265":[]}`
)

func aacgTestIndex(parts ...string) string {
	return `<div id="index" role="main">` + strings.Join(parts, "") + `</div>`
}

func aacgTestArchive(parts ...string) string {
	return `<div id="archive" role="main">` + strings.Join(parts, "") + `</div>`
}

func aacgTestPageNav(info, nav string) string {
	return `<div class="page-nav">` + info + nav + `</div>`
}

func aacgTestPage(body string) string {
	return `<html><body>` + body + `</body></html>`
}

func aacgTestArticlePage() string {
	return `<!doctype html><html><head><meta name="description" content="Fixture summary." /></head><body><div id="post" role="main">` +
		`<article itemscope itemtype="https://schema.org/BlogPosting">` +
		aacgTestArticleHeadline + aacgTestArticleDate + aacgTestArticleCover +
		`<div class="post-content" itemprop="articleBody"><p>First paragraph.</p>` +
		`<div class="dplayer" data-config='` + aacgTestPlayerConfig + `'></div>` +
		`</div></article></div></body></html>`
}

// aacgTestUpstream 起一个按路径分发 fixture 的上游并记录请求路径。
func aacgTestUpstream(t *testing.T, routes map[string]string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		body, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

// newAacgTestServer 构造带测试客户端（注入 loopback Transport）与已种子
// 目标缓存（跳过 Discover）的 Server。
func newAacgTestServer(t *testing.T, upstream *httptest.Server) *Server {
	t.Helper()
	client, err := aacg.New(aacg.Options{
		EntryURL:  upstream.URL + "/",
		Transport: upstream.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{aacg: client}
	s.aacgTarget = aacg.Target{URL: upstream.URL + "/"}
	s.aacgAt = time.Now()
	return s
}

func aacgGetJSON(t *testing.T, h http.HandlerFunc, target string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, target, nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return rec.Code, body
}

func aacgWantString(t *testing.T, body map[string]any, key, want string) {
	t.Helper()
	if got := body[key]; got != want {
		t.Fatalf("%s = %v, want %q", key, got, want)
	}
}

func aacgWantNum(t *testing.T, body map[string]any, key string, want float64) {
	t.Helper()
	if got := body[key]; got != want {
		t.Fatalf("%s = %v, want %v", key, got, want)
	}
}

func aacgWantItems(t *testing.T, body map[string]any, want int) []map[string]any {
	t.Helper()
	raw, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("items = %v, want array", body["items"])
	}
	if len(raw) != want {
		t.Fatalf("len(items) = %d, want %d", len(raw), want)
	}
	items := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("item = %v, want object", item)
		}
		items = append(items, m)
	}
	return items
}

func aacgWantPaths(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("requests = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("requests = %v, want %v", got, want)
		}
	}
}

func TestAacgHomeHandler(t *testing.T) {
	upstream, paths := aacgTestUpstream(t, map[string]string{
		"/":        aacgTestPage(aacgTestBlogTitle + aacgTestIndex(aacgTestCardOne, aacgTestCardTwo, aacgTestPageNav(aacgTestHomeInfo1, aacgTestHomeNav1))),
		"/page/2/": aacgTestPage(aacgTestBlogTitle + aacgTestIndex(aacgTestCardOne, aacgTestPageNav(aacgTestHomeInfo2, aacgTestHomeNav2))),
	})
	s := newAacgTestServer(t, upstream)

	code, body := aacgGetJSON(t, s.handleAacgHome, "/api/aacg/home?page=2")
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", code, body)
	}
	aacgWantString(t, body, "title", "Fixture Site")
	aacgWantString(t, body, "site", upstream.URL+"/")
	aacgWantNum(t, body, "page", 2)
	aacgWantNum(t, body, "maxPage", 3)
	if body["hasNext"] != true || body["hasPrev"] != true {
		t.Fatalf("hasNext/hasPrev = %v/%v, want true/true", body["hasNext"], body["hasPrev"])
	}
	items := aacgWantItems(t, body, 1)
	aacgWantString(t, items[0], "title", "First headline")
	aacgWantString(t, items[0], "url", upstream.URL+"/archives/11/")
	aacgWantString(t, items[0], "cover_url", "https://pic.example.invalid/one.jpeg")
	aacgWantString(t, items[0], "published_at", "2026-10-03T17:37:24+00:00")
	aacgWantPaths(t, paths(), "/page/2/")

	// page 缺省/非法归一到 1，且不再触发任何发现请求（缓存命中）。
	code, body = aacgGetJSON(t, s.handleAacgHome, "/api/aacg/home?page=0")
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", code, body)
	}
	aacgWantNum(t, body, "page", 1)
	aacgWantItems(t, body, 2)
	aacgWantPaths(t, paths(), "/page/2/", "/")
}

func TestAacgCategoriesHandler(t *testing.T) {
	upstream, paths := aacgTestUpstream(t, map[string]string{
		"/": aacgTestPage(aacgTestNavbar + aacgTestBlogTitle + aacgTestIndex(aacgTestCardOne, aacgTestPageNav(aacgTestHomeInfo1, aacgTestHomeNav1))),
	})
	s := newAacgTestServer(t, upstream)

	code, body := aacgGetJSON(t, s.handleAacgCategories, "/api/aacg/categories")
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", code, body)
	}
	aacgWantString(t, body, "site", upstream.URL+"/")
	raw, ok := body["categories"].([]any)
	if !ok || len(raw) != 2 {
		t.Fatalf("categories = %v, want 2 entries", body["categories"])
	}
	first, _ := raw[0].(map[string]any)
	aacgWantString(t, first, "name", "News")
	aacgWantString(t, first, "url", upstream.URL+"/category/news/")
	second, _ := raw[1].(map[string]any)
	aacgWantString(t, second, "url", upstream.URL+"/category/fun/")
	aacgWantPaths(t, paths(), "/")
}

func TestAacgFeedHandler(t *testing.T) {
	upstream, paths := aacgTestUpstream(t, map[string]string{
		"/category/news/": aacgTestPage(aacgTestArchive(aacgTestBreadcrumb, aacgTestCardOne, aacgTestPageNav(aacgTestCategoryInfo, aacgTestCategoryNav))),
	})
	s := newAacgTestServer(t, upstream)

	target := "/api/aacg/feed?" + url.Values{"url": {upstream.URL + "/category/news/"}, "page": {"1"}}.Encode()
	code, body := aacgGetJSON(t, s.handleAacgFeed, target)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", code, body)
	}
	aacgWantString(t, body, "title", "News")
	aacgWantNum(t, body, "page", 1)
	aacgWantNum(t, body, "maxPage", 5)
	if body["hasNext"] != true {
		t.Fatalf("hasNext = %v, want true", body["hasNext"])
	}
	aacgWantItems(t, body, 1)
	aacgWantPaths(t, paths(), "/category/news/")

	// 缺少 url → 400
	code, _ = aacgGetJSON(t, s.handleAacgFeed, "/api/aacg/feed")
	if code != http.StatusBadRequest {
		t.Fatalf("missing url status = %d, want 400", code)
	}

	// 非 feed 形态 URL 被表单校验拒绝 → 502
	bad := "/api/aacg/feed?" + url.Values{"url": {upstream.URL + "/archives/11/"}}.Encode()
	code, _ = aacgGetJSON(t, s.handleAacgFeed, bad)
	if code != http.StatusBadGateway {
		t.Fatalf("bad feed url status = %d, want 502", code)
	}
}

func TestAacgSearchHandler(t *testing.T) {
	upstream, paths := aacgTestUpstream(t, map[string]string{
		"/search/51/": aacgTestPage(aacgTestSearchHeading + aacgTestArchive(aacgTestSearchCrumbs, aacgTestCardOne, aacgTestPageNav(aacgTestSearchInfo, aacgTestSearchNav))),
	})
	s := newAacgTestServer(t, upstream)

	code, body := aacgGetJSON(t, s.handleAacgSearch, "/api/aacg/search?q=51")
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", code, body)
	}
	aacgWantString(t, body, "title", "Articles matching 51")
	aacgWantNum(t, body, "page", 1)
	aacgWantNum(t, body, "maxPage", 20)
	if body["hasNext"] != true {
		t.Fatalf("hasNext = %v, want true", body["hasNext"])
	}
	aacgWantItems(t, body, 1)
	aacgWantPaths(t, paths(), "/search/51/")

	// 缺少 q → 400
	code, _ = aacgGetJSON(t, s.handleAacgSearch, "/api/aacg/search")
	if code != http.StatusBadRequest {
		t.Fatalf("missing q status = %d, want 400", code)
	}
}

func TestAacgArticleHandler(t *testing.T) {
	upstream, paths := aacgTestUpstream(t, map[string]string{
		"/archives/11/": aacgTestArticlePage(),
	})
	s := newAacgTestServer(t, upstream)

	target := "/api/aacg/article?" + url.Values{"url": {upstream.URL + "/archives/11/"}}.Encode()
	code, body := aacgGetJSON(t, s.handleAacgArticle, target)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", code, body)
	}
	aacgWantString(t, body, "title", "Fixture headline")
	aacgWantString(t, body, "url", upstream.URL+"/archives/11/")
	aacgWantString(t, body, "summary", "Fixture summary.")
	aacgWantString(t, body, "cover_url", "https://img.example.invalid/cover.jpeg")
	aacgWantString(t, body, "published_at", "2026-01-02T03:04:05+07:00")
	content, _ := body["content"].(string)
	if !strings.Contains(content, "First paragraph.") {
		t.Fatalf("content = %q, want First paragraph.", content)
	}
	videos, ok := body["videos"].([]any)
	if !ok || len(videos) != 1 {
		t.Fatalf("videos = %v, want 1 entry", body["videos"])
	}
	video, _ := videos[0].(map[string]any)
	aacgWantString(t, video, "url", "https://cdn.example.invalid/main.m3u8")
	aacgWantString(t, video, "type", "hls")
	aacgWantPaths(t, paths(), "/archives/11/")

	// 缺少 url → 400
	code, _ = aacgGetJSON(t, s.handleAacgArticle, "/api/aacg/article")
	if code != http.StatusBadRequest {
		t.Fatalf("missing url status = %d, want 400", code)
	}
}

func TestAacgInvalidateOnFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(upstream.Close)
	s := newAacgTestServer(t, upstream)

	code, _ := aacgGetJSON(t, s.handleAacgHome, "/api/aacg/home")
	if code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", code)
	}
	if s.aacgTarget.URL != "" || !s.aacgAt.IsZero() {
		t.Fatalf("target cache not invalidated: %+v @ %v", s.aacgTarget, s.aacgAt)
	}
}

func TestAacgClientUnavailable(t *testing.T) {
	s := &Server{}
	for name, h := range map[string]http.HandlerFunc{
		"home":       s.handleAacgHome,
		"categories": s.handleAacgCategories,
		"feed":       s.handleAacgFeed,
		"search":     s.handleAacgSearch,
		"article":    s.handleAacgArticle,
	} {
		code, _ := aacgGetJSON(t, h, "/api/aacg/"+name)
		if code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, want 503", name, code)
		}
	}
}
