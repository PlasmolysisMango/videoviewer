package javdb

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"
)

// newDualClient wires one stub as both the JSON API and the HTML site, which is
// how the package behaves in production: api first, web as the fallback.
func newDualClient(t *testing.T, st *stub, extra ...Option) *Client {
	t.Helper()
	opts := append([]Option{
		WithSites(st.URL()),
		WithAPIBase(st.APIBase()),
		WithRateLimit(0),
		WithRetry(0, time.Millisecond),
		WithCache(0),
		WithTimeout(5 * time.Second),
	}, extra...)
	c, err := New(opts...)
	requireNoErr(t, err)
	if got := c.Backends(); len(got) != 2 || got[0] != "api" || got[1] != "web" {
		t.Fatalf("backend order: %v", got)
	}
	return c
}

// unavailableAPI answers every app endpoint with the "please log in" envelope,
// forcing the aggregator on to the HTML backend.
func unavailableAPI() string {
	return apiErrBody("JWTVerificationError", "請登錄帳號")
}

func TestClientFallsBackToWebWhenAPIDenies(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v2/search": unavailableAPI(),
		"/search":        fixtureListingHTML,
	}))
	c := newDualClient(t, st)

	res, err := c.Search(context.Background(), Query{Keyword: "ssis"})
	requireNoErr(t, err)
	if res.Source != "web" {
		t.Fatalf("expected the web backend to answer, got %q", res.Source)
	}
	if len(res.Movies) != 3 {
		t.Fatalf("movies: %d", len(res.Movies))
	}
	if res.Query.Keyword != "ssis" {
		t.Fatalf("query not echoed back: %+v", res.Query)
	}
	if st.count("/api/v2/search") != 1 || st.count("/search") != 1 {
		t.Fatalf("both backends should be tried once: api=%d web=%d",
			st.count("/api/v2/search"), st.count("/search"))
	}
}

func TestClientKeepsAggregateWhenEveryBackendFails(t *testing.T) {
	st := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/search" {
			writeJSON(w, apiErrBody("ResourceNotFound", "查無此資源"))
			return
		}
		writeHTML(w, fixtureLoginWallHTML)
	})
	c := newDualClient(t, st)
	_, err := c.Search(context.Background(), Query{Keyword: "ssis"})
	if err == nil {
		t.Fatal("expected an aggregate error")
	}
	// Both failures are deterministic but of different kinds, so the caller gets
	// both wrapped and can still match either sentinel.
	requireSentinel(t, err, ErrNotFound)
	requireSentinel(t, err, ErrAuthRequired)
	var multi *MultiError
	if !errors.As(err, &multi) {
		t.Fatalf("want *MultiError, got %T: %v", err, err)
	}
	if len(multi.Errs) != 2 {
		t.Fatalf("per-backend errors: %d", len(multi.Errs))
	}
}

func TestClientPrefersAPIWhenBothAnswer(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v2/search":   apiBody(fixtureAPISearchData),
		"/search":          fixtureListingHTML,
		"/rankings/top":    fixtureListingHTML,
		"/api/v1/movies":   unavailableAPI(),
		"/api/v1/rankings": unavailableAPI(),
	}))
	c := newDualClient(t, st)
	res, err := c.Search(context.Background(), Query{Keyword: "ssis"})
	requireNoErr(t, err)
	if res.Source != "api" {
		t.Fatalf("source: %q", res.Source)
	}
	if st.count("/search") != 0 {
		t.Fatal("the web backend must not be consulted when the API answers")
	}
}

func TestClientRankingUsesWebForAPIUnknownKind(t *testing.T) {
	// The app API has no fanza-award endpoint; the HTML ranking page does.
	st := newStub(t, routerHandler(map[string]string{
		"/rankings/fanza_award": fixtureListingHTML,
	}))
	c := newDualClient(t, st)
	r, err := c.Ranking(context.Background(), RankingQuery{Kind: RankingFanzaAward})
	requireNoErr(t, err)
	if r.Kind != RankingFanzaAward || r.Source != "web" {
		t.Fatalf("ranking: %+v", r)
	}
	if st.count("/rankings/fanza_award") != 1 {
		t.Fatalf("web ranking requests: %d", st.count("/rankings/fanza_award"))
	}
}

