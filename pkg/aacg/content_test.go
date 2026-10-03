package aacg

import (
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const contentTestBase = "https://pages.invalid/dir/home#top"
const contentTestHomeHTML = `<nav data-role="categories"></nav><section data-role="recommendations"></section><form role="search"><input type="search" name="q"></form>`
const contentTestCardHTML = `<article data-role="card"><h2 data-role="title"><a href="/notes">Forest sensors</a></h2></article>`

func contentTestList(body string) string {
	return `<main data-role="article-list"><h1 data-role="title">Field notes</h1>` + body + `</main>`
}

func contentTestArticle(body string) string {
	return `<article data-role="article"><h1 data-role="title">Forest sensors</h1><div data-role="content">` + body + `</div></article>`
}

func contentTestParse(kind string, r io.Reader, base string) error {
	switch kind {
	case "home":
		_, err := ParseHome(r, base)
		return err
	case "list":
		_, err := ParseList(r, base)
		return err
	case "article":
		_, err := ParseArticle(r, base)
		return err
	default:
		return fmt.Errorf("unknown test parser %q", kind)
	}
}

func TestContentHome(t *testing.T) {
	body := `<base href="https://ignored.invalid/">
<nav data-role="categories"><a href="../science#one"> Science &amp; nature </a><a href="https://PAGES.invalid/science#two">Duplicate</a><a href="//pages.invalid/technology">Technology</a></nav>
<section data-role="recommendations">
<article data-role="card"><h2 data-role="title"><a href="../solar#read"> Solar <em>sensors</em><span hidden>decoy</span> </a></h2><p data-role="summary"> Small &amp; useful </p><img data-role="cover" data-src="//media.invalid/solar.jpg#image"><time datetime="2026-06-02T09:30:00Z">Today</time></article>
<article data-role="card"><a data-role="title" href="//articles.invalid/trees#read">Tree rings</a></article>
</section><form role="search" method="GET" action="../search?lang=zh#form"><input type="search" name="query"><input type="search" name="disabled" disabled><input type="hidden" name="tracking" value="ignored"></form>`
	got, err := ParseHome(strings.NewReader(body), contentTestBase)
	want := HomePage{
		Categories: []Category{{Name: "Science & nature", URL: "https://pages.invalid/science"}, {Name: "Technology", URL: "https://pages.invalid/technology"}},
		Recommendations: []ArticleSummary{
			{Title: "Solar sensors", URL: "https://pages.invalid/solar", Summary: "Small & useful", CoverURL: "https://media.invalid/solar.jpg", PublishedAt: "2026-06-02T09:30:00Z"},
			{Title: "Tree rings", URL: "https://articles.invalid/trees"},
		},
		Search: SearchForm{Action: "https://pages.invalid/search?lang=zh", QueryName: "query"},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("home = %+v, %v; want %+v", got, err, want)
	}
	search, err := BuildSearchURL(got.Search, "forest")
	if err != nil || search != "https://pages.invalid/search?lang=zh&query=forest" {
		t.Fatalf("search serialized unexpected fields: %q, %v", search, err)
	}
}

func TestContentList(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		count      int
		pagination Pagination
	}{
		{"marked empty", "", 0, Pagination{Current: 1}},
		{"optional fields absent", contentTestCardHTML, 1, Pagination{Current: 1}},
		{"descendant card", `<section>` + contentTestCardHTML + `</section>`, 1, Pagination{Current: 1}},
		{"pagination", contentTestCardHTML + `<nav data-role="pagination"><span aria-current="page">2</span><a rel="nofollow prev" href="?page=1#prev">Previous</a><a rel="next nofollow" href="?page=3#next">Next</a></nav>`, 1, Pagination{Current: 2, PreviousURL: "https://pages.invalid/dir/home?page=1", NextURL: "https://pages.invalid/dir/home?page=3"}},
		{"single page", `<nav data-role="pagination"><span aria-current="page">1</span></nav>`, 0, Pagination{Current: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseList(strings.NewReader(contentTestList(tc.body)), contentTestBase)
			if err != nil || got.Title != "Field notes" || len(got.Items) != tc.count || got.Pagination != tc.pagination {
				t.Fatalf("list = %+v, %v", got, err)
			}
			if tc.count > 0 && got.Items[0] != (ArticleSummary{Title: "Forest sensors", URL: "https://pages.invalid/notes"}) {
				t.Fatalf("card = %+v", got.Items[0])
			}
		})
	}
}

