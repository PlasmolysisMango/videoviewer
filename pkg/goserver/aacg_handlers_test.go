package goserver

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
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
	aacgTestPlayerConfig    = `{"video":{"url":"https://cdn.example.invalid/main.m3u8","type":"hls"},"video_h265":{"url":"https://cdn2.example.invalid/backup.m3u8","type":"hls"}}`
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
		`<p>See <a href="/archives/12/">later post</a> for details.</p>` +
		`<p><img src="/usr/plugins/tbxw/zw.png?v=3" data-xuid="1" data-xkrkllgl="https://pic.example.invalid/body.jpeg" /></p>` +
		`<div class="post-near"><nav><span class="prev"><a href="/archives/9/" title="Earlier post"><span class="post-near-span"><span class="prev-t">上一篇: </span><br><span>previous entry</span></span></a></span><span class="next"><a href="/archives/21/">Later entry</a></span></nav></div>` +
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
		"/archives/12/": aacgTestPage(`<div id="post" role="main"><article itemscope itemtype="https://schema.org/BlogPosting">` +
			aacgTestArticleHeadline +
			`<div class="post-content" itemprop="articleBody"><p>Only paragraph.</p></div></article></div>`),
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
	aacgWantString(t, body, "content", "First paragraph.\n\nSee later post for details.")
	parts, ok := body["content_parts"].([]any)
	if !ok || len(parts) != 4 {
		t.Fatalf("content_parts = %v, want 4 entries", body["content_parts"])
	}
	textPart, _ := parts[0].(map[string]any)
	aacgWantString(t, textPart, "text", "First paragraph.\n\nSee ")
	aacgWantString(t, textPart, "url", "")
	aacgWantString(t, textPart, "image_url", "")
	linkPart, _ := parts[1].(map[string]any)
	aacgWantString(t, linkPart, "text", "later post")
	aacgWantString(t, linkPart, "url", upstream.URL+"/archives/12/")
	aacgWantString(t, linkPart, "image_url", "")
	tailPart, _ := parts[2].(map[string]any)
	aacgWantString(t, tailPart, "text", " for details.")
	aacgWantString(t, tailPart, "url", "")
	aacgWantString(t, tailPart, "image_url", "")
	imagePart, _ := parts[3].(map[string]any)
	aacgWantString(t, imagePart, "text", "")
	aacgWantString(t, imagePart, "url", "")
	aacgWantString(t, imagePart, "image_url", "https://pic.example.invalid/body.jpeg")
	prev, ok := body["prev"].(map[string]any)
	if !ok {
		t.Fatalf("prev = %v, want object", body["prev"])
	}
	aacgWantString(t, prev, "title", "Earlier post")
	aacgWantString(t, prev, "url", upstream.URL+"/archives/9/")
	next, ok := body["next"].(map[string]any)
	if !ok {
		t.Fatalf("next = %v, want object", body["next"])
	}
	aacgWantString(t, next, "title", "Later entry")
	aacgWantString(t, next, "url", upstream.URL+"/archives/21/")
	videos, ok := body["videos"].([]any)
	if !ok || len(videos) != 1 {
		t.Fatalf("videos = %v, want 1 entry", body["videos"])
	}
	video, _ := videos[0].(map[string]any)
	aacgWantString(t, video, "url", "https://cdn.example.invalid/main.m3u8")
	aacgWantString(t, video, "type", "hls")
	sources, ok := video["sources"].([]any)
	if !ok || len(sources) != 2 ||
		sources[0] != "https://cdn.example.invalid/main.m3u8" ||
		sources[1] != "https://cdn2.example.invalid/backup.m3u8" {
		t.Fatalf("sources = %v, want [main backup]", video["sources"])
	}
	// 文章响应同时把媒体主机注册进 HLS 代理白名单（probe/播放的前置条件）。
	if !s.aacgMediaHostAllowed("cdn.example.invalid") || !s.aacgMediaHostAllowed("cdn2.example.invalid") {
		t.Fatalf("article media hosts not registered: %v", s.aacgMediaHosts)
	}
	s.aacgMediaMu.Lock()
	registered := len(s.aacgMediaHosts)
	s.aacgMediaMu.Unlock()
	if registered != 2 {
		t.Fatalf("registered media hosts = %d, want 2", registered)
	}
	aacgWantPaths(t, paths(), "/archives/11/")

	// 缺少 url → 400
	code, _ = aacgGetJSON(t, s.handleAacgArticle, "/api/aacg/article")
	if code != http.StatusBadRequest {
		t.Fatalf("missing url status = %d, want 400", code)
	}

	// 旧文章模板无 post-near：键仍在，值为 null。
	code, body = aacgGetJSON(t, s.handleAacgArticle, "/api/aacg/article?"+url.Values{"url": {upstream.URL + "/archives/12/"}}.Encode())
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", code, body)
	}
	aacgWantString(t, body, "content", "Only paragraph.")
	if body["prev"] != nil || body["next"] != nil {
		t.Fatalf("prev/next = %v/%v, want null/null", body["prev"], body["next"])
	}
	aacgWantPaths(t, paths(), "/archives/11/", "/archives/12/")
}

