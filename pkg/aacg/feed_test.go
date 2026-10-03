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
	feedTestBlogTitle   = `<h1 class="blog-title">样例站点 - 仅用于测试</h1>`
	feedTestHomeInfo    = `<span class="page-info"> 1/3077 </span>`
	feedTestHomeInfoTwo = `<span class="page-info"> 2/3077 </span>`
	feedTestHomeNav     = `<ul class="page-navigator"><li class="active"><a href="/">1</a></li><li><span></span></li><li><a href="/page/3077/">3077</a></li><li class="next"><a href="/page/2/">下一页</a></li></ul>`
	feedTestHomeNavTwo  = `<ul class="page-navigator"><li class="prev"><a href="/">上一页</a></li><li><a href="/">1</a></li><li class="active"><a href="/page/2/">2</a></li><li><span></span></li><li><a href="/page/3077/">3077</a></li><li class="next"><a href="/page/3/">下一页</a></li></ul>`
	feedTestPromoCard   = `<article itemscope itemtype="http://schema.org/BlogPosting"><meta itemprop="url mainEntityOfPage" content="/archives/999001/" /><a href="/archives/999001/"><div class="post-card" id="post-card-999001"><script type="text/javascript">loadBannerDirect('https://pic.example.invalid/promo.jpeg', '', document.querySelector('#post-card-999001'));</script><h2 class="post-card-title" itemprop="headline"><div class="wrap"><span class="wraps">热搜 HOT</span></div></h2><div class="post-card-info"></div></div></a></article>`
)

func feedTestIndex(parts ...string) string {
	return `<div id="index" role="main">` + strings.Join(parts, "") + `</div>`
}

func TestFeedPageURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		page int
		want string
	}{
		{"home page one", "https://site.invalid/", 1, "https://site.invalid/"},
		{"home without trailing slash", "https://site.invalid", 1, "https://site.invalid/"},
		{"home numbered page", "https://site.invalid/", 3, "https://site.invalid/page/3/"},
		{"replaces home page suffix", "https://site.invalid/page/5/", 2, "https://site.invalid/page/2/"},
		{"drops home page suffix for one", "https://site.invalid/page/2/", 1, "https://site.invalid/"},
		{"drops query and fragment", "https://site.invalid/?from=menu#top", 2, "https://site.invalid/page/2/"},
		{"keeps port", "https://site.invalid:8443/", 2, "https://site.invalid:8443/page/2/"},
		{"category page one", "https://site.invalid/category/wpcz/", 1, "https://site.invalid/category/wpcz/"},
		{"category numbered page", "https://site.invalid/category/wpcz/", 2, "https://site.invalid/category/wpcz/2/"},
		{"replaces category page suffix", "https://site.invalid/category/wpcz/9/", 3, "https://site.invalid/category/wpcz/3/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := feedPageURL(tc.raw, tc.page)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name string
		raw  string
		page int
		want string
	}{
		{"zero page", "https://site.invalid/", 0, "invalid page"},
		{"archive path", "https://site.invalid/archives/277594/", 1, "not a feed URL"},
		{"page without number", "https://site.invalid/page/", 1, "not a feed URL"},
		{"non numeric page", "https://site.invalid/page/latest/", 1, "not a feed URL"},
		{"too deep home", "https://site.invalid/page/2/extra/", 1, "not a feed URL"},
		{"category root", "https://site.invalid/category/", 1, "not a category URL"},
		{"deep category", "https://site.invalid/category/a/b/2/", 1, "not a category URL"},
		{"not http", "file:///category/wpcz/", 1, "invalid feed URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := feedPageURL(tc.raw, tc.page)
			if err == nil || !strings.Contains(err.Error(), tc.want) || got != "" {
				t.Fatalf("got %q, %v; want error %q", got, err, tc.want)
			}
		})
	}
}

