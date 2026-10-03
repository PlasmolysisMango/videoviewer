package aacg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const categoriesSidebarHTML = `<nav id="site-navigation" class="sidebar" role="navigation"><div id="nav">
<a class="logo" href="/"><img src="/logo.png" alt="51吃瓜网"></a>
<ul id="menu-menu-1" class="menu navbar-nav">
<li class="menu-item"><a href="/">首页</a></li>
<li class="menu-item"><a href="/category/wpcz/" target="_self">今日吃瓜</a></li>
</ul></div></nav>`

const categoriesNavbarHTML = `<nav id="navbar" class="navbar"><div class="container-fluid">
<a class="navbar-brand" href="/"><img src="/logo.png" alt="51吃瓜网"></a>
<div class="collapse navbar-collapse" id="navbarCollapse"><ul class="navbar-nav mr-auto">
<li class="nav-item dropdown"><a class="nav-link dropdown-toggle" href="/"><div>首页</div></a></li>
<li class="nav-item dropdown"><a class="nav-link dropdown-toggle" href="/category/wpcz/" target="_self"><div>今日吃瓜</div></a></li>
<li class="nav-item dropdown">
<button class="nav-link dropdown-toggle seo-nav-dropdown-toggle" data-url="" id="more-menu-dropdown-9" role="button" data-toggle="dropdown" aria-expanded="false"><div>吃瓜热门</div></button>
<ul class="dropdown-menu" aria-labelledby="more-menu-dropdown-9">
<li class="dropdown-item"><a href="/category/xsxy/" title="学生校园" target="_self" >学生校园</a></li>
<li class="dropdown-item"><a href="/category/whhl/?from=menu#top" title="网红黑料" target="_self" > 网红黑料 </a></li>
<li class="dropdown-item"><a href="/category/xsxy/" title="学生校园" target="_self" >学生校园</a></li>
</ul></li>
<li class="nav-item dropdown">
<button class="nav-link dropdown-toggle seo-nav-dropdown-toggle" data-url="" id="more-menu-dropdown-36" role="button" data-toggle="dropdown" aria-expanded="false"><div>官方信息</div></button>
<ul class="dropdown-menu" aria-labelledby="more-menu-dropdown-36">
<li class="dropdown-item"><a href="/archives.html" title="往期内容" target="_self" >往期内容</a></li>
<li class="dropdown-item"><a href="https://06f.example.invalid/c/Csn" title="51成人导航" target="_blank" rel="nofollow noopener">51成人导航</a></li>
</ul></li>
</ul></div></div></nav>`

func TestCategories(t *testing.T) {
	body := `<html><head><title>51吃瓜网 - 测试首页</title></head><body>` + categoriesSidebarHTML + categoriesNavbarHTML + `</body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeTestHTML(w, body) }))
	defer srv.Close()
	got, err := newTestClient(t, srv, "").Categories(context.Background(), Target{URL: srv.URL})
	want := []Category{
		{Name: "今日吃瓜", URL: srv.URL + "/category/wpcz/"},
		{Name: "学生校园", URL: srv.URL + "/category/xsxy/"},
		{Name: "网红黑料", URL: srv.URL + "/category/whhl/?from=menu"},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("categories = %+v, %v; want %+v", got, err, want)
	}
}

func TestCategoriesFallbackAndRejections(t *testing.T) {
	external := `<a href="https://other.invalid/category/x/">外站分类</a>`
	cases := []struct {
		name      string
		body      string
		wantNames []string
		wantPaths []string
		reason    string
	}{
		{
			name:      "sidebar only",
			body:      `<html><body>` + categoriesSidebarHTML + `</body></html>`,
			wantNames: []string{"今日吃瓜"},
			wantPaths: []string{"/category/wpcz/"},
		},
		{
			name:      "external category link ignored",
			body:      `<html><body><nav id="navbar">` + external + `<ul><li><a href="/category/local/">本地</a></li></ul></nav></body></html>`,
			wantNames: []string{"本地"},
			wantPaths: []string{"/category/local/"},
		},
		{
			name:      "content links outside navigation ignored",
			body:      `<html><body><div class="breadcrumbs"><a href="/category/bread/">面包屑</a></div><nav id="navbar"><ul><li><a href="/category/real/">真分类</a></li></ul></nav></body></html>`,
			wantNames: []string{"真分类"},
			wantPaths: []string{"/category/real/"},
		},
		{
			name:   "nav without categories",
			body:   `<html><body><nav id="navbar"><ul><li><a href="/archives.html">往期内容</a></li>` + external + `</ul></nav></body></html>`,
			reason: "no navigation categories",
		},
		{
			name:   "no navigation at all",
			body:   healthyHomepage,
			reason: "no navigation categories",
		},
		{
			name:   "challenge page",
			body:   healthyHomepage + `<div>verify you are human</div>`,
			reason: "challenge",
		},
		{
			name:   "empty category name",
			body:   `<html><body><nav id="navbar"><ul><li><a href="/category/blank/"></a></li></ul></nav></body></html>`,
			reason: "empty category name",
		},
		{
			name:   "malformed category link",
			body:   `<html><body><nav id="navbar"><ul><li><a href="/category/a\b/">坏链</a></li></ul></nav></body></html>`,
			reason: "invalid URL",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeTestHTML(w, tc.body) }))
			defer srv.Close()
			got, err := newTestClient(t, srv, "").Categories(context.Background(), Target{URL: srv.URL})
			if tc.reason != "" {
				if err == nil || !strings.Contains(err.Error(), tc.reason) {
					t.Fatalf("categories = %+v, %v; want error containing %q", got, err, tc.reason)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.wantNames) {
				t.Fatalf("categories = %+v; want %d entries", got, len(tc.wantNames))
			}
			for i := range got {
				if got[i].Name != tc.wantNames[i] || !strings.HasSuffix(got[i].URL, tc.wantPaths[i]) {
					t.Fatalf("categories[%d] = %+v; want name=%q with path suffix %q", i, got[i], tc.wantNames[i], tc.wantPaths[i])
				}
			}
		})
	}
}
