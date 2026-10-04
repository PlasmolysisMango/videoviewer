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
	listTestBreadcrumb      = `<div class="nav-breadcrumb-wrap"><a class="action-butn" href="/">首页</a><a class="action-butn" href="/category/wpcz/">今日吃瓜</a><span class="name">第2页</span></div>`
	listTestFirstBreadcrumb = `<div class="nav-breadcrumb-wrap"><a class="action-butn" href="/">首页</a><span class="name">今日吃瓜</span></div>`
	listTestInfo            = `<span class="page-info"> 2/3070 </span>`
	listTestFirstInfo       = `<span class="page-info"> 1/3070 </span>`
	listTestNav             = `<ul class="page-navigator"><li class="prev"><a href="/category/wpcz/">上一页</a></li><li><a href="/category/wpcz/">1</a></li><li class="active"><a href="/category/wpcz/2/">2</a></li><li><span></span></li><li><a href="/category/wpcz/3070/">3070</a></li><li class="next"><a href="/category/wpcz/3/">下一页</a></li></ul>`
	listTestFirstNav        = `<ul class="page-navigator"><li class="active"><a href="/category/wpcz/">1</a></li><li><span></span></li><li><a href="/category/wpcz/3070/">3070</a></li><li class="next"><a href="/category/wpcz/2/">下一页</a></li></ul>`
	listTestCardOne         = `<article itemscope itemtype="http://schema.org/BlogPosting"><meta itemprop="url mainEntityOfPage" content="/archives/277594/" /><a href="/archives/277594/"><div class="post-card" id="post-card-277594"><script type="text/javascript">loadBannerDirect('https://pic.example.invalid/one.jpeg', '', document.querySelector('#post-card-277594'));</script><h2 class="post-card-title" itemprop="headline">First <em>headline</em></h2><span itemprop="datePublished" content="2026-10-03T17:37:24+00:00">2026 年 10 月 03 日</span></div></a></article>`
	listTestCardTwo         = `<article itemscope itemtype="http://schema.org/BlogPosting"><meta itemprop="url mainEntityOfPage" content="/archives/277585/" /><a href="/archives/277585/"><div class="post-card" id="post-card-277585"><script type="text/javascript">loadBannerDirect('', '', document.querySelector('#post-card-277585'));</script><h2 class="post-card-title" itemprop="headline">Second headline</h2><span itemprop="datePublished" content="2026-10-03T17:30:00+00:00">2026 年 10 月 03 日</span></div></a></article>`
	listTestShellCard       = `<article itemscope itemtype="http://schema.org/BlogPosting"><meta itemprop="url mainEntityOfPage" content="/archives/277664/" /><a href="/archives/277664/"><div class="post-card" id="post-card-277664"><script type="text/javascript">loadBannerDirect('https://pic.example.invalid/shell.jpeg', '', document.querySelector('#post-card-277664'));</script><div class="post-card-mask "><div class="post-card-container"><div class="post-card-info"></div></div></div></div></a></article>`
)

func listTestPage(archive string) string {
	return `<html><body>` + archive + `</body></html>`
}

func listTestArchive(parts ...string) string {
	return `<div id="archive" role="main">` + strings.Join(parts, "") + `</div>`
}