func TestContentArticle(t *testing.T) {
	body := `<base href="https://ignored.invalid/"><article data-role="article">
<h1 data-role="title"> Forest <em>sensors</em> </h1><p data-role="summary">Rain &amp; wind</p><img data-role="cover" src="../cover.jpg#cover"><time datetime="2026-06-02">Not the date</time>
<video src="https://media.invalid/outside.mp4"></video><div data-role="content">
<h2>Cloud <em>notes</em></h2><p> Rain   &amp; <strong>wind <span>patterns</span></strong>. </p><ul><li>First <em>reading</em></li><li>Second&nbsp;reading</li></ul><p>Line one<br>Line <span>two</span></p>
<video src="//media.invalid/rain.mp4#start" type="video/mp4" poster="../poster.jpg#image"><source src="//media.invalid/rain.webm" type="video/webm">Video fallback</video>
<video src="" poster="//media.invalid/wind.jpg"><source src="https://MEDIA.invalid/rain.mp4#duplicate" type="duplicate"><source src="//media.invalid/wind.webm" type="video/webm"><div><source src="https://media.invalid/nested.mp4"></div>More fallback</video>
<source src="https://media.invalid/orphan.mp4"><script>window.player={url:"https://media.invalid/config.m3u8"}</script><iframe src="https://media.invalid/frame"></iframe>
</div></article>`
	got, err := ParseArticle(strings.NewReader(body), "https://PAGES.invalid/dir/notes?view=full#top")
	want := ArticleDetail{
		ArticleSummary: ArticleSummary{Title: "Forest sensors", URL: "https://pages.invalid/dir/notes?view=full", Summary: "Rain & wind", CoverURL: "https://pages.invalid/cover.jpg", PublishedAt: "2026-06-02"},
		Content:        "Cloud notes\n\nRain & wind patterns.\n\nFirst reading\nSecond reading\n\nLine one\nLine two",
		Videos: []VideoLink{
			{URL: "https://media.invalid/rain.mp4", Type: "video/mp4", PosterURL: "https://pages.invalid/poster.jpg"},
			{URL: "https://media.invalid/rain.webm", Type: "video/webm", PosterURL: "https://pages.invalid/poster.jpg"},
			{URL: "https://media.invalid/wind.webm", Type: "video/webm", PosterURL: "https://media.invalid/wind.jpg"},
		},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("article = %+v, %v; want %+v", got, err, want)
	}
	for _, fragment := range []string{`<p>Visible text</p>`, `<p>Visible text</p><video>Ignored fallback</video>`, `<style>.concealed { display:none }</style><p class="concealed">Visible text</p>`} {
		got, err := ParseArticle(strings.NewReader(contentTestArticle(fragment)), contentTestBase)
		if err != nil || got.ArticleSummary != (ArticleSummary{Title: "Forest sensors", URL: "https://pages.invalid/dir/home"}) || got.Content != "Visible text" || len(got.Videos) != 0 {
			t.Fatalf("optional/no-video article = %+v, %v", got, err)
		}
	}
	minimal, err := ParseArticle(strings.NewReader(contentTestArticle(`<video src="../rain.mp4#start"></video>`)), contentTestBase)
	if err != nil || !reflect.DeepEqual(minimal.Videos, []VideoLink{{URL: "https://pages.invalid/rain.mp4"}}) {
		t.Fatalf("relative video without optional metadata = %+v, %v", minimal, err)
	}
}

func TestContentTextSpacing(t *testing.T) {
	homeHTML := strings.Replace(contentTestHomeHTML, `</nav>`, `<a href="/science">Forest<br>science</a></nav>`, 1)
	home, err := ParseHome(strings.NewReader(homeHTML), contentTestBase)
	if err != nil || len(home.Categories) != 1 || home.Categories[0].Name != "Forest science" {
		t.Fatalf("category spacing = %+v, %v", home, err)
	}
	for _, titleTag := range []string{"h1", "h2", "div", "a"} {
		t.Run(titleTag, func(t *testing.T) {
			title := `<` + titleTag + ` data-role="title">Forest<br>sensors</` + titleTag + `>`
			body := `<article data-role="article">` + title + `<p data-role="summary">Rain<br>readings</p><div data-role="content"><p>One<br>two<br><br>three</p><ul><li>Four</li><li>Five</li></ul></div></article>`
			article, err := ParseArticle(strings.NewReader(body), contentTestBase)
			if err != nil || article.Title != "Forest sensors" || article.Summary != "Rain readings" || article.Content != "One\ntwo\n\nthree\n\nFour\nFive" {
				t.Fatalf("article spacing = %+v, %v", article, err)
			}
			list, err := ParseList(strings.NewReader(`<main data-role="article-list">`+title+`</main>`), contentTestBase)
			if err != nil || list.Title != "Forest sensors" {
				t.Fatalf("list title spacing = %+v, %v", list, err)
			}
		})
	}
}

