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
	searchTestHeading = `<h1 class="blog-title">包含关键字 51 的文章</h1>`
	searchTestCrumbs  = `<div class="archive-nav-breadcrumb-wrap"><div class="nav-breadcrumb-wrap"></div></div>`
	searchTestInfo    = `<span class="page-info"> 1/2985 </span>`
	searchTestInfoTwo = `<span class="page-info"> 2/2985 </span>`
	searchTestNav     = `<ul class="page-navigator"><li class="active"><a href="/search/51/">1</a></li><li><span></span></li><li><a href="/search/51/2985/">2985</a></li><li class="next"><a href="/search/51/2/">下一页</a></li></ul>`
	searchTestNavTwo  = `<ul class="page-navigator"><li class="prev"><a href="/search/51/">上一页</a></li><li><a href="/search/51/">1</a></li><li class="active"><a href="/search/51/2/">2</a></li><li><span></span></li><li><a href="/search/51/2985/">2985</a></li><li class="next"><a href="/search/51/3/">下一页</a></li></ul>`
)

func searchTestArchive(parts ...string) string {
	return `<div id="archive" role="main">` + strings.Join(parts, "") + `</div>`
}

func searchTestPage(parts ...string) string {
	return `<html><body>` + strings.Join(parts, "") + `</body></html>`
}

func TestSearchPageURL(t *testing.T) {
	for _, tc := range []struct {
		name    string
		siteURL string
		query   string
		page    int
		want    string
	}{
		{"page one", "https://site.invalid/", "51", 1, "https://site.invalid/search/51/"},
		{"numbered page", "https://site.invalid/", "51", 3, "https://site.invalid/search/51/3/"},
		{"page site URL", "https://site.invalid/page/2/", "51", 1, "https://site.invalid/search/51/"},
		{"page site URL numbered", "https://site.invalid/page/2/", "51", 2, "https://site.invalid/search/51/2/"},
		{"chinese query", "https://site.invalid/", "萌瓜", 1, "https://site.invalid/search/%E8%90%8C%E7%93%9C/"},
		{"reserved characters", "https://site.invalid/", "a b?c#d", 1, "https://site.invalid/search/a%20b%3Fc%23d/"},
		{"percent sign", "https://site.invalid/", "100%", 1, "https://site.invalid/search/100%25/"},
		{"slash", "https://site.invalid/", "a/b", 1, "https://site.invalid/search/a%2Fb/"},
		{"folded whitespace", "https://site.invalid/", "  51   瓜  ", 1, "https://site.invalid/search/51%20%E7%93%9C/"},
		{"keeps port", "https://site.invalid:8443/", "51", 2, "https://site.invalid:8443/search/51/2/"},
		{"drops site query and fragment", "https://site.invalid/?from=menu#top", "51", 2, "https://site.invalid/search/51/2/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := searchPageURL(tc.siteURL, tc.query, tc.page)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name    string
		siteURL string
		query   string
		page    int
		want    string
	}{
		{"zero page", "https://site.invalid/", "51", 0, "invalid page"},
		{"empty query", "https://site.invalid/", "", 1, "empty query"},
		{"blank query", "https://site.invalid/", "  \t ", 1, "empty query"},
		{"dot query", "https://site.invalid/", ".", 1, "invalid query"},
		{"dot dot query", "https://site.invalid/", "..", 1, "invalid query"},
		{"category URL", "https://site.invalid/category/wpcz/", "51", 1, "not a site URL"},
		{"search URL", "https://site.invalid/search/51/", "51", 1, "not a site URL"},
		{"archive URL", "https://site.invalid/archives/1/", "51", 1, "not a site URL"},
		{"page URL without number", "https://site.invalid/page/", "51", 1, "not a site URL"},
		{"not http", "file:///", "51", 1, "invalid site URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := searchPageURL(tc.siteURL, tc.query, tc.page)
			if err == nil || !strings.Contains(err.Error(), tc.want) || got != "" {
				t.Fatalf("got %q, %v; want error %q", got, err, tc.want)
			}
		})
	}
}