func TestCategoryList(t *testing.T) {
	t.Run("page two", func(t *testing.T) {
		var mu sync.Mutex
		var paths []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			paths = append(paths, r.URL.Path)
			mu.Unlock()
			writeTestHTML(w, listTestPage(listTestArchive(
				listTestBreadcrumb,
				listTestCardOne,
				`<article class="ad-item"><a href="https://ads.invalid/">广告</a></article>`,
				`<article itemscope itemtype="http://schema.org/BlogPosting"><meta itemprop="url mainEntityOfPage" content="/archives/277594/" /><a href="/archives/277594/"><h2 itemprop="headline">First headline duplicate</h2></a></article>`,
				listTestCardTwo,
				`<div class="page-nav">`+listTestInfo+listTestNav+`</div>`,
			)))
		}))
		defer srv.Close()
		got, err := newTestClient(t, srv, "").CategoryList(context.Background(), srv.URL+"/category/wpcz/", 2)
		want := ArticleList{
			Title: "今日吃瓜",
			Items: []ArticleSummary{
				{Title: "First headline", URL: srv.URL + "/archives/277594/", CoverURL: "https://pic.example.invalid/one.jpeg", PublishedAt: "2026-10-03T17:37:24+00:00"},
				{Title: "Second headline", URL: srv.URL + "/archives/277585/", PublishedAt: "2026-10-03T17:30:00+00:00"},
			},
			Pagination: Pagination{Current: 2, Total: 3070, NextURL: srv.URL + "/category/wpcz/3/", PreviousURL: srv.URL + "/category/wpcz/"},
		}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("page two = %+v, %v; want %+v", got, err, want)
		}
		mu.Lock()
		defer mu.Unlock()
		if !reflect.DeepEqual(paths, []string{"/category/wpcz/2/"}) {
			t.Fatalf("requests = %v", paths)
		}
	})
	t.Run("page one", func(t *testing.T) {
		var mu sync.Mutex
		var paths []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			paths = append(paths, r.URL.Path)
			mu.Unlock()
			writeTestHTML(w, listTestPage(listTestArchive(listTestFirstBreadcrumb, listTestCardOne, listTestFirstInfo, listTestFirstNav)))
		}))
		defer srv.Close()
		got, err := newTestClient(t, srv, "").CategoryList(context.Background(), srv.URL+"/category/wpcz/", 1)
		want := ArticleList{
			Title: "今日吃瓜",
			Items: []ArticleSummary{
				{Title: "First headline", URL: srv.URL + "/archives/277594/", CoverURL: "https://pic.example.invalid/one.jpeg", PublishedAt: "2026-10-03T17:37:24+00:00"},
			},
			Pagination: Pagination{Current: 1, Total: 3070, NextURL: srv.URL + "/category/wpcz/2/"},
		}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("page one = %+v, %v; want %+v", got, err, want)
		}
		mu.Lock()
		defer mu.Unlock()
		if !reflect.DeepEqual(paths, []string{"/category/wpcz/"}) {
			t.Fatalf("requests = %v", paths)
		}
	})
	t.Run("single page", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeTestHTML(w, listTestPage(listTestArchive(listTestFirstBreadcrumb, listTestCardTwo)))
		}))
		defer srv.Close()
		got, err := newTestClient(t, srv, "").CategoryList(context.Background(), srv.URL+"/category/wpcz/", 1)
		if err != nil || got.Title != "今日吃瓜" || len(got.Items) != 1 || got.Items[0].Title != "Second headline" ||
			got.Pagination != (Pagination{Current: 1}) {
			t.Fatalf("single page = %+v, %v", got, err)
		}
	})
	t.Run("skips titleless shell card", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeTestHTML(w, listTestPage(listTestArchive(listTestFirstBreadcrumb, listTestShellCard, listTestCardTwo, listTestFirstInfo, listTestFirstNav)))
		}))
		defer srv.Close()
		got, err := newTestClient(t, srv, "").CategoryList(context.Background(), srv.URL+"/category/wpcz/", 1)
		want := ArticleList{
			Title: "今日吃瓜",
			Items: []ArticleSummary{
				{Title: "Second headline", URL: srv.URL + "/archives/277585/", PublishedAt: "2026-10-03T17:30:00+00:00"},
			},
			Pagination: Pagination{Current: 1, Total: 3070, NextURL: srv.URL + "/category/wpcz/2/"},
		}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("shell card = %+v, %v; want %+v", got, err, want)
		}
	})
}