func TestFeedHome(t *testing.T) {
	t.Run("page one", func(t *testing.T) {
		var mu sync.Mutex
		var paths []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			paths = append(paths, r.URL.Path)
			mu.Unlock()
			writeTestHTML(w, listTestPage(feedTestBlogTitle+feedTestIndex(
				listTestCardOne,
				feedTestPromoCard,
				listTestCardTwo,
				`<div class="page-nav">`+feedTestHomeInfo+feedTestHomeNav+`</div>`,
			)))
		}))
		defer srv.Close()
		got, err := newTestClient(t, srv, "").Feed(context.Background(), srv.URL+"/", 1)
		want := ArticleList{
			Title: "样例站点 - 仅用于测试",
			Items: []ArticleSummary{
				{Title: "First headline", URL: srv.URL + "/archives/277594/", CoverURL: "https://pic.example.invalid/one.jpeg", PublishedAt: "2026-10-03T17:37:24+00:00"},
				{Title: "Second headline", URL: srv.URL + "/archives/277585/", PublishedAt: "2026-10-03T17:30:00+00:00"},
			},
			Pagination: Pagination{Current: 1, Total: 3077, NextURL: srv.URL + "/page/2/"},
		}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("page one = %+v, %v; want %+v", got, err, want)
		}
		mu.Lock()
		defer mu.Unlock()
		if !reflect.DeepEqual(paths, []string{"/"}) {
			t.Fatalf("requests = %v", paths)
		}
	})
	t.Run("page two", func(t *testing.T) {
		var mu sync.Mutex
		var paths []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			paths = append(paths, r.URL.Path)
			mu.Unlock()
			writeTestHTML(w, listTestPage(feedTestBlogTitle+feedTestIndex(
				listTestCardOne,
				`<div class="page-nav">`+feedTestHomeInfoTwo+feedTestHomeNavTwo+`</div>`,
			)))
		}))
		defer srv.Close()
		got, err := newTestClient(t, srv, "").Feed(context.Background(), srv.URL+"/", 2)
		want := ArticleList{
			Title: "样例站点 - 仅用于测试",
			Items: []ArticleSummary{
				{Title: "First headline", URL: srv.URL + "/archives/277594/", CoverURL: "https://pic.example.invalid/one.jpeg", PublishedAt: "2026-10-03T17:37:24+00:00"},
			},
			Pagination: Pagination{Current: 2, Total: 3077, NextURL: srv.URL + "/page/3/", PreviousURL: srv.URL + "/"},
		}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("page two = %+v, %v; want %+v", got, err, want)
		}
		mu.Lock()
		defer mu.Unlock()
		if !reflect.DeepEqual(paths, []string{"/page/2/"}) {
			t.Fatalf("requests = %v", paths)
		}
	})
	t.Run("promo only", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeTestHTML(w, listTestPage(feedTestBlogTitle+feedTestIndex(
				feedTestPromoCard,
				`<div class="page-nav">`+feedTestHomeInfo+feedTestHomeNav+`</div>`,
			)))
		}))
		defer srv.Close()
		got, err := newTestClient(t, srv, "").Feed(context.Background(), srv.URL+"/", 1)
		if err != nil || got.Title != "样例站点 - 仅用于测试" || len(got.Items) != 0 || got.Pagination.Total != 3077 {
			t.Fatalf("promo only = %+v, %v", got, err)
		}
	})
}

func TestFeedCategoryMatchesCategoryList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTestHTML(w, listTestPage(listTestArchive(listTestBreadcrumb, listTestCardOne, listTestFirstInfo, listTestFirstNav)))
	}))
	defer srv.Close()
	c := newTestClient(t, srv, "")
	ctx := context.Background()
	viaFeed, err := c.Feed(ctx, srv.URL+"/category/wpcz/", 1)
	if err != nil {
		t.Fatalf("feed category = %+v, %v", viaFeed, err)
	}
	viaCategory, err := c.CategoryList(ctx, srv.URL+"/category/wpcz/", 1)
	if err != nil || !reflect.DeepEqual(viaFeed, viaCategory) {
		t.Fatalf("feed = %+v; category list = %+v, %v", viaFeed, viaCategory, err)
	}
	if viaFeed.Title != "今日吃瓜" || len(viaFeed.Items) != 1 || viaFeed.Pagination.Current != 1 {
		t.Fatalf("unexpected category feed: %+v", viaFeed)
	}
}

func TestFeedRejections(t *testing.T) {
	cases := []struct {
		name   string
		page   int
		path   string
		status int
		body   string
		want   string
	}{
		{name: "missing container", page: 1, body: `<html><body><div class="post-card"></div></body></html>`, want: "expected one feed container"},
		{name: "duplicate containers", page: 1, body: listTestPage(feedTestIndex(listTestCardOne) + listTestArchive(listTestBreadcrumb, listTestCardOne)), want: "expected one feed container, found 2"},
		{name: "challenge page", page: 1, body: `<html><head><title>Just a moment</title></head><body></body></html>`, want: "challenge page"},
		{name: "server error", page: 1, status: 500, body: listTestPage(feedTestIndex()), want: "HTTP 500"},
		{name: "page mismatch", page: 2, body: listTestPage(feedTestBlogTitle + feedTestIndex(listTestCardOne, feedTestHomeInfo, feedTestHomeNav)), want: "requested page 2, got page 1"},
		{name: "blank headline without badge", page: 1, body: listTestPage(feedTestIndex(`<article itemscope itemtype="http://schema.org/BlogPosting"><meta itemprop="url mainEntityOfPage" content="/archives/1/" /><h2 itemprop="headline">  </h2></article>`)), want: "empty card headline"},
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
			path := tc.path
			if path == "" {
				path = "/"
			}
			list, err := newTestClient(t, srv, "").Feed(context.Background(), srv.URL+path, tc.page)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !reflect.DeepEqual(list, ArticleList{}) {
				t.Fatalf("outcome = %+v, %v; want %q", list, err, tc.want)
			}
		})
	}
}