func TestContentCardDeduplication(t *testing.T) {
	duplicate := strings.ReplaceAll(contentTestCardHTML, "/notes", "https://PAGES.invalid/notes#duplicate")
	duplicate = strings.ReplaceAll(duplicate, "Forest sensors", "Duplicate notes")
	other := strings.ReplaceAll(contentTestCardHTML, "/notes", "/other")
	cards := contentTestCardHTML + duplicate + other
	want := []ArticleSummary{{Title: "Forest sensors", URL: "https://pages.invalid/notes"}, {Title: "Forest sensors", URL: "https://pages.invalid/other"}}
	list, err := ParseList(strings.NewReader(contentTestList(cards)), contentTestBase)
	if err != nil || !reflect.DeepEqual(list.Items, want) {
		t.Fatalf("deduplicated list = %+v, %v", list, err)
	}
	home, err := ParseHome(strings.NewReader(strings.Replace(contentTestHomeHTML, `</section>`, cards+`</section>`, 1)), contentTestBase)
	if err != nil || !reflect.DeepEqual(home.Recommendations, want) {
		t.Fatalf("deduplicated recommendations = %+v, %v", home, err)
	}
}

func TestContentInlineStylePrecedence(t *testing.T) {
	for _, tc := range []struct {
		style  string
		hidden bool
	}{
		{"display:none; display:block", false},
		{"display:block; display:none", true},
		{"display:none!important; display:block", true},
		{"display:none; display:block!important", false},
		{"display:none!important; display:block!important", false},
		{"visibility:hidden; visibility:visible", false},
		{"visibility:visible; visibility:collapse", true},
		{"display:block; visibility:hidden", true},
	} {
		t.Run(tc.style, func(t *testing.T) {
			body := `<p>Before</p><div style="` + tc.style + `"><p>Middle</p><video src="https://media.invalid/forest.mp4"></video></div><p>After</p>`
			got, err := ParseArticle(strings.NewReader(contentTestArticle(body)), contentTestBase)
			want, count := "Before\n\nMiddle\n\nAfter", 1
			if tc.hidden {
				want, count = "Before\n\nAfter", 0
			}
			if err != nil || got.Content != want || len(got.Videos) != count {
				t.Fatalf("inline style = %+v, %v", got, err)
			}
		})
	}
}

func TestContentOptionalFieldScope(t *testing.T) {
	fields := []string{`<p data-role="summary">Canopy notes</p>`, `<img data-role="cover" data-src="../canopy.jpg">`, `<time datetime="date-as-supplied">Today</time>`}
	metadata := strings.Join(fields, "")
	nested := `<section>` + metadata + `<h3 data-role="title">Nested heading</h3></section>`
	card := strings.Replace(contentTestCardHTML, `</article>`, nested+metadata+`</article>`, 1)
	list, err := ParseList(strings.NewReader(contentTestList(card)), contentTestBase)
	want := ArticleSummary{Title: "Forest sensors", URL: "https://pages.invalid/notes", Summary: "Canopy notes", CoverURL: "https://pages.invalid/canopy.jpg", PublishedAt: "date-as-supplied"}
	if err != nil || len(list.Items) != 1 || list.Items[0] != want {
		t.Fatalf("direct card metadata = %+v, %v", list, err)
	}
	body := strings.Replace(contentTestArticle(nested), `</h1>`, `</h1>`+metadata, 1)
	article, err := ParseArticle(strings.NewReader(body), contentTestBase)
	want.URL = "https://pages.invalid/dir/home"
	if err != nil || article.ArticleSummary != want {
		t.Fatalf("direct article metadata = %+v, %v", article, err)
	}
	article, err = ParseArticle(strings.NewReader(contentTestArticle(nested)), contentTestBase)
	if err != nil || article.Summary != "" || article.CoverURL != "" || article.PublishedAt != "" {
		t.Fatalf("nested article metadata leaked = %+v, %v", article, err)
	}
	list, err = ParseList(strings.NewReader(contentTestList(strings.Replace(contentTestCardHTML, `</article>`, nested+`</article>`, 1))), contentTestBase)
	if err != nil || len(list.Items) != 1 || list.Items[0] != (ArticleSummary{Title: "Forest sensors", URL: "https://pages.invalid/notes"}) {
		t.Fatalf("nested card metadata leaked = %+v, %v", list, err)
	}
	for i, field := range fields {
		for kind, body := range map[string]string{
			"list":    contentTestList(strings.Replace(contentTestCardHTML, `</article>`, field+field+`</article>`, 1)),
			"article": strings.Replace(contentTestArticle("Text"), `</h1>`, `</h1>`+field+field, 1),
		} {
			t.Run(fmt.Sprintf("duplicate %s field %d", kind, i), func(t *testing.T) {
				if err := contentTestParse(kind, strings.NewReader(body), contentTestBase); err == nil {
					t.Fatal("ambiguous optional field accepted")
				}
			})
		}
	}
}