func TestCategoryListRejections(t *testing.T) {
	cases := []struct {
		name   string
		page   int
		path   string
		status int
		body   string
		want   string
	}{
		{name: "invalid category URL", page: 1, path: "/archives/277594/", body: listTestPage(listTestArchive()), want: "not a category URL"},
		{name: "zero page", page: 0, body: listTestPage(listTestArchive()), want: "invalid page"},
		{name: "challenge page", page: 1, body: `<html><head><title>Just a moment</title></head><body></body></html>`, want: "challenge page"},
		{name: "server error", page: 1, status: 500, body: listTestPage(listTestArchive()), want: "HTTP 500"},
		{name: "missing archive", page: 1, body: `<html><body><div class="nav-breadcrumb-wrap"></div></body></html>`, want: "expected one feed container"},
		{name: "duplicate archive", page: 1, body: listTestPage(listTestArchive(listTestBreadcrumb, listTestCardOne) + listTestArchive(listTestBreadcrumb, listTestCardOne)), want: "expected one feed container, found 2"},
		{name: "missing breadcrumb", page: 1, body: listTestPage(listTestArchive(listTestCardOne)), want: "expected one breadcrumb"},
		{name: "card without link", page: 1, body: listTestPage(listTestArchive(listTestBreadcrumb, `<article itemscope itemtype="http://schema.org/BlogPosting"><h2 itemprop="headline">T</h2></article>`)), want: "expected one card link"},
		{name: "empty headline", page: 1, body: listTestPage(listTestArchive(listTestBreadcrumb, `<article itemscope itemtype="http://schema.org/BlogPosting"><meta itemprop="url mainEntityOfPage" content="/archives/1/" /><h2 itemprop="headline">  </h2></article>`)), want: "empty card headline"},
		{name: "ambiguous card date", page: 1, body: listTestPage(listTestArchive(listTestBreadcrumb, `<article itemscope itemtype="http://schema.org/BlogPosting"><meta itemprop="url mainEntityOfPage" content="/archives/1/" /><h2 itemprop="headline">T</h2><span itemprop="datePublished" content="a"></span><span itemprop="datePublished" content="b"></span></article>`)), want: "ambiguous card date"},
		{name: "unsafe cover URL", page: 1, body: listTestPage(listTestArchive(listTestBreadcrumb, `<article itemscope itemtype="http://schema.org/BlogPosting"><meta itemprop="url mainEntityOfPage" content="/archives/1/" /><h2 itemprop="headline">T</h2><script>loadBannerDirect('javascript:alert(1)', '', 0);</script></article>`)), want: "invalid link"},
		{name: "invalid page info", page: 2, body: listTestPage(listTestArchive(listTestBreadcrumb, listTestCardOne, `<span class="page-info">many/4</span>`, listTestNav)), want: "invalid page info"},
		{name: "unreadable active page", page: 1, body: listTestPage(listTestArchive(listTestBreadcrumb, listTestCardOne, `<ul class="page-navigator"><li class="active"><a>?</a></li></ul>`)), want: "invalid current page"},
		{name: "page mismatch", page: 2, body: listTestPage(listTestArchive(listTestBreadcrumb, listTestCardOne, listTestFirstInfo, listTestFirstNav)), want: "requested page 2, got page 1"},
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
				path = "/category/wpcz/"
			}
			list, err := newTestClient(t, srv, "").CategoryList(context.Background(), srv.URL+path, tc.page)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !reflect.DeepEqual(list, ArticleList{}) {
				t.Fatalf("outcome = %+v, %v; want %q", list, err, tc.want)
			}
		})
	}
}

func TestCategoryPageURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		page int
		want string
	}{
		{"page one", "https://site.invalid/category/wpcz/", 1, "https://site.invalid/category/wpcz/"},
		{"numbered page", "https://site.invalid/category/wpcz/", 3, "https://site.invalid/category/wpcz/3/"},
		{"missing trailing slash", "https://site.invalid/category/wpcz", 2, "https://site.invalid/category/wpcz/2/"},
		{"replaces page suffix", "https://site.invalid/category/wpcz/9/", 2, "https://site.invalid/category/wpcz/2/"},
		{"drops page suffix for one", "https://site.invalid/category/wpcz/9/", 1, "https://site.invalid/category/wpcz/"},
		{"drops query and fragment", "https://site.invalid/category/whhl/?from=menu#top", 1, "https://site.invalid/category/whhl/"},
		{"numeric slug", "https://site.invalid/category/123/", 2, "https://site.invalid/category/123/2/"},
		{"keeps port", "https://site.invalid:8443/category/wpcz/", 4, "https://site.invalid:8443/category/wpcz/4/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := categoryPageURL(tc.raw, tc.page)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name string
		raw  string
		page int
	}{
		{"zero page", "https://site.invalid/category/wpcz/", 0},
		{"root", "https://site.invalid/", 1},
		{"archive", "https://site.invalid/archives/277594/", 1},
		{"category root", "https://site.invalid/category/", 1},
		{"non numeric page suffix", "https://site.invalid/category/wpcz/latest/", 1},
		{"too deep", "https://site.invalid/category/a/b/2/", 1},
		{"dot slug", "https://site.invalid/category/../", 1},
		{"not http", "file:///category/wpcz/", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := categoryPageURL(tc.raw, tc.page); err == nil {
				t.Fatalf("unexpected URL %q", got)
			}
		})
	}
}
