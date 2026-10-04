package aacg

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const (
	articleTestHeadline = `<h1 class="post-title" itemprop="name headline">Fixture <em>headline</em></h1>`
	articleTestDate     = `<meta itemprop="datePublished" content="2026-01-02T03:04:05+07:00" />`
	articleTestCover    = `<meta itemprop="image" content="https://img.example.invalid/cover.jpeg" />`
	articleTestAds      = `<div class="txt-apps"><a class="tjtagmanager" href="https://ads.example.invalid/app" data-ad_slot_key="slot" data-ad_id="ad">install the app</a></div>`
	articleTestPromo    = `<blockquote>mirror promo <a href="https://mirror.example.invalid/">link</a></blockquote>`
	articleTestShare    = `<div style="display:flex"><div class="btn-download"><a href="https://app.example.invalid/">download the app</a></div><div class="copy-box">copy the link</div></div>`
	articleTestRelated  = `<p>热门吃瓜</p><table><tr><td><a href="/archives/1/">related one</a></td></tr></table>`
	articleTestNear     = `<div class="post-near"><nav><span class="prev"><a href="/archives/20/" title="Earlier post title"><span class="post-near-span"><span class="prev-t no-user-select color-main">上一篇: </span><br><span>previous entry</span></span></a></span><span class="next"><a href="/archives/21/">Later entry</a></span></nav></div>`
	articleTestAdBanner = `<a data-ad_type="banner" data-creative_id="creative-1"><img alt="ad" src="/static/banner.png" loading="lazy" data-src="https://ads.example.invalid/ad.jpeg" /></a>`
	articleTestRights   = `<p class="content-copyright">published by the fixture site</p><p>版权声明：fixture notice</p>`
	articleTestTags     = `<div class="tags"><a href="/tag/x/">fixture</a></div>`
	articleTestHotNews  = `<section class="hot-news-section" aria-label="hot news">hot news teaser</section>`
	articleTestNotice   = `<div class="content-tabs">official notice block</div>`
	articleTestSibling  = `<div class="post-content" style="margin: 1rem 0"><a href="https://ads.example.invalid/top">promoted app</a></div>`
)

func articleTestPlayer(config string) string {
	return `<div class="dplayer" data-config='` + config + `'><div class="dplayer-loading"><div class="dplayer-loading-text">player loading</div></div></div>`
}

func articleTestBody(parts ...string) string {
	return `<div class="post-content" itemprop="articleBody">` + strings.Join(parts, "") + `</div>`
}

func articleTestArticle(parts ...string) string {
	return `<article itemscope itemtype="https://schema.org/BlogPosting">` + strings.Join(parts, "") + `</article>`
}

func articleTestPage(article string) string {
	return `<!doctype html><html><head><meta name="description" content="Fixture summary." /></head>` +
		`<body><div id="post" role="main">` + article + `</div></body></html>`
}

func articleTestServe(t *testing.T, page string) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	paths := &[]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*paths = append(*paths, r.URL.Path)
		mu.Unlock()
		writeTestHTML(w, page)
	}))
	t.Cleanup(srv.Close)
	return srv, paths
}