func TestContentHiddenSubtrees(t *testing.T) {
	decoy := `<nav data-role="categories"><a href="javascript:bad">Hidden category</a></nav><section data-role="recommendations">` + contentTestCardHTML + `</section><form role="search"></form>` + contentTestList(contentTestCardHTML) + contentTestArticle(`<p>Hidden text</p><video src="javascript:bad"></video>`)
	for _, tc := range []struct{ name, open, close string }{
		{"script", "<script>", "</script>"}, {"style", "<style>", "</style>"},
		{"iframe", "<iframe>", "</iframe>"}, {"noscript", "<noscript>", "</noscript>"},
		{"template", "<template>", "</template>"}, {"object", "<object>", "</object>"},
		{"hidden", `<div hidden="false">`, "</div>"}, {"aria", `<div aria-hidden="TrUe">`, "</div>"},
		{"ad", `<div data-role="ad">`, "</div>"},
		{"display", `<div style=" color:red; DISPLAY : none !important ; ">`, "</div>"},
		{"visibility", `<div style="visibility: Hidden!important">`, "</div>"},
		{"collapse", `<div style="VISIBILITY : COLLAPSE !IMPORTANT">`, "</div>"},
		{"embed", `<embed data-role="content" src="https://media.invalid/plugin">`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hidden := tc.open + decoy + tc.close
			if tc.name == "embed" {
				hidden = tc.open // embed is void, not a wrapper.
			}
			home, err := ParseHome(strings.NewReader(contentTestHomeHTML+hidden), contentTestBase)
			if err != nil || len(home.Categories) != 0 || len(home.Recommendations) != 0 {
				t.Fatalf("hidden home = %+v, %v", home, err)
			}
			list, err := ParseList(strings.NewReader(contentTestList(hidden)), contentTestBase)
			if err != nil || list.Title != "Field notes" || len(list.Items) != 0 {
				t.Fatalf("hidden list = %+v, %v", list, err)
			}
			article, err := ParseArticle(strings.NewReader(contentTestArticle(`<p>Before</p>`+hidden+`<p>After</p>`)), contentTestBase)
			if err != nil || article.Content != "Before\n\nAfter" || len(article.Videos) != 0 {
				t.Fatalf("hidden article = %+v, %v", article, err)
			}
		})
	}
}