func TestSearch(t *testing.T) {
	t.Run("page one", func(t *testing.T) {
		var mu sync.Mutex
		var paths []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			paths = append(paths, r.URL.Path)
			mu.Unlock()
			writeTestHTML(w, searchTestPage(searchTestHeading, searchTestArchive(
				searchTestCrumbs,
				listTestCardOne,
				listTestCardTwo,
				`<div class="page-nav">`+searchTestInfo+searchTestNav+`</div>`,
			)))
		}))
		defer srv.Close()
		got, err := newTestClient(t, srv, "").Search(context.Background(), srv.URL+"/", "51", 1)
		want := ArticleList{
			Title: "包含关键字 51 的文章",
			Items: []ArticleSummary{
				{Title: "First headline", URL: srv.URL + "/archives/277594/", CoverURL: "https://pic.example.invalid/one.jpeg", PublishedAt: "2026-10-03T17:37:24+00:00"},
				{Title: "Second headline", URL: srv.URL + "/archives/277585/", PublishedAt: "2026-10-03T17:30:00+00:00"},
			},
			Pagination: Pagination{Current: 1, Total: 2985, NextURL: srv.URL + "/search/51/2/"},
		}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("page one = %+v, %v; want %+v", got, err, want)
		}
		mu.Lock()
		defer mu.Unlock()
		if !reflect.DeepEqual(paths, []string{"/search/51/"}) {
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
			writeTestHTML(w, searchTestPage(searchTestHeading, searchTestArchive(
				searchTestCrumbs,
				listTestCardOne,
				`<div class="page-nav">`+searchTestInfoTwo+searchTestNavTwo+`</div>`,
			)))
		}))
		defer srv.Close()
		got, err := newTestClient(t, srv, "").Search(context.Background(), srv.URL+"/", "51", 2)
		want := ArticleList{
			Title: "包含关键字 51 的文章",
			Items: []ArticleSummary{
				{Title: "First headline", URL: srv.URL + "/archives/277594/", CoverURL: "https://pic.example.invalid/one.jpeg", PublishedAt: "2026-10-03T17:37:24+00:00"},
			},
			Pagination: Pagination{Current: 2, Total: 2985, NextURL: srv.URL + "/search/51/3/", PreviousURL: srv.URL + "/search/51/"},
		}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("page two = %+v, %v; want %+v", got, err, want)
		}
		mu.Lock()
		defer mu.Unlock()
		if !reflect.DeepEqual(paths, []string{"/search/51/2/"}) {
			t.Fatalf("requests = %v", paths)
		}
	})
	t.Run("escaped query", func(t *testing.T) {
		var mu sync.Mutex
		var uris []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			uris = append(uris, r.RequestURI)
			mu.Unlock()
			writeTestHTML(w, searchTestPage(searchTestHeading, searchTestArchive(
				searchTestCrumbs,
				listTestCardOne,
				`<div class="page-nav">`+searchTestInfo+searchTestNav+`</div>`,
			)))
		}))
		defer srv.Close()
		got, err := newTestClient(t, srv, "").Search(context.Background(), srv.URL+"/", "萌瓜", 1)
		if err != nil || got.Title != "包含关键字 51 的文章" {
			t.Fatalf("escaped query = %+v, %v", got, err)
		}
		mu.Lock()
		defer mu.Unlock()
		if !reflect.DeepEqual(uris, []string{"/search/%E8%90%8C%E7%93%9C/"}) {
			t.Fatalf("request URIs = %v", uris)
		}
	})
	t.Run("empty results", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeTestHTML(w, searchTestPage(searchTestHeading, searchTestArchive(
				searchTestCrumbs,
				`<aside class="xqbj-search-none" aria-label="暂无内容"></aside>`,
			)))
		}))
		defer srv.Close()
		got, err := newTestClient(t, srv, "").Search(context.Background(), srv.URL+"/", "51", 1)
		want := ArticleList{Title: "包含关键字 51 的文章", Items: []ArticleSummary{}, Pagination: Pagination{Current: 1}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("empty results = %+v, %v; want %+v", got, err, want)
		}
	})
	t.Run("missing heading", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeTestHTML(w, searchTestPage(searchTestArchive(searchTestCrumbs, listTestCardOne)))
		}))
		defer srv.Close()
		got, err := newTestClient(t, srv, "").Search(context.Background(), srv.URL+"/", "51", 1)
		if err != nil || got.Title != "" || len(got.Items) != 1 || got.Pagination.Current != 1 {
			t.Fatalf("missing heading = %+v, %v", got, err)
		}
	})
}

func TestSearchRejections(t *testing.T) {
	cases := []struct {
		name   string
		page   int
		status int
		body   string
		want   string
	}{
		{name: "missing container", page: 1, body: `<html><body><h1 class="blog-title">包含关键字 51 的文章</h1></body></html>`, want: "expected one search archive"},
		{name: "duplicate containers", page: 1, body: listTestPage(listTestArchive(listTestBreadcrumb) + listTestArchive(listTestBreadcrumb)), want: "expected one search archive, found 2"},
		{name: "challenge page", page: 1, body: `<html><head><title>Just a moment</title></head><body></body></html>`, want: "challenge page"},
		{name: "server error", page: 1, status: 500, body: searchTestPage(searchTestHeading, searchTestArchive()), want: "HTTP 500"},
		{name: "page mismatch", page: 2, body: searchTestPage(searchTestHeading, searchTestArchive(searchTestCrumbs, listTestCardOne, searchTestInfo, searchTestNav)), want: "requested page 2, got page 1"},
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
			list, err := newTestClient(t, srv, "").Search(context.Background(), srv.URL+"/", "51", tc.page)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !reflect.DeepEqual(list, ArticleList{}) {
				t.Fatalf("outcome = %+v, %v; want %q", list, err, tc.want)
			}
		})
	}
}