// API 端点不可用（stub 404）时，实体 scope 的 category listing 回退 web。
func TestClientCategoryListingFallsBackToWebWhenAPILacks(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{"/makers/zKW": fixtureListingHTML}))
	c := newDualClient(t, st)
	res, err := c.CategoryMovies(context.Background(), CategoryQuery{Maker: "zKW"})
	requireNoErr(t, err)
	if len(res.Movies) != 3 {
		t.Fatalf("movies: %d", len(res.Movies))
	}
	if st.count("/api/v1/movies/tags") != 1 {
		t.Fatalf("api requests: %d", st.count("/api/v1/movies/tags"))
	}
	if got := st.lastRequest(t, "/makers/zKW").Query.Get("f"); got != "download" {
		t.Fatalf("maker listing filter: %q", got)
	}
}

func TestClientDetailFailsOverToWebWhenAPIDenies(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v4/movies/yxY7kW": unavailableAPI(),
		"/v/yxY7kW":             fixtureDetailHTML,
	}))
	c := newDualClient(t, st)
	d, err := c.Movie(context.Background(), "yxY7kW")
	requireNoErr(t, err)
	if d.ID != "yxY7kW" || d.Code != "SSIS-001" || d.Source != "web" {
		t.Fatalf("detail: %+v", d.Movie)
	}
	if !d.MagnetsFetched || len(d.Magnets) == 0 {
		t.Fatalf("magnet table not scraped: %+v", d)
	}
	// The detail page carries the magnet rows, so no second request is needed.
	if st.count("/v/yxY7kW") != 1 {
		t.Fatalf("detail requests: %d", st.count("/v/yxY7kW"))
	}
}

func TestClientMagnetsCodeResolutionAndOrdering(t *testing.T) {
	const magnets = `{"magnets":[
	  {"name":"SSIS-001.mp4","hash":"plain","size":5368709120,"hd":true,"files_count":1},
	  {"name":"SSIS-001 [CNSUB].mp4","hash":"sub","size":2147483648,"files_count":1},
	  {"name":"SSIS-001 UC無碼破解.mp4","hash":"hack","size":1073741824,"files_count":1}]}`
	st := newStub(t, routerHandler(map[string]string{
		"/api/v2/search":                apiBody(fixtureAPISearchData),
		"/api/v1/movies/yxY7kW/magnets": apiBody(magnets),
	}))
	c := newDualClient(t, st)

	list, err := c.Magnets(context.Background(), "SSIS-001")
	requireNoErr(t, err)
	if len(list) != 3 {
		t.Fatalf("magnets: %d", len(list))
	}
	// Subtitled torrents win, then hacked, then the plain file - regardless of
	// size, which is what users actually filter by.
	if list[0].Hash != "sub" || list[1].Hash != "hack" || list[2].Hash != "plain" {
		t.Fatalf("ordering: %+v", list)
	}
	if got := st.lastRequest(t, "/api/v1/movies/yxY7kW/magnets"); got.Method != http.MethodGet {
		t.Fatalf("magnet request: %s", got.Method)
	}
}