func TestContentMalformedSchemas(t *testing.T) {
	for _, tc := range []struct{ name, kind, body string }{
		{"missing home", "home", `<h1>Ordinary page</h1>`},
		{"missing categories", "home", strings.Replace(contentTestHomeHTML, `<nav data-role="categories"></nav>`, "", 1)},
		{"missing recommendations", "home", strings.Replace(contentTestHomeHTML, `<section data-role="recommendations"></section>`, "", 1)},
		{"missing search", "home", `<nav data-role="categories"></nav><section data-role="recommendations"></section>`},
		{"duplicate categories", "home", contentTestHomeHTML + `<nav data-role="categories"></nav>`},
		{"duplicate recommendations", "home", contentTestHomeHTML + `<section data-role="recommendations"></section>`},
		{"duplicate forms", "home", contentTestHomeHTML + `<form role="search"><input type="search" name="q"></form>`},
		{"blank category", "home", strings.Replace(contentTestHomeHTML, `</nav>`, `<a href="/science"> </a></nav>`, 1)},
		{"empty category URL", "home", strings.Replace(contentTestHomeHTML, `</nav>`, `<a href="">Science</a></nav>`, 1)},
		{"missing category URL", "home", strings.Replace(contentTestHomeHTML, `</nav>`, `<a>Science</a></nav>`, 1)},
		{"malformed recommendation", "home", strings.Replace(contentTestHomeHTML, `</section>`, `<article data-role="card"></article></section>`, 1)},
		{"missing search input", "home", strings.Replace(contentTestHomeHTML, `<input type="search" name="q">`, "", 1)},
		{"disabled search", "home", strings.Replace(contentTestHomeHTML, `type="search"`, `disabled type="search"`, 1)},
		{"wrong input type", "home", strings.Replace(contentTestHomeHTML, `type="search"`, `type="text"`, 1)},
		{"missing input name", "home", strings.Replace(contentTestHomeHTML, ` name="q"`, "", 1)},
		{"duplicate inputs", "home", strings.Replace(contentTestHomeHTML, `</form>`, `<input type="search" name="other"></form>`, 1)},
		{"POST form", "home", strings.Replace(contentTestHomeHTML, `role="search"`, `role="search" method="post"`, 1)},
		{"unsupported method", "home", strings.Replace(contentTestHomeHTML, `role="search"`, `role="search" method="dialog"`, 1)},
		{"missing list", "list", `<main></main>`},
		{"duplicate lists", "list", contentTestList("") + contentTestList("")},
		{"missing list title", "list", `<main data-role="article-list"></main>`},
		{"blank list title", "list", strings.Replace(contentTestList(""), "Field notes", " ", 1)},
		{"nested list title", "list", `<main data-role="article-list"><div><h1 data-role="title">Notes</h1></div></main>`},
		{"duplicate list titles", "list", contentTestList(`<h2 data-role="title">Second</h2>`)},
		{"missing card title", "list", contentTestList(`<article data-role="card"></article>`)},
		{"nested card title", "list", contentTestList(`<article data-role="card"><div><a data-role="title" href="/notes">Notes</a></div></article>`)},
		{"duplicate card titles", "list", contentTestList(strings.Replace(contentTestCardHTML, `</article>`, `<a data-role="title" href="/second">Second</a></article>`, 1))},
		{"ambiguous title anchors", "list", contentTestList(strings.Replace(contentTestCardHTML, `</h2>`, `<a href="/second">Second</a></h2>`, 1))},
		{"blank card title", "list", contentTestList(strings.Replace(contentTestCardHTML, "Forest sensors", " ", 1))},
		{"missing card URL", "list", contentTestList(strings.Replace(contentTestCardHTML, ` href="/notes"`, "", 1))},
		{"empty card URL", "list", contentTestList(strings.Replace(contentTestCardHTML, `href="/notes"`, `href=""`, 1))},
		{"missing article", "article", `<article></article>`},
		{"duplicate articles", "article", contentTestArticle("") + contentTestArticle("")},
		{"missing article title", "article", `<article data-role="article"><div data-role="content">Text</div></article>`},
		{"blank article title", "article", strings.Replace(contentTestArticle("Text"), "Forest sensors", " ", 1)},
		{"nested article title", "article", `<article data-role="article"><div><h1 data-role="title">Notes</h1></div><div data-role="content">Text</div></article>`},
		{"duplicate article titles", "article", strings.Replace(contentTestArticle("Text"), `</h1>`, `</h1><h2 data-role="title">Second</h2>`, 1)},
		{"missing content", "article", `<article data-role="article"><h1 data-role="title">Notes</h1></article>`},
		{"duplicate content", "article", contentTestArticle(`<div data-role="content">Second</div>`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := contentTestParse(tc.kind, strings.NewReader(tc.body), contentTestBase); err == nil {
				t.Fatal("malformed schema accepted")
			}
		})
	}
	for _, current := range []string{"", "0", "-1", "two", "1.5", "999999999999999999999999999999"} {
		t.Run("pagination current "+current, func(t *testing.T) {
			body := contentTestList(`<nav data-role="pagination"><span aria-current="page">` + current + `</span></nav>`)
			if _, err := ParseList(strings.NewReader(body), contentTestBase); err == nil {
				t.Fatal("invalid current page accepted")
			}
		})
	}
	for _, pagination := range []string{
		`<nav data-role="pagination"></nav>`,
		`<nav data-role="pagination"><b aria-current="page">1</b><b aria-current="page">2</b></nav>`,
		`<nav data-role="pagination"><b aria-current="page">1</b></nav><nav data-role="pagination"><b aria-current="page">2</b></nav>`,
		`<nav data-role="pagination"><b aria-current="page">1</b><a rel="next" href="/two">Next</a><a rel="nofollow next" href="/two">Next</a></nav>`,
		`<nav data-role="pagination"><b aria-current="page">2</b><a rel="prev" href="/one">Prev</a><a rel="prev nofollow" href="/one">Prev</a></nav>`,
	} {
		if _, err := ParseList(strings.NewReader(contentTestList(pagination)), contentTestBase); err == nil {
			t.Fatalf("ambiguous/missing pagination accepted: %s", pagination)
		}
	}
}

