package javdb

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// newWebClient wires a client to the stub with the JSON API switched off.
func newWebClient(t *testing.T, st *stub, extra ...Option) *Client {
	t.Helper()
	opts := append([]Option{
		WithSites(st.URL()),
		WithAPIBase(""),
		WithRateLimit(0),
		WithRetry(0, time.Millisecond),
		WithCache(0),
		WithTimeout(5 * time.Second),
	}, extra...)
	c, err := New(opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.Backends(); len(got) != 1 || got[0] != "web" {
		t.Fatalf("backends: %v", got)
	}
	return c
}

func TestWebSearchMovies(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{"/search": fixtureListingHTML}))
	c := newWebClient(t, st)

	res, err := c.SearchMovies(context.Background(), "ssis", WithPage(2, 0), WithCategory(CategoryCensored))
	requireNoErr(t, err)
	if res.Source != "web" || len(res.Movies) != 3 {
		t.Fatalf("result: source=%s movies=%d", res.Source, len(res.Movies))
	}
	if res.Current != 2 || res.MaxPage != 7 {
		t.Fatalf("pagination scraped from the page: current=%d max=%d", res.Current, res.MaxPage)
	}
	if res.Movies[0].Page != 2 {
		t.Fatalf("page stamping: %+v", res.Movies[0])
	}
	q := st.lastRequest(t, "/search").Query
	if q.Get("q") != "ssis" || q.Get("f") != "censored" || q.Get("page") != "2" {
		t.Fatalf("query: %v", q)
	}
}

func TestWebSearchFilterTable(t *testing.T) {
	cases := []struct {
		name  string
		query Query
		want  string
	}{
		{"default", Query{Keyword: "x"}, "m"},
		{"subtitle flag", Query{WithSubtitle: true}, "cnsub"},
		{"subtitle filter", Query{Filter: FilterWithSub}, "cnsub"},
		{"playable", Query{Filter: FilterPlayable}, "playable"},
		{"download", Query{Filter: FilterDownloaded}, "download"},
		{"single", Query{Filter: FilterSingle}, "single"},
		{"actor scope", Query{Scope: ScopeActor}, "actor"},
		{"list scope", Query{Scope: ScopeTag}, "list"},
		{"category", Query{Category: CategoryUncensored}, "uncensored"},
		{"passthrough", Query{Filter: "code"}, "code"},
	}
	for _, tc := range cases {
		if got := webSearchFilter(tc.query); got != tc.want {
			t.Errorf("%s: webSearchFilter = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestWebRankingMovies(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{"/rankings/movies": fixtureListingHTML}))
	c := newWebClient(t, st)
	r, err := c.CategoryRanking(context.Background(), PeriodDaily, CategoryCensored, Page{})
	requireNoErr(t, err)
	if r.Kind != RankingMovies || r.Period != PeriodDaily || r.Category != CategoryCensored {
		t.Fatalf("ranking header: %+v", r)
	}
	if r.Title == "" {
		t.Fatal("title not scraped")
	}
	// The fixture prints a badge only for the first card, so positions are
	// derived from document order to keep them stable.
	if len(r.Movies) != 3 || r.Movies[0].Ranking != 1 || r.Movies[2].Ranking != 3 {
		t.Fatalf("ranks: %+v", r.Movies)
	}
	q := st.lastRequest(t, "/rankings/movies").Query
	if q.Get("p") != "daily" || q.Get("t") != "censored" {
		t.Fatalf("query: %v", q)
	}
}

func TestWebRankingInfersPeriodFromURL(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{"/rankings/movies": fixtureListingHTML}))
	c := newWebClient(t, st)
	r, err := c.Ranking(context.Background(), RankingQuery{Kind: RankingMovies, Period: PeriodMonthly})
	requireNoErr(t, err)
	if r.Period != PeriodMonthly {
		t.Fatalf("period: %q", r.Period)
	}
}

func TestWebActorRanking(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{"/rankings/actors": fixtureActorsHTML}))
	c := newWebClient(t, st)
	r, err := c.ActorRanking(context.Background(), CategoryCensored, Page{})
	requireNoErr(t, err)
	if len(r.Actors) != 2 || r.Actors[0].ID != "kzx6" || r.Actors[0].Ranking != 1 {
		t.Fatalf("actors: %+v", r.Actors)
	}
	if r.Actors[1].Ranking != 2 || r.Actors[1].Source != "web" {
		t.Fatalf("actor ranks: %+v", r.Actors[1])
	}
	if got := st.lastRequest(t, "/rankings/actors").Query.Get("t"); got != "censored" {
		t.Fatalf("category param: %q", got)
	}
}