func TestArticle(t *testing.T) {
	t.Run("full article", func(t *testing.T) {
		page := articleTestPage(articleTestArticle(
			articleTestHeadline,
			articleTestDate,
			articleTestCover,
			articleTestBody(
				articleTestAds,
				`<div class="line"></div>`,
				articleTestPromo,
				`<p>First paragraph.</p>`,
				articleTestPlayer(`{"video":{"url":"https://cdn.example.invalid/main.m3u8?token=1","type":"hls"},"video_h265":[]}`),
				`<p>Second <strong>paragraph</strong>.</p>`,
				`<ul><li>one</li><li>two</li></ul>`,
				`<p>关键词: #fixture #sample</p>`,
				articleTestShare,
				articleTestRelated,
				articleTestRights,
				articleTestTags,
				articleTestHotNews,
				articleTestNotice,
				articleTestAdBanner,
				articleTestNear,
			),
			articleTestSibling,
		))
		srv, paths := articleTestServe(t, page)
		got, err := newTestClient(t, srv, "").Article(context.Background(), srv.URL+"/archives/277594/")
		want := ArticleDetail{
			ArticleSummary: ArticleSummary{
				Title:       "Fixture headline",
				URL:         srv.URL + "/archives/277594/",
				Summary:     "Fixture summary.",
				CoverURL:    "https://img.example.invalid/cover.jpeg",
				PublishedAt: "2026-01-02T03:04:05+07:00",
			},
			Content:      "First paragraph.\n\nSecond paragraph.\n\none\ntwo\n\n关键词: #fixture #sample",
			ContentParts: []ContentPart{},
			Videos:       []VideoLink{{URL: "https://cdn.example.invalid/main.m3u8?token=1", Type: "hls"}},
			Previous:     &ArticleLink{Title: "Earlier post title", URL: srv.URL + "/archives/20/"},
			Next:         &ArticleLink{Title: "Later entry", URL: srv.URL + "/archives/21/"},
		}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("full article = %+v, %v; want %+v", got, err, want)
		}
		for _, excluded := range []string{"install the app", "mirror promo", "player loading", "download the app", "copy the link", "related one", "previous", "fixture notice", "hot news teaser", "official notice block", "promoted app", "ads.example.invalid", "上一篇", "Earlier post title", "Later entry"} {
			if strings.Contains(got.Content, excluded) {
				t.Fatalf("content leaked %q: %q", excluded, got.Content)
			}
		}
		if want := []string{"/archives/277594/"}; !reflect.DeepEqual(*paths, want) {
			t.Fatalf("requests = %v, want %v", *paths, want)
		}
	})
	t.Run("content links", func(t *testing.T) {
		page := articleTestPage(articleTestArticle(
			articleTestHeadline,
			articleTestBody(
				`<p>See <a href="/archives/12/">first post</a> for details.</p>`,
				`<p>More in <a href="/archives/13/">second post</a>.</p>`,
				`<p>Alias <a href="/archives/12/">first post</a> repeated.</p>`,
				`<p>Ad <a href="https://ads.example.invalid/archives/8/">bonus</a> then <a href="/archives/17/">bonus</a>.</p>`,
				`<p>Self <a href="/archives/99/">this post</a> stays.</p>`,
				`<p>Skip <a href="https://ads.example.invalid/archives/7/">off site</a> and <a href="/category/news/">news</a>.</p>`,
				`<p>Bare <a href="/archives/14/"></a> after.</p>`,
				`<p>Hidden <a href="/archives/15/">in table</a></p><table><tr><td><a href="/archives/16/">stripped link</a></td></tr></table>`,
			),
		))
		srv, _ := articleTestServe(t, page)
		got, err := newTestClient(t, srv, "").Article(context.Background(), srv.URL+"/archives/99/")
		wantParts := []ContentPart{
			{Text: "See "},
			{Text: "first post", URL: srv.URL + "/archives/12/"},
			{Text: " for details.\n\nMore in "},
			{Text: "second post", URL: srv.URL + "/archives/13/"},
			{Text: ".\n\nAlias "},
			{Text: "first post", URL: srv.URL + "/archives/12/"},
			{Text: " repeated.\n\nAd bonus then "},
			{Text: "bonus", URL: srv.URL + "/archives/17/"},
			{Text: ".\n\nSelf this post stays.\n\nSkip off site and news.\n\nBare after.\n\nHidden "},
			{Text: "in table", URL: srv.URL + "/archives/15/"},
		}
		joined := ""
		for _, part := range got.ContentParts {
			joined += part.Text
		}
		if err != nil || !reflect.DeepEqual(got.ContentParts, wantParts) || joined != got.Content {
			t.Fatalf("content links parts = %+v, %v; want %+v (content %q)", got.ContentParts, err, wantParts, got.Content)
		}
	})
	t.Run("content images", func(t *testing.T) {
		page := articleTestPage(articleTestArticle(
			articleTestHeadline,
			articleTestBody(
				`<p>Intro text.</p>`,
				`<p><img alt="one" src="/usr/plugins/tbxw/zw.png?v=3" data-xuid="1" data-xkrkllgl="https://pic.example.invalid/one.jpeg" /></p>`,
				`<p><img alt="two" src="/usr/plugins/tbxw/zw.png?v=3" data-xuid="2" data-xkrkllgl="https://pic.example.invalid/two.jpeg" /></p>`,
				`<p><img alt="three" src="https://pic.example.invalid/three.jpeg" /></p>`,
				`<p><img alt="placeholder" src="/usr/plugins/tbxw/zw.png?v=3" /></p>`,
				`<p>Mid <img alt="mid" src="https://pic.example.invalid/mid.jpeg" /> text.</p>`,
				articleTestAdBanner,
				`<img alt="bottom" src="/static/banner.png" data-src="https://pic.example.invalid/bottom-ads.jpeg" id="article-bottom-ads-2" />`,
				`<table><tr><td><img alt="table" src="https://pic.example.invalid/table.jpeg" /></td></tr></table>`,
				`<p>See <a href="/archives/12/">first <img alt="in" src="https://pic.example.invalid/in-link.jpeg" /> post</a> here.</p>`,
				`<p>Link after <a href="/archives/13/">posted</a></p>`,
				`<p><img alt="before" src="https://pic.example.invalid/before-link.jpeg" /><a href="/archives/14/">prefixed</a> end.</p>`,
				`<p>Tail <img alt="tail" src="https://pic.example.invalid/tail.jpeg" /></p>`,
			),
		))
		srv, _ := articleTestServe(t, page)
		got, err := newTestClient(t, srv, "").Article(context.Background(), srv.URL+"/archives/1/")
		wantContent := "Intro text.\n\nMid text.\n\nSee first post here.\n\nLink after posted\n\nprefixed end.\n\nTail"
		wantParts := []ContentPart{
			{Text: "Intro text."},
			{ImageURL: "https://pic.example.invalid/one.jpeg"},
			{ImageURL: "https://pic.example.invalid/two.jpeg"},
			{ImageURL: "https://pic.example.invalid/three.jpeg"},
			{Text: "\n\nMid"},
			{ImageURL: "https://pic.example.invalid/mid.jpeg"},
			{Text: " text.\n\nSee "},
			{Text: "first post", URL: srv.URL + "/archives/12/"},
			{Text: " here.\n\nLink after "},
			{Text: "posted", URL: srv.URL + "/archives/13/"},
			{ImageURL: "https://pic.example.invalid/before-link.jpeg"},
			{Text: "\n\n"},
			{Text: "prefixed", URL: srv.URL + "/archives/14/"},
			{Text: " end.\n\nTail"},
			{ImageURL: "https://pic.example.invalid/tail.jpeg"},
		}
		joined := ""
		for _, part := range got.ContentParts {
			joined += part.Text
		}
		if err != nil || got.Content != wantContent || !reflect.DeepEqual(got.ContentParts, wantParts) || joined != got.Content {
			t.Fatalf("content images parts = %+v, %v; want %+v (content %q)", got.ContentParts, err, wantParts, got.Content)
		}
	})
	t.Run("near degradations", func(t *testing.T) {
		page := articleTestPage(articleTestArticle(
			articleTestHeadline,
			articleTestBody(
				`<p>Text.</p>`,
				`<div class="post-near"><nav><span class="prev"><a href="https://mirror.example.invalid/archives/20/">off site</a></span><span class="next"><a href="/archives/21/"></a></span></nav></div>`,
			),
		))
		srv, _ := articleTestServe(t, page)
		got, err := newTestClient(t, srv, "").Article(context.Background(), srv.URL+"/archives/1/")
		if err != nil || got.Previous != nil || got.Next != nil {
			t.Fatalf("near degradations = %+v, %v; want nil navigation", got, err)
		}
	})
	t.Run("h265 fallback", func(t *testing.T) {
		page := articleTestPage(articleTestArticle(
			articleTestHeadline,
			articleTestBody(
				articleTestPlayer(`{"video":null,"video_h265":{"url":"https://cdn.example.invalid/fallback.m3u8","type":"hls"}}`),
				`<p>Text.</p>`,
			),
		))
		srv, _ := articleTestServe(t, page)
		got, err := newTestClient(t, srv, "").Article(context.Background(), srv.URL+"/archives/1/")
		want := []VideoLink{{URL: "https://cdn.example.invalid/fallback.m3u8", Type: "hls"}}
		if err != nil || !reflect.DeepEqual(got.Videos, want) || got.Content != "Text." {
			t.Fatalf("h265 fallback = %+v, %v; want videos %+v", got, err, want)
		}
	})
	t.Run("prefer h264 and dedupe", func(t *testing.T) {
		page := articleTestPage(articleTestArticle(
			articleTestHeadline,
			articleTestBody(
				articleTestPlayer(`{"video":{"url":"https://cdn.example.invalid/main.m3u8","type":"hls"},"video_h265":{"url":"https://cdn.example.invalid/backup.m3u8","type":"hls"}}`),
				articleTestPlayer(`{"video":{"url":"https://cdn.example.invalid/main.m3u8","type":"hls"},"video_h265":[]}`),
				`<p>Text.</p>`,
			),
		))
		srv, _ := articleTestServe(t, page)
		got, err := newTestClient(t, srv, "").Article(context.Background(), srv.URL+"/archives/1/")
		want := []VideoLink{{URL: "https://cdn.example.invalid/main.m3u8", Type: "hls"}}
		if err != nil || !reflect.DeepEqual(got.Videos, want) {
			t.Fatalf("dedupe sources = %+v, %v; want %+v", got.Videos, err, want)
		}
	})
	t.Run("relative cover and no player", func(t *testing.T) {
		page := articleTestPage(articleTestArticle(
			articleTestHeadline,
			`<meta itemprop="image" content="/media/cover.jpeg" />`,
			articleTestBody(`<p>Text.</p>`),
		))
		srv, _ := articleTestServe(t, page)
		got, err := newTestClient(t, srv, "").Article(context.Background(), srv.URL+"/archives/1/")
		if err != nil || got.CoverURL != srv.URL+"/media/cover.jpeg" || got.Content != "Text." || got.Videos == nil || len(got.Videos) != 0 {
			t.Fatalf("relative cover = %+v, %v", got, err)
		}
		if got.Previous != nil || got.Next != nil {
			t.Fatalf("absent footer navigation = %v/%v, want nil", got.Previous, got.Next)
		}
	})
}