func TestContentInvalidURLs(t *testing.T) {
	for _, raw := range []string{"javascript:alert(1)", "data:text/plain,no", "file:///tmp/no", "https://user:pass@pages.invalid/a", "https://pages.invalid:99999/a", "https://pages.invalid\\a", "/a\nb", "/a\tb", "/a\x01b", "\n", "\t"} {
		t.Run(fmt.Sprintf("%q", raw), func(t *testing.T) {
			escaped := html.EscapeString(raw)
			for _, tc := range []struct{ name, kind, body string }{
				{"category", "home", strings.Replace(contentTestHomeHTML, `</nav>`, `<a href="`+escaped+`">Science</a></nav>`, 1)},
				{"search", "home", strings.Replace(contentTestHomeHTML, `role="search"`, `role="search" action="`+escaped+`"`, 1)},
				{"card", "list", contentTestList(strings.Replace(contentTestCardHTML, "/notes", escaped, 1))},
				{"cover", "list", contentTestList(strings.Replace(contentTestCardHTML, `</article>`, `<img data-role="cover" src="`+escaped+`"></article>`, 1))},
				{"next", "list", contentTestList(`<nav data-role="pagination"><b aria-current="page">1</b><a rel="next" href="` + escaped + `">Next</a></nav>`)},
				{"previous", "list", contentTestList(`<nav data-role="pagination"><b aria-current="page">2</b><a rel="prev" href="` + escaped + `">Prev</a></nav>`)},
				{"video", "article", contentTestArticle(`<video src="` + escaped + `"><source src="https://media.invalid/good.mp4"></video>`)},
				{"source", "article", contentTestArticle(`<video><source src="` + escaped + `"></video>`)},
				{"poster", "article", contentTestArticle(`<video src="https://media.invalid/good.mp4" poster="` + escaped + `"></video>`)},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if err := contentTestParse(tc.kind, strings.NewReader(tc.body), contentTestBase); err == nil {
						t.Fatal("invalid URL accepted")
					}
				})
			}
			if _, err := BuildSearchURL(SearchForm{Action: raw, QueryName: "q"}, "forest"); err == nil {
				t.Fatal("invalid search action accepted")
			}
		})
	}
}

func TestContentSearch(t *testing.T) {
	for _, tc := range []struct{ name, base, attrs, want string }{
		{"missing action", contentTestBase, "", "https://pages.invalid/dir/home"},
		{"empty action", contentTestBase, ` action=""`, "https://pages.invalid/dir/home"},
		{"relative GET", contentTestBase, ` method="get" action="../search#top"`, "https://pages.invalid/search"},
		{"HTTPS default port", contentTestBase, ` action="https://PAGES.invalid:443/search"`, "https://pages.invalid:443/search"},
		{"HTTP default port", "http://pages.invalid:80/home", ` action="//pages.invalid/search"`, "http://pages.invalid/search"},
		{"cross origin", contentTestBase, ` action="https://other.invalid/search"`, ""},
		{"cross scheme", contentTestBase, ` action="http://pages.invalid/search"`, ""},
		{"different port", contentTestBase, ` action="https://pages.invalid:444/search"`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(contentTestHomeHTML, `role="search"`, `role="search"`+tc.attrs, 1)
			got, err := ParseHome(strings.NewReader(body), tc.base)
			if tc.want == "" {
				if err == nil {
					t.Fatal("cross-origin search accepted")
				}
			} else if err != nil || got.Search != (SearchForm{Action: tc.want, QueryName: "q"}) {
				t.Fatalf("search = %+v, %v", got.Search, err)
			}
		})
	}
	form := SearchForm{Action: "https://pages.invalid/search?lang=zh&keep=one&keep=two&q=old&q=stale#form", QueryName: "q"}
	got, err := BuildSearchURL(form, "  森林 观测 &?#  ")
	want := "https://pages.invalid/search?keep=one&keep=two&lang=zh&q=%E6%A3%AE%E6%9E%97+%E8%A7%82%E6%B5%8B+%26%3F%23"
	if err != nil || got != want {
		t.Fatalf("encoded search = %q, %v; want %q", got, err, want)
	}
	for _, query := range []string{"", " \t\n\u3000"} {
		if _, err := BuildSearchURL(form, query); err == nil {
			t.Fatalf("empty query accepted: %q", query)
		}
	}
	for _, name := range []string{"", "two words", " q", "q\t", "q\x01", "q\u3000name"} {
		if _, err := BuildSearchURL(SearchForm{Action: form.Action, QueryName: name}, "forest"); err == nil {
			t.Errorf("invalid search name accepted: %q", name)
		}
		body := strings.Replace(contentTestHomeHTML, `name="q"`, `name="`+html.EscapeString(name)+`"`, 1)
		if _, err := ParseHome(strings.NewReader(body), contentTestBase); err == nil {
			t.Errorf("invalid form name accepted: %q", name)
		}
	}
}

