package javdb

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func mustDoc(t *testing.T, html string) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return doc
}

func TestParseMovieList(t *testing.T) {
	movies := parseMovieList(mustDoc(t, fixtureListingHTML), 2)
	if len(movies) != 3 {
		t.Fatalf("got %d movies, want 3", len(movies))
	}
	first := movies[0]
	if first.ID != "yxY7kW" || first.Href != "/v/yxY7kW" || first.Page != 2 {
		t.Fatalf("identity: %+v", first)
	}
	if first.Code != "SSIS-001" || first.Title != "標題一" || first.OriginTitle != "標題一" {
		t.Fatalf("title/code: %+v", first)
	}
	if first.Score != 4.45 || first.Ratings != 386 {
		t.Fatalf("score: %+v", first)
	}
	if first.RateText != "4.45分, 由386人評價" {
		t.Fatalf("rate text: %q", first.RateText)
	}
	if first.ReleaseDate != "2026-09-15" {
		t.Fatalf("release date: %q", first.ReleaseDate)
	}
	if !first.HasCNSub || first.Ranking != 1 || !strings.HasPrefix(first.CoverURL, "https://c0.jdbstatic.com/") {
		t.Fatalf("flags/cover: %+v cover=%s", first, first.CoverURL)
	}
	if len(first.Tags) != 1 || first.Tags[0] != "巨乳" {
		t.Fatalf("tags: %v", first.Tags)
	}
	if first.Source != "web" {
		t.Fatalf("source: %q", first.Source)
	}

	second := movies[1]
	if second.Code != "MIDE-842" || !second.CanPlay || second.Score != 0 {
		t.Fatalf("second card: %+v", second)
	}
	if second.CoverURL != "/images/0eEZqk_cover.jpg" {
		t.Fatalf("lazy cover: %q", second.CoverURL)
	}
	if len(second.Tags) != 0 {
		t.Fatalf("utility badges must not leak into tags: %v", second.Tags)
	}

	// No <strong>: the leading token is only taken as a code when it looks
	// like one (FC2-PPV-1234567 does).
	third := movies[2]
	if third.Code != "FC2-PPV-1234567" || third.Title != "無碼作品" {
		t.Fatalf("third card: %+v", third)
	}
}

func TestParseMovieListFallsBackToAnchors(t *testing.T) {
	const html = `<div class="section"><a href="/v/abc1"><div class="video-title"><strong>ABC-123</strong> 標題</div></a></div>`
	movies := parseMovieList(mustDoc(t, html), 1)
	if len(movies) != 1 || movies[0].ID != "abc1" || movies[0].Code != "ABC-123" {
		t.Fatalf("anchor fallback: %+v", movies)
	}
}

func TestParsePagination(t *testing.T) {
	cur, maxPage := parsePagination(mustDoc(t, fixtureListingHTML))
	if cur != 2 {
		t.Fatalf("current: %d", cur)
	}
	if maxPage != 7 {
		t.Fatalf("max page: %d", maxPage)
	}

	// A truncated window with a "next" link can only be estimated.
	const truncated = `<div class="movie-list"></div><nav>
	  <a class="pagination-link is-current" href="#">1</a>
	  <a class="pagination-link" href="?page=2">2</a>
	  <a class="pagination-next" href="?page=2">next</a></nav>`
	cur, maxPage = parsePagination(mustDoc(t, truncated))
	if cur != 1 || maxPage != 2 {
		t.Fatalf("estimate: cur=%d max=%d", cur, maxPage)
	}

	// No pagination markup at all.
	cur, maxPage = parsePagination(mustDoc(t, `<html><body><p>x</p></body></html>`))
	if cur != 1 || maxPage != 0 {
		t.Fatalf("defaults: cur=%d max=%d", cur, maxPage)
	}
}

func TestParseActorBoxes(t *testing.T) {
	actors := parseActorBoxes(mustDoc(t, fixtureActorsHTML))
	if len(actors) != 2 {
		t.Fatalf("got %d actors", len(actors))
	}
	a := actors[0]
	if a.ID != "kzx6" || a.Name != "小明" || a.Href != "/actors/kzx6" {
		t.Fatalf("identity: %+v", a)
	}
	if a.OtherName != "別名A, 別名B" {
		t.Fatalf("other name: %q", a.OtherName)
	}
	if a.VideosCount != 128 {
		t.Fatalf("videos count: %d", a.VideosCount)
	}
	if !strings.HasPrefix(a.AvatarURL, "https://c0.jdbstatic.com/") {
		t.Fatalf("avatar: %q", a.AvatarURL)
	}
	if actors[1].AvatarURL != "/avatars/21Jp.jpg" {
		t.Fatalf("lazy avatar: %q", actors[1].AvatarURL)
	}
}

func TestParsePageParam(t *testing.T) {
	cases := map[string]int{
		"?page=4":             4,
		"/search?q=x&page=12": 12,
		"?page=3#anchor":      3,
		"/v/abc":              1,
		"?sort=newest":        1,
		"?page=0":             1,
	}
	for href, want := range cases {
		if got := ParsePageParam(href); got != want {
			t.Errorf("ParsePageParam(%q) = %d, want %d", href, got, want)
		}
	}
}

func TestFirstDateAndCollapseSpace(t *testing.T) {
	if got := firstDate("2026-09-14 / 片商A"); got != "2026-09-14" {
		t.Fatalf("firstDate: %q", got)
	}
	if got := firstDate("沒有日期"); got != "" {
		t.Fatalf("firstDate negative: %q", got)
	}
	if got := collapseSpace(" a\t b \n c "); got != "a b c" {
		t.Fatalf("collapseSpace: %q", got)
	}
}
