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
	articleTestNear     = `<div class="post-near">previous: <a href="/archives/2/">first post</a></div>`
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
				articleTestNear,
				articleTestRights,
				articleTestTags,
				articleTestHotNews,
				articleTestNotice,
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
			Content: "First paragraph.\n\nSecond paragraph.\n\none\ntwo\n\n关键词: #fixture #sample",
			Videos:  []VideoLink{{URL: "https://cdn.example.invalid/main.m3u8?token=1", Type: "hls"}},
		}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("full article = %+v, %v; want %+v", got, err, want)
		}
		for _, excluded := range []string{"install the app", "mirror promo", "player loading", "download the app", "copy the link", "related one", "previous", "fixture notice", "hot news teaser", "official notice block", "promoted app", "ads.example.invalid"} {
			if strings.Contains(got.Content, excluded) {
				t.Fatalf("content leaked %q: %q", excluded, got.Content)
			}
		}
		if want := []string{"/archives/277594/"}; !reflect.DeepEqual(*paths, want) {
			t.Fatalf("requests = %v, want %v", *paths, want)
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