type contentTestErrorReader struct{}

func (contentTestErrorReader) Read([]byte) (int, error) { return 0, errors.New("fixture read error") }

func TestContentInputBoundaries(t *testing.T) {
	for kind, body := range map[string]string{"home": contentTestHomeHTML, "list": contentTestList(""), "article": contentTestArticle("Text")} {
		t.Run(kind, func(t *testing.T) {
			for _, extra := range []int{0, 1} {
				r := strings.NewReader(body + strings.Repeat(" ", maxBodyBytes-len(body)+extra))
				err := contentTestParse(kind, r, contentTestBase)
				if (err != nil) != (extra == 1) {
					t.Fatalf("size %d: %v", maxBodyBytes+extra, err)
				}
			}
			for _, prefix := range []string{"", body} {
				if err := contentTestParse(kind, io.MultiReader(strings.NewReader(prefix), contentTestErrorReader{}), contentTestBase); err == nil {
					t.Fatal("reader error ignored")
				}
			}
			for _, base := range []string{"", "/relative", "ftp://pages.invalid/", "https://u:p@pages.invalid/", "https://pages.invalid\\bad", "https://pages.invalid/a\nb"} {
				if err := contentTestParse(kind, strings.NewReader(body), base); err == nil {
					t.Fatalf("invalid base accepted: %q", base)
				}
			}
		})
	}
}

type contentTestRoundTripper func(*http.Request) (*http.Response, error)