func TestClientBestMagnet(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v1/movies/yxY7kW/magnets": apiBody(`{"magnets":[
		  {"name":"SSIS-001.mp4","hash":"plain","size":5368709120},
		  {"name":"SSIS-001 [CNSUB].mp4","hash":"sub","size":1073741824}]}`),
	}))
	c := newDualClient(t, st)
	m, err := c.BestMagnet(context.Background(), "yxY7kW")
	requireNoErr(t, err)
	if m.Hash != "sub" {
		t.Fatalf("best magnet: %+v", m)
	}
	if m.MagnetURL() != "magnet:?xt=urn:btih:sub" {
		t.Fatalf("magnet uri: %q", m.MagnetURL())
	}
}

func TestClientBestMagnetNoneAvailable(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v1/movies/yxY7kW/magnets": apiBody(`{"magnets":[]}`),
	}))
	c := newDualClient(t, st)
	_, err := c.BestMagnet(context.Background(), "yxY7kW")
	requireSentinel(t, err, ErrNotFound)
}

func TestClientFindByCodeRequiresExactMatch(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v2/search": apiBody(`{"movies":[{"id":"a1","number":"SSIS-002","title":"乙"}],"current_page":1}`),
		"/search":        unavailableAPI(),
	}))
	c := newDualClient(t, st)
	_, err := c.FindByCode(context.Background(), "ssis-001")
	requireSentinel(t, err, ErrNotFound)
}

func TestClientFindByCodeAcceptsUniqueCodelessHit(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v2/search": apiBody(`{"movies":[{"id":"a1","number":null,"title":"標題一"}],"current_page":1}`),
	}))
	c := newDualClient(t, st)
	m, err := c.FindByCode(context.Background(), "標題一")
	requireNoErr(t, err)
	if m.ID != "a1" {
		t.Fatalf("movie: %+v", m)
	}
}

func TestClientSearchRejectsEmptyKeyword(t *testing.T) {
	c := newDualClient(t, newStub(t, routerHandler(map[string]string{})))
	_, err := c.Search(context.Background(), Query{Keyword: "   "})
	requireSentinel(t, err, ErrInvalidQuery)
}

func TestClientSearchActorScope(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v1/actors": apiBody(`{"actors":[{"id":"21Jp","name":"楓花戀"}],"current_page":1}`),
	}))
	c := newDualClient(t, st)
	res, err := c.Search(context.Background(), Query{Keyword: "楓花戀", Scope: ScopeActor})
	requireNoErr(t, err)
	if len(res.Actors) != 1 || res.Actors[0].ID != "21Jp" {
		t.Fatalf("actors: %+v", res.Actors)
	}
	if res.Source != "api" {
		t.Fatalf("source: %q", res.Source)
	}
}

func TestClientActorByNameThenProfile(t *testing.T) {
	const profile = `{"actor":{"id":"21Jp","name":"楓花戀","birthday":"2000-01-01",
	  "height":160,"bust":88,"waist":58,"hips":85,"birthplace":"日本"}}`
	st := newStub(t, routerHandler(map[string]string{
		"/api/v1/actors":      apiBody(`{"actors":[{"id":"21Jp","name":"楓花戀"}],"current_page":1}`),
		"/api/v1/actors/21Jp": apiBody(profile),
	}))
	c := newDualClient(t, st)
	a, err := c.Actor(context.Background(), "楓花戀")
	requireNoErr(t, err)
	if a.ID != "21Jp" || a.Name == "" {
		t.Fatalf("actor: %+v", a)
	}
	// The search already identified the id, so the profile is fetched once.
	if st.count("/api/v1/actors/21Jp") != 1 {
		t.Fatalf("profile requests: %d", st.count("/api/v1/actors/21Jp"))
	}
}

func TestClientActorByIDMissFallsBackToNameSearch(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v1/actors/21Jp": apiErrBody("ResourceNotFound", "資源不存在"),
		"/api/v1/actors":      apiBody(`{"actors":[{"id":"21Jp","name":"楓花戀"}],"current_page":1}`),
	}))
	c := newAPIClient(t, st)
	a, err := c.Actor(context.Background(), "21Jp")
	requireNoErr(t, err)
	if a.ID != "21Jp" {
		t.Fatalf("actor: %+v", a)
	}
	if st.count("/api/v1/actors") == 0 {
		t.Fatal("expected a name search after the id lookup failed")
	}
}

func TestClientMovieRetriesUnknownIDAsCode(t *testing.T) {
	// JavDB ids and video codes share one alphabet, so "aBCdE" is guessed as an
	// id first. When that guess 404s the client must re-resolve it as a code.
	st := newStub(t, routerHandler(map[string]string{
		"/api/v4/movies/aBCdE":          apiErrBody("ResourceNotFound", "資源不存在"),
		"/api/v4/movies/yxY7kW":         apiBody(`{"movie":{"id":"yxY7kW","number":"SSIS-001","title":"標題一"}}`),
		"/api/v1/movies/yxY7kW/magnets": apiBody(`{"magnets":[]}`),
		"/api/v2/search":                apiBody(`{"movies":[{"id":"yxY7kW","number":"ABCDE","title":"標題一"}],"current_page":1}`),
	}))
	c := newAPIClient(t, st)
	d, err := c.Movie(context.Background(), "aBCdE")
	requireNoErr(t, err)
	if d.ID != "yxY7kW" {
		t.Fatalf("resolved detail: %+v", d.Movie)
	}
	if st.count("/api/v4/movies/yxY7kW") != 1 {
		t.Fatalf("re-resolved detail requests: %d", st.count("/api/v4/movies/yxY7kW"))
	}
}

// 全小写+数字的 id（app API 搜索原样返回，如 SONE-855 的 "824qk5"）必须原样
// 作为 id 查询，不能被当成番号规范化大写（回归：详情页 not found: code 824QK5）。
func TestClientMovieTriesLowercaseDigitIDVerbatim(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v4/movies/824qk5":         apiBody(`{"movie":{"id":"824qk5","number":"SONE-855","title":"標題"}}`),
		"/api/v1/movies/824qk5/magnets": apiBody(`{"magnets":[]}`),
	}))
	c := newAPIClient(t, st)
	d, err := c.Movie(context.Background(), "824qk5")
	requireNoErr(t, err)
	if d.ID != "824qk5" || d.Code != "SONE-855" {
		t.Fatalf("detail: %+v", d.Movie)
	}
	if st.count("/api/v2/search") != 0 {
		t.Fatalf("must not fall back to code search, saw %d", st.count("/api/v2/search"))
	}
}

func TestClientLoginImportsSessionWithoutRequests(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v2/search": apiBody(fixtureAPISearchData),
		"/search":        fixtureListingHTML,
	}))
	c := newDualClient(t, st)
	requireNoErr(t, c.Login(context.Background(), Credentials{Cookie: "_jdb_session=1", AppToken: "jwt"}))
	if st.total() != 0 {
		t.Fatalf("importing a session must stay offline, saw %d requests", st.total())
	}
	if cookie, token := c.Session(); cookie != "_jdb_session=1" || token != "jwt" {
		t.Fatalf("session not stored: %q %q", cookie, token)
	}
	// Both backends now send the imported credentials.
	_, err := c.Search(context.Background(), Query{Keyword: "ssis"})
	requireNoErr(t, err)
	if got := st.lastRequest(t, "/api/v2/search").Header.Get("Authorization"); got != "Bearer jwt" {
		t.Fatalf("api authorization: %q", got)
	}
}

func TestClientBackendAccessors(t *testing.T) {
	// A web-only client must still expose the shared capability surfaces.
	st := newStub(t, routerHandler(map[string]string{"/search": fixtureListingHTML}))
	c := newWebClient(t, st)
	if c.API() != nil {
		t.Fatal("API() must be nil when the app backend is disabled")
	}
	if c.Web() == nil {
		t.Fatal("Web() must be set")
	}
	if c.Site() != st.URL() {
		t.Fatalf("Site(): %q", c.Site())
	}
}

func TestNewWithoutAnyBackend(t *testing.T) {
	if _, err := New(WithSites(), WithAPIBase("")); err == nil {
		t.Fatal("expected a configuration error")
	}
}

func TestClientSearchIsCachedAndReset(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v2/search": apiBody(fixtureAPISearchData),
	}))
	c := newDualClient(t, st, WithCache(time.Minute))
	ctx := context.Background()

	first, err := c.Search(ctx, Query{Keyword: "ssis-001"})
	requireNoErr(t, err)
	second, err := c.Search(ctx, Query{Keyword: "  SSIS-001 "})
	requireNoErr(t, err)
	if st.count("/api/v2/search") != 1 {
		t.Fatalf("normalised keyword must reuse the cache entry: %d requests", st.count("/api/v2/search"))
	}
	if first != second {
		t.Fatal("the cached result should be the very same pointer")
	}

	c.ResetCache()
	if _, err := c.Search(ctx, Query{Keyword: "ssis-001"}); err != nil {
		t.Fatal(err)
	}
	if st.count("/api/v2/search") != 2 {
		t.Fatalf("ResetCache did not drop the entry: %d requests", st.count("/api/v2/search"))
	}

	// A different filter is a different cache key.
	if _, err := c.Search(ctx, Query{Keyword: "ssis-001", Filter: FilterWithSub}); err != nil {
		t.Fatal(err)
	}
	if st.count("/api/v2/search") != 3 {
		t.Fatalf("filtered search reused a cached entry: %d requests", st.count("/api/v2/search"))
	}
}

func TestClientConcurrentSearchDedupesInFlight(t *testing.T) {
	release := make(chan struct{})
	st := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
		writeJSON(w, apiBody(fixtureAPISearchData))
	})
	c := newDualClient(t, st, WithCache(time.Minute))

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Search(context.Background(), Query{Keyword: "ssis"}); err != nil {
				t.Errorf("search: %v", err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if got := st.count("/api/v2/search"); got != 1 {
		t.Fatalf("concurrent identical searches cost %d requests, want 1", got)
	}
}

func TestClientSearchRejectsTagFilters(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{"/tags": fixtureListingHTML}))
	c := newDualClient(t, st)
	// Neither endpoint understands tag parameters, so the client must refuse
	// rather than return unfiltered hits that look like filtered ones.
	_, err := c.Search(context.Background(), Query{Keyword: "ssis", TagIDs: map[string]string{"4": "15"}})
	requireSentinel(t, err, ErrUnsupported)
	if st.total() != 0 {
		t.Fatalf("unsupported filter still cost %d requests", st.total())
	}
	// CategoryMovies is the supported route for the very same filter.
	res, err := c.CategoryMovies(context.Background(), CategoryQuery{TagIDs: map[string]string{"4": "15"}})
	requireNoErr(t, err)
	if len(res.Movies) != 3 {
		t.Fatalf("movies: %d", len(res.Movies))
	}
	if got := st.lastRequest(t, "/tags").Query.Get("c4"); got != "15" {
		t.Fatalf("tag param: %q", got)
	}
}

func TestLooksLikeID(t *testing.T) {
	cases := []struct {
		token string
		want  bool
	}{
		{"yxY7kW", true}, // app id, mixes letter cases
		{"21Jp", true},   // digits plus mixed cases, as most ids look
		{"abcd", true},   // lower-case only, no digits: ids exist, codes do not
		{"SSIS-001", false},
		{"062216_001", false},
		{"ABP123", false}, // upper case with digits: a video code
		{"n0656", false},  // lower case with digits: a video code
		{"ssis", true},    // ambiguous, but the id guess is retried as a code
		{"12345", false},
		{"", false},
		{"a", false},
		{"SSIS-001 標題", false},
		{"verylongtoken123", false},
	}
	for _, tc := range cases {
		if got := looksLikeID(tc.token); got != tc.want {
			t.Errorf("looksLikeID(%q) = %v, want %v", tc.token, got, tc.want)
		}
	}
}