func TestWebLoginGatedRanking(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{"/rankings/top": fixtureLoginWallHTML}))
	c := newWebClient(t, st)
	_, err := c.Top250(context.Background(), Top250All, Page{})
	requireSentinel(t, err, ErrAuthRequired)
	if st.count("/rankings/top") != 1 {
		t.Fatalf("auth walls must not be retried: %d", st.count("/rankings/top"))
	}
}

func TestWebRankingWithCookie(t *testing.T) {
	st := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") == "" {
			writeHTML(w, fixtureLoginWallHTML)
			return
		}
		writeHTML(w, fixtureListingHTML)
	})
	c := newWebClient(t, st, WithCookie("_jdb_session=abc"))
	r, err := c.Top250(context.Background(), Top250OfYear(2025), Page{})
	requireNoErr(t, err)
	if len(r.Movies) == 0 {
		t.Fatal("expected movies after login")
	}
	if got := st.lastRequest(t, "/rankings/top").Query.Get("t"); got != "y2025" {
		t.Fatalf("year slice param: %q", got)
	}
}

func TestWebDetailAndMagnetsSingleRequest(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{"/v/yxY7kW": fixtureDetailHTML}))
	c := newWebClient(t, st)
	d, err := c.Movie(context.Background(), "yxY7kW")
	requireNoErr(t, err)
	if d.Code != "SSIS-001" || len(d.Magnets) != 2 || !d.MagnetsFetched {
		t.Fatalf("detail: %+v magnets=%d", d.Movie, len(d.Magnets))
	}
	if got := st.count("/v/yxY7kW"); got != 1 {
		t.Fatalf("detail page fetched %d times, want 1", got)
	}
}

func TestWebMovieResolvedFromCode(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/search":   fixtureListingHTML,
		"/v/yxY7kW": fixtureDetailHTML,
	}))
	c := newWebClient(t, st)
	d, err := c.Movie(context.Background(), "ssis-001")
	requireNoErr(t, err)
	if d.ID != "yxY7kW" || d.Title == "" {
		t.Fatalf("resolved detail: %+v", d.Movie)
	}
	if got := st.lastRequest(t, "/search").Query.Get("f"); got != "code" {
		t.Fatalf("code search filter: %q", got)
	}
}

func TestWebMovieRejectsCodeAsID(t *testing.T) {
	w := &webBackend{t: testTransport(t, "https://example.invalid")}
	_, err := w.MovieDetail(context.Background(), "SSIS-001")
	requireSentinel(t, err, ErrInvalidQuery)
}

func TestWebCategoryListings(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/video_codes/SSIS": fixtureListingHTML,
		"/censored":         fixtureListingHTML,
		"/makers/zKW":       fixtureListingHTML,
		"/tags":             fixtureListingHTML,
		"/actors/kzx6":      fixtureListingHTML,
	}))
	c := newWebClient(t, st)
	ctx := context.Background()

	cases := []struct {
		name string
		q    CategoryQuery
		path string
	}{
		{"video code", CategoryQuery{VideoCode: "ssis"}, "/video_codes/SSIS"},
		{"bucket", CategoryQuery{Category: CategoryCensored}, "/censored"},
		{"maker", CategoryQuery{Maker: "zKW"}, "/makers/zKW"},
		{"tags", CategoryQuery{TagIDs: map[string]string{"4": "15", "1": "3"}}, "/tags"},
		{"actor", CategoryQuery{ActorID: "kzx6"}, "/actors/kzx6"},
	}
	for _, tc := range cases {
		res, err := c.CategoryMovies(ctx, tc.q)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(res.Movies) != 3 {
			t.Fatalf("%s: movies=%d", tc.name, len(res.Movies))
		}
		rec := st.lastRequest(t, tc.path)
		switch tc.name {
		case "video code", "maker":
			if rec.Query.Get("f") != "download" {
				t.Errorf("%s: f=%v", tc.name, rec.Query)
			}
		case "tags":
			if rec.Query.Get("c1") != "3" || rec.Query.Get("c4") != "15" {
				t.Errorf("%s: %v", tc.name, rec.Query)
			}
		case "actor":
			if rec.Query.Get("t") != "d" {
				t.Errorf("%s: %v", tc.name, rec.Query)
			}
		}
	}
}

func TestWebSearchActorsScansLibrary(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{"/actors/censored": fixtureActorsHTML}))
	c := newWebClient(t, st)
	actors, err := c.SearchActors(context.Background(), Query{
		Keyword:  "楓花戀",
		Category: CategoryCensored,
		Page:     Page{Limit: 1}, // one library page only
	})
	requireNoErr(t, err)
	if len(actors) != 1 || actors[0].ID != "21Jp" || actors[0].Source != "web" {
		t.Fatalf("matches: %+v", actors)
	}
	if got := st.count("/actors/censored"); got != 1 {
		t.Fatalf("library pages fetched: %d, want 1", got)
	}
}