func (f contentTestRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestContentIntegration(t *testing.T) {
	const query = "森林 观测 &?#"
	const articleHTML = `<article data-role="article"><h1 data-role="title">Forest sensors</h1><p data-role="summary">Measuring the canopy</p><img data-role="cover" src="https://media.invalid/forest.jpg"><time datetime="2026-06-02">Today</time><div data-role="content"><p>Sensors track <em>forest</em> growth.</p><p>Rain &amp; sunlight.</p><video src="https://media.invalid/forest.mp4#start" type="video/mp4" poster="https://media.invalid/poster.jpg">Fallback text</video><script src="/never-script"></script><div data-role="ad">Not article text</div></div></article>`
	homeHTML := strings.Replace(contentTestHomeHTML, `</nav>`, `<a href="/category">Nature</a></nav>`, 1)
	homeHTML = strings.Replace(homeHTML, `</section>`, contentTestCardHTML+`</section>`, 1)
	homeHTML = strings.Replace(homeHTML, `role="search"`, `role="search" action="/search?lang=zh"`, 1)
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.RequestURI())
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/home":
			fmt.Fprint(w, homeHTML)
		case "/category":
			fmt.Fprint(w, contentTestList(contentTestCardHTML+`<nav data-role="pagination"><b aria-current="page">1</b><a rel="next" href="/category/page2">Next</a></nav>`))
		case "/category/page2":
			fmt.Fprint(w, contentTestList(contentTestCardHTML+`<nav data-role="pagination"><b aria-current="page">2</b><a rel="prev" href="/category">Previous</a></nav>`))
		case "/notes":
			fmt.Fprint(w, articleHTML)
		case "/search":
			if got := r.URL.Query(); got.Get("q") != query || got.Get("lang") != "zh" || len(got) != 2 {
				t.Errorf("search query = %v", got)
			}
			fmt.Fprint(w, contentTestList(contentTestCardHTML))
		case "/redirect":
			http.Redirect(w, r, "/notes", http.StatusFound)
		default:
			t.Errorf("unexpected request reached server: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	origin, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"/home": true, "/category": true, "/category/page2": true, "/notes": true, "/search": true, "/redirect": true}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	redirectErr := errors.New("fixture redirects forbidden")
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: contentTestRoundTripper(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodGet || r.URL.Scheme != origin.Scheme || r.URL.Host != origin.Host || r.URL.User != nil || r.URL.Opaque != "" || !allowed[r.URL.Path] {
				return nil, fmt.Errorf("fixture request outside allowlist: %s", r.URL)
			}
			return transport.RoundTrip(r)
		}),
		CheckRedirect: func(*http.Request, []*http.Request) error { return redirectErr },
	}
	fetch := func(raw string) io.Reader {
		t.Helper()
		resp, err := client.Get(raw)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %s", raw, resp.Status)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
		if err != nil || len(body) > maxBodyBytes {
			t.Fatalf("read fixture: %v, bytes=%d", err, len(body))
		}
		return strings.NewReader(string(body))
	}
	homeURL := srv.URL + "/home"
	home, err := ParseHome(fetch(homeURL), homeURL)
	if err != nil || len(home.Categories) != 1 || len(home.Recommendations) != 1 {
		t.Fatalf("home = %+v, %v", home, err)
	}
	categoryURL := home.Categories[0].URL
	list, err := ParseList(fetch(categoryURL), categoryURL)
	if err != nil || len(list.Items) != 1 || list.Pagination.Current != 1 || list.Pagination.NextURL != srv.URL+"/category/page2" {
		t.Fatalf("category = %+v, %v", list, err)
	}
	page2URL := list.Pagination.NextURL
	page2, err := ParseList(fetch(page2URL), page2URL)
	if err != nil || len(page2.Items) != 1 || page2.Pagination != (Pagination{Current: 2, PreviousURL: categoryURL}) {
		t.Fatalf("page two = %+v, %v", page2, err)
	}
	detailURL := home.Recommendations[0].URL
	detail, err := ParseArticle(fetch(detailURL), detailURL)
	wantDetail := ArticleDetail{
		ArticleSummary: ArticleSummary{Title: "Forest sensors", URL: srv.URL + "/notes", Summary: "Measuring the canopy", CoverURL: "https://media.invalid/forest.jpg", PublishedAt: "2026-06-02"},
		Content:        "Sensors track forest growth.\n\nRain & sunlight.",
		Videos:         []VideoLink{{URL: "https://media.invalid/forest.mp4", Type: "video/mp4", PosterURL: "https://media.invalid/poster.jpg"}},
	}
	if err != nil || !reflect.DeepEqual(detail, wantDetail) {
		t.Fatalf("recommendation detail = %+v, %v", detail, err)
	}
	searchURL, err := BuildSearchURL(home.Search, "  "+query+"  ")
	if err != nil {
		t.Fatal(err)
	}
	results, err := ParseList(fetch(searchURL), searchURL)
	if err != nil || len(results.Items) != 1 || results.Items[0] != home.Recommendations[0] {
		t.Fatalf("search results = %+v, %v", results, err)
	}
	resultURL := results.Items[0].URL
	searchDetail, err := ParseArticle(fetch(resultURL), resultURL)
	if err != nil || !reflect.DeepEqual(searchDetail, wantDetail) {
		t.Fatalf("search detail = %+v, %v", searchDetail, err)
	}
	if resp, err := client.Get(srv.URL + "/redirect"); !errors.Is(err, redirectErr) {
		if resp != nil {
			resp.Body.Close()
		}
		t.Fatalf("redirect not rejected: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	wantPaths := []string{"/home", "/category", "/category/page2", "/notes", "/search?lang=zh&q=%E6%A3%AE%E6%9E%97+%E8%A7%82%E6%B5%8B+%26%3F%23", "/notes", "/redirect"}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("requests = %v; want %v (no resource fetches)", paths, wantPaths)
	}
	t.Logf("categories=%d recommendations=%d category_items=%d page2_items=%d search_items=%d requests=%d", len(home.Categories), len(home.Recommendations), len(list.Items), len(page2.Items), len(results.Items), len(paths))
	t.Logf("article=%q summary=%q date=%q content=%q videos=%+v", detail.Title, detail.Summary, detail.PublishedAt, detail.Content, detail.Videos)
}