func TestArticleRejections(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		status int
		want   string
	}{
		{name: "challenge page", body: `<html><head><title>Just a moment</title></head></html>`, want: "challenge page"},
		{name: "server error", status: 500, body: articleTestPage(articleTestArticle(articleTestHeadline)), want: "HTTP 500"},
		{name: "missing article page", body: `<html><body><section></section></body></html>`, want: "expected one article page"},
		{name: "duplicate article page", body: `<html><body><div id="post" role="main"></div><div id="post" role="main"></div></body></html>`, want: "expected one article page, found 2"},
		{name: "missing article", body: `<html><body><div id="post" role="main"><section></section></div></body></html>`, want: "expected one article"},
		{name: "missing headline", body: articleTestPage(articleTestArticle(articleTestDate)), want: "expected one article headline"},
		{name: "empty headline", body: articleTestPage(articleTestArticle(`<h1 itemprop="name headline">  </h1>`)), want: "empty article title"},
		{name: "ambiguous date", body: articleTestPage(articleTestArticle(articleTestHeadline, articleTestDate, articleTestDate)), want: "ambiguous article date"},
		{name: "ambiguous cover", body: articleTestPage(articleTestArticle(articleTestHeadline, articleTestCover, articleTestCover)), want: "ambiguous article cover"},
		{name: "ambiguous summary", body: `<!doctype html><html><head><meta name="description" content="a" /><meta name="description" content="b" /></head><body><div id="post" role="main">` + articleTestArticle(articleTestHeadline) + `</div></body></html>`, want: "ambiguous description"},
		{name: "unsafe cover", body: articleTestPage(articleTestArticle(articleTestHeadline, `<meta itemprop="image" content="javascript:alert(1)" />`)), want: "invalid link"},
		{name: "missing body", body: articleTestPage(articleTestArticle(articleTestHeadline)), want: "expected one article body"},
		{name: "player without config", body: articleTestPage(articleTestArticle(articleTestHeadline, articleTestBody(`<div class="dplayer"></div>`, `<p>Text.</p>`))), want: "player without config"},
		{name: "broken player config", body: articleTestPage(articleTestArticle(articleTestHeadline, articleTestBody(articleTestPlayer(`not-json`), `<p>Text.</p>`))), want: "invalid player config"},
		{name: "empty video link", body: articleTestPage(articleTestArticle(articleTestHeadline, articleTestBody(articleTestPlayer(`{"video":{"url":"","type":"hls"},"video_h265":[]}`), `<p>Text.</p>`))), want: "invalid video link"},
		{name: "empty content", body: articleTestPage(articleTestArticle(articleTestHeadline, articleTestBody(articleTestShare, articleTestRelated))), want: "empty article content"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header()["Content-Type"] = []string{"text/html; charset=utf-8"}
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			detail, err := newTestClient(t, srv, "").Article(context.Background(), srv.URL+"/archives/1/")
			if err == nil || !strings.Contains(err.Error(), tc.want) || !reflect.DeepEqual(detail, ArticleDetail{}) {
				t.Fatalf("outcome = %+v, %v; want %q", detail, err, tc.want)
			}
		})
	}
}