func TestWebActorProfile(t *testing.T) {
	const profile = `<html><body>
	  <span class="actor-section-name">楓花戀</span>
	  <img class="avatar" src="https://x/rhe951l4q/a/1.jpg">
	  <div class="section-column"><div class="info">
	    生日:2000-01-01
	    身高:160
	    三圍:88-58-85
	    出生地:日本
	  </div></div>
	</body></html>`
	st := newStub(t, routerHandler(map[string]string{"/actors/21Jp": profile}))
	c := newWebClient(t, st)
	a, err := c.Actor(context.Background(), "21Jp")
	requireNoErr(t, err)
	if a.Name != "楓花戀" || a.Birthday != "2000-01-01" || a.Height != 160 {
		t.Fatalf("profile: %+v", a)
	}
	if a.Bust != 88 || a.Waist != 58 || a.Hips != 85 || a.Birthplace != "日本" {
		t.Fatalf("measurements: %+v", a)
	}
	if a.AvatarURL != "https://c0.jdbstatic.com/a/1.jpg" {
		t.Fatalf("avatar: %q", a.AvatarURL)
	}
}

func TestWebTagGroups(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{"/genres/censored": fixtureGenreHTML}))
	c := newWebClient(t, st)
	groups, err := c.TagGroups(context.Background(), CategoryCensored)
	requireNoErr(t, err)
	if len(groups) != 1 {
		t.Fatalf("groups: %+v", groups)
	}
	g := groups[0]
	if g.Name != "类别" || g.CategoryID != "4" {
		t.Fatalf("group header: %+v", g)
	}
	if len(g.Options) != 3 || g.Options[0].ID != "abc" || g.Options[1].ID != "151" || g.Options[2].ID != "15" {
		t.Fatalf("options: %+v", g.Options)
	}
}

func TestWebMirrorFailover(t *testing.T) {
	bad := newStub(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	good := newStub(t, routerHandler(map[string]string{"/search": fixtureListingHTML}))
	c, err := New(
		WithSites(bad.URL(), good.URL()),
		WithAPIBase(""),
		WithRateLimit(0),
		WithRetry(0, time.Millisecond),
		WithCache(0),
	)
	requireNoErr(t, err)
	res, err := c.SearchMovies(context.Background(), "ssis")
	requireNoErr(t, err)
	if len(res.Movies) != 3 {
		t.Fatalf("movies: %d", len(res.Movies))
	}
	if bad.count("/search") != 1 || good.count("/search") != 1 {
		t.Fatalf("failover counts: bad=%d good=%d", bad.count("/search"), good.count("/search"))
	}
}

func TestWebAuthErrorDoesNotFailOver(t *testing.T) {
	gated := newStub(t, routerHandler(map[string]string{"/search": fixtureLoginWallHTML}))
	other := newStub(t, routerHandler(map[string]string{"/search": fixtureListingHTML}))
	c, err := New(WithSites(gated.URL(), other.URL()), WithAPIBase(""), WithRateLimit(0), WithRetry(0, time.Millisecond), WithCache(0))
	requireNoErr(t, err)
	_, err = c.SearchMovies(context.Background(), "ssis")
	requireSentinel(t, err, ErrAuthRequired)
	if other.total() != 0 {
		t.Fatal("an auth wall is an answer, not a broken mirror")
	}
}

func TestWebReviewsUnsupported(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{}))
	c := newWebClient(t, st)
	_, err := c.Reviews(context.Background(), ReviewQuery{MovieID: "yxY7kW"})
	requireSentinel(t, err, ErrUnsupported)
}

func TestPageBlockedMessage(t *testing.T) {
	cases := []struct {
		html string
		want bool
	}{
		{`<div class="empty-message">請先登入</div>`, true},
		{`<div class="empty-message">沒有資料</div>`, false},
		{`<div class="movie-list"></div>`, false},
	}
	for _, tc := range cases {
		doc := mustDoc(t, tc.html)
		if got := pageBlockedMessage(doc) != ""; got != tc.want {
			t.Errorf("pageBlockedMessage(%q) = %v, want %v", tc.html, got, tc.want)
		}
	}
}

func TestWebNoSitesConfigured(t *testing.T) {
	c, err := New(WithSites(), WithAPIBase("https://api.invalid"))
	requireNoErr(t, err)
	if got := c.Web(); got != nil {
		t.Fatal("web backend must be disabled without sites")
	}
	b := &webBackend{t: c.t}
	b.t.opts.Sites = nil
	if _, _, err := b.getHTML(context.Background(), "/search", nil); !errors.Is(err, ErrNoSite) {
		t.Fatalf("getHTML without sites: %v", err)
	}
}