// aacgTestVideoArticlePage 生成视频地址指向定制上游的文章页（HLS 代理测试用）。
func aacgTestVideoArticlePage(videoURL string) string {
	return aacgTestPage(`<div id="post" role="main"><article itemscope itemtype="https://schema.org/BlogPosting">` +
		aacgTestArticleHeadline +
		`<div class="post-content" itemprop="articleBody"><p>Fixture paragraph.</p>` +
		`<div class="dplayer" data-config='{"video":{"url":"` + videoURL + `","type":"hls"},"video_h265":[]}'></div>` +
		`</div></article></div>`)
}

// aacgTestProxyURL 解析改写后的代理 URL，须指向 httptest 请求的默认 Host。
func aacgTestProxyURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host != "example.com" || !strings.HasPrefix(u.Path, "/api/hls/") {
		t.Fatalf("proxy URL %q, %v", raw, err)
	}
	return u
}

// aacgTestProxyURI 从 #EXT-X-KEY / #EXT-X-MAP 行中取出改写后的 URI 并解析。
func aacgTestProxyURI(t *testing.T, line string) *url.URL {
	t.Helper()
	start := strings.Index(line, `URI="`)
	if start < 0 {
		t.Fatalf("no URI in %q", line)
	}
	rest := line[start+5:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatalf("unterminated URI in %q", line)
	}
	return aacgTestProxyURL(t, rest[:end])
}

// HLS 代理的 AACG 通路：文章注册的主机经 aacg 网络栈取流并改写内层 URI；
// 内层主机随 playlist 注册放行；白名单外的未注册主机保持 403。
func TestAacgHlsProxy(t *testing.T) {
	const (
		playlistBody = "#EXTM3U\n" +
			"#EXT-X-VERSION:3\n" +
			"#EXT-X-TARGETDURATION:10\n" +
			"#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\"\n" +
			"#EXT-X-MAP:URI=\"https://inner.example.invalid/init.mp4\"\n" +
			"#EXTINF:10.0,\n" +
			"seg1.ts\n" +
			"#EXTINF:10.0,\n" +
			"seg2.ts\n" +
			"#EXT-X-ENDLIST\n"
		segmentBody = "\x47fixture-ts-payload"
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/archives/11/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, aacgTestVideoArticlePage("http://"+r.Host+"/media/main.m3u8"))
		case "/media/main.m3u8":
			fmt.Fprint(w, playlistBody)
		case "/media/seg1.ts":
			w.Header().Set("Content-Type", "video/mp2t")
			fmt.Fprint(w, segmentBody)
		default:
			t.Errorf("unexpected upstream request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	s := newAacgTestServer(t, upstream)

	// 文章响应后媒体主机已注册（app 随后 probe/播放的前置条件）。
	code, body := aacgGetJSON(t, s.handleAacgArticle, "/api/aacg/article?"+url.Values{"url": {upstream.URL + "/archives/11/"}}.Encode())
	if code != http.StatusOK {
		t.Fatalf("article status = %d, body = %v", code, body)
	}
	if !s.aacgMediaHostAllowed("127.0.0.1") {
		t.Fatalf("article media host not registered")
	}
	if s.aacgMediaHostAllowed("inner.example.invalid") {
		t.Fatalf("playlist-inner host registered too early")
	}

	// playlist：经 aacg.Fetch 拉取 → 相对/内层 URI 改写为本代理 + 内层主机注册。
	rec := httptest.NewRecorder()
	s.handleHlsPlaylist(rec, httptest.NewRequest(http.MethodGet, "/api/hls/playlist?"+url.Values{"u": {upstream.URL + "/media/main.m3u8"}}.Encode(), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("playlist status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Fatalf("playlist Content-Type = %q", ct)
	}
	lines := strings.Split(rec.Body.String(), "\n")
	if len(lines) != 11 || lines[0] != "#EXTM3U" || lines[5] != "#EXTINF:10.0," || lines[9] != "#EXT-X-ENDLIST" {
		t.Fatalf("playlist body = %q", rec.Body.String())
	}
	if !s.aacgMediaHostAllowed("inner.example.invalid") {
		t.Fatalf("playlist-inner host not registered before response")
	}
	seg := aacgTestProxyURL(t, lines[6])
	if seg.Path != "/api/hls/segment" || seg.Query().Get("u") != upstream.URL+"/media/seg1.ts" {
		t.Fatalf("segment line rewritten to %q", lines[6])
	}
	key := aacgTestProxyURI(t, lines[3])
	if key.Path != "/api/hls/segment" || key.Query().Get("u") != upstream.URL+"/media/key.bin" {
		t.Fatalf("key URI rewritten to %q", lines[3])
	}
	init := aacgTestProxyURI(t, lines[4])
	if init.Path != "/api/hls/segment" || init.Query().Get("u") != "https://inner.example.invalid/init.mp4" {
		t.Fatalf("init URI rewritten to %q", lines[4])
	}

	// 改写出的分片 URL 直接可用：原文回传（内层主机已随 playlist 放行）。
	rec = httptest.NewRecorder()
	s.handleHlsSegment(rec, httptest.NewRequest(http.MethodGet, seg.String(), nil))
	if rec.Code != http.StatusOK || rec.Body.String() != segmentBody {
		t.Fatalf("segment status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp2t" {
		t.Fatalf("segment Content-Type = %q", ct)
	}

	// 未注册主机仍是 403；非法 scheme 仍是 400。
	rec = httptest.NewRecorder()
	s.handleHlsPlaylist(rec, httptest.NewRequest(http.MethodGet, "/api/hls/playlist?"+url.Values{"u": {"https://unregistered.example.invalid/x.m3u8"}}.Encode(), nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unregistered host status = %d, want 403", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.handleHlsSegment(rec, httptest.NewRequest(http.MethodGet, "/api/hls/segment?"+url.Values{"u": {"file:///etc/passwd"}}.Encode(), nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unsafe scheme status = %d, want 400", rec.Code)
	}
}

// probe：播放前动态选源的判定链（注册主机 + 200 + #EXTM3U），只回判定结果。
func TestAacgProbeHandler(t *testing.T) {
	upstream, paths := aacgTestUpstream(t, map[string]string{
		"/good.m3u8":  "#EXTM3U\n#EXT-X-ENDLIST\n",
		"/plain.html": "<html>not a playlist</html>",
	})
	s := newAacgTestServer(t, upstream)
	s.registerAacgMediaHosts("127.0.0.1")
	good := upstream.URL + "/good.m3u8"
	missing := upstream.URL + "/missing.m3u8"
	plain := upstream.URL + "/plain.html"

	// 首个 404、次个 200+#EXTM3U → 返回次者（恒 200；顺序即候选优先级）。
	target := "/api/aacg/probe?" + url.Values{"url": {missing, good}}.Encode()
	code, body := aacgGetJSON(t, s.handleAacgProbe, target)
	if code != http.StatusOK {
		t.Fatalf("probe status = %d, body = %v", code, body)
	}
	aacgWantString(t, body, "url", good)
	aacgWantPaths(t, paths(), "/missing.m3u8", "/good.m3u8")

	// 非播放列表正文 / 未注册主机 / 非法 URL → 空串（候选不发出请求）。
	target = "/api/aacg/probe?" + url.Values{"url": {plain, "https://unregistered.example.invalid/x.m3u8", "file:///etc/passwd"}}.Encode()
	code, body = aacgGetJSON(t, s.handleAacgProbe, target)
	if code != http.StatusOK {
		t.Fatalf("probe status = %d, body = %v", code, body)
	}
	aacgWantString(t, body, "url", "")
	aacgWantPaths(t, paths(), "/missing.m3u8", "/good.m3u8", "/plain.html")

	// 参数个数校验：0 个或超过 4 个 → 400。
	if code, _ = aacgGetJSON(t, s.handleAacgProbe, "/api/aacg/probe"); code != http.StatusBadRequest {
		t.Fatalf("no-url status = %d, want 400", code)
	}
	many := "/api/aacg/probe?" + url.Values{"url": {good, good, good, good, good}}.Encode()
	if code, _ = aacgGetJSON(t, s.handleAacgProbe, many); code != http.StatusBadRequest {
		t.Fatalf("too-many status = %d, want 400", code)
	}
}

// aacgTestEncryptImage 复刻站点图片混淆（AES-128-CBC + PKCS7），供代理解密测试用。
func aacgTestEncryptImage(t *testing.T, plain []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher([]byte("f5d965df75336270"))
	if err != nil {
		t.Fatal(err)
	}
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte(nil), plain...), bytes.Repeat([]byte{byte(padding)}, padding)...)
	encrypted := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, []byte("97b60394abc2fbe1")).CryptBlocks(encrypted, padded)
	return encrypted
}

func TestAacgImageHandler(t *testing.T) {
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 'J', 'F', 'I', 'F', 1, 2, 3}
	encrypted := aacgTestEncryptImage(t, jpeg)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cover.jpeg":
			w.Write(encrypted)
		case "/no-image.bin":
			fmt.Fprint(w, "not an image")
		default:
			t.Errorf("unexpected image request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	s := newAacgTestServer(t, upstream)

	target := "/api/aacg/image?" + url.Values{"url": {upstream.URL + "/cover.jpeg"}}.Encode()
	rec := httptest.NewRecorder()
	s.handleAacgImage(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("Content-Type = %q, want image/jpeg", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=86400" {
		t.Fatalf("Cache-Control = %q, want public, max-age=86400", cc)
	}
	if !bytes.Equal(rec.Body.Bytes(), jpeg) {
		t.Fatalf("body = %x, want %x", rec.Body.Bytes(), jpeg)
	}

	// 缺少 url → 400
	code, _ := aacgGetJSON(t, s.handleAacgImage, "/api/aacg/image")
	if code != http.StatusBadRequest {
		t.Fatalf("missing url status = %d, want 400", code)
	}

	// 上游返回无法识别的载荷 → 502
	garbage := "/api/aacg/image?" + url.Values{"url": {upstream.URL + "/no-image.bin"}}.Encode()
	code, _ = aacgGetJSON(t, s.handleAacgImage, garbage)
	if code != http.StatusBadGateway {
		t.Fatalf("unrecognized payload status = %d, want 502", code)
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
		"image":      s.handleAacgImage,
		"probe":      s.handleAacgProbe,
	} {
		code, _ := aacgGetJSON(t, h, "/api/aacg/"+name)
		if code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, want 503", name, code)
		}
	}
}
