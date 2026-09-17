package javdb

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

const fixtureAPISearchData = `{"movies":[
  {"id":"yxY7kW","number":"ssis-001","title":"標題一","origin_title":"標題一 完整版",
   "thumb_url":"https://www.javdb.com/rhe951l4q/images/t.jpg","cover_url":"https://www.javdb.com/rhe951l4q/images/c.jpg",
   "duration":"125","magnets_count":6,"can_play":true,"play_subtitle":1,"has_preview_video":true,
   "has_cnsub":true,"release_date":"2024-05-21","new_magnets":true,"score":"4.02",
   "tags":[{"id":15,"name":"巨乳"},{"id":88,"name":"制服"}],"actors":["楓花戀"],
   "preview_images":[{"thumb_url":"t1","large_url":"l1"}]},
  {"id":"0eEZqk","number":null,"title":"無番号作品","score":null,"actors":null}
],"current_page":3}`

// newAPIClient wires a client to the stub with the web backend switched off.
func newAPIClient(t *testing.T, st *stub, extra ...Option) *Client {
	t.Helper()
	opts := append([]Option{
		WithAPIBase(st.APIBase()),
		WithSites(), // no mirrors: forces every call through the JSON API
		WithRateLimit(0),
		WithRetry(0, time.Millisecond),
		WithCache(0),
		WithTimeout(5 * time.Second),
	}, extra...)
	c, err := New(opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.Backends(); len(got) != 1 || got[0] != "api" {
		t.Fatalf("backends: %v", got)
	}
	return c
}

func TestAPISearchMovies(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v2/search": apiBody(fixtureAPISearchData),
	}))
	c := newAPIClient(t, st)

	res, err := c.Search(context.Background(), Query{
		Keyword: "ssis-001", Scope: ScopeMovie, Category: CategoryCensored,
		Sort: SortNewest, Page: Page{Page: 3, Limit: 30},
	})
	requireNoErr(t, err)
	if res.Current != 3 || len(res.Movies) != 2 {
		t.Fatalf("result: current=%d movies=%d", res.Current, len(res.Movies))
	}
	if res.Source != "api" {
		t.Fatalf("source: %q", res.Source)
	}

	m := res.Movies[0]
	if m.ID != "yxY7kW" || m.Href != "/v/yxY7kW" || m.Source != "api" {
		t.Fatalf("identity: %+v", m)
	}
	if m.Code != "SSIS-001" {
		t.Fatalf("code normalisation: %q", m.Code)
	}
	if m.Score != 4.02 || m.Duration != 125 || m.ReleaseDate != "2024-05-21" {
		t.Fatalf("loose scalars: %+v", m)
	}
	if !m.HasCNSub || !m.CanPlay || !m.NewMagnets || m.MagnetsCount != 6 {
		t.Fatalf("flags: %+v", m)
	}
	if m.CoverURL != "https://c0.jdbstatic.com/images/c.jpg" {
		t.Fatalf("cover: %q", m.CoverURL)
	}
	if len(m.Tags) != 2 || m.Tags[0] != "巨乳" {
		t.Fatalf("tags: %v", m.Tags)
	}
	if len(m.Actors) != 1 || m.Actors[0] != "楓花戀" {
		t.Fatalf("actors: %v", m.Actors)
	}
	if res.Movies[1].Code != "" || res.Movies[1].Score != 0 {
		t.Fatalf("null number/score: %+v", res.Movies[1])
	}

	req := st.lastRequest(t, "/api/v2/search")
	want := map[string]string{
		"q": "ssis-001", "page": "3", "limit": "30", "type": "movie",
		"movie_type": "censored", "movie_sort_by": "newest", "movie_filter_by": "all",
		"from_recent": "false",
	}
	for k, v := range want {
		if got := req.Query.Get(k); got != v {
			t.Errorf("query %s = %q, want %q", k, got, v)
		}
	}
	if req.Header.Get("Accept") != "application/json" {
		t.Errorf("accept: %q", req.Header.Get("Accept"))
	}
	if got := req.Header.Get("User-Agent"); !strings.HasPrefix(got, "Dart/") {
		t.Errorf("app user agent: %q", got)
	}
	ts, clientID, digest := SplitSignature(req.Header.Get("jdsignature"))
	if ts == "" || clientID != signatureClientID || len(digest) != 32 {
		t.Fatalf("jdsignature header: %q", req.Header.Get("jdsignature"))
	}
}

func TestAPISearchParameterTranslation(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v2/search": apiBody(fixtureAPISearchData),
		"/api/v1/actors": apiBody(`{"actors":[{"id":"21Jp","name":"楓花戀"}]}`),
	}))
	c := newAPIClient(t, st)
	ctx := context.Background()

	_, err := c.Search(ctx, Query{Keyword: "x", WithSubtitle: true, FromRecent: true, Year: 2024, Month: 7})
	requireNoErr(t, err)
	q := st.lastRequest(t, "/api/v2/search").Query
	if q.Get("movie_filter_by") != string(FilterWithSub) || q.Get("from_recent") != "true" {
		t.Fatalf("filters: %v", q)
	}
	if q.Get("year") != "2024" || q.Get("month") != "7" {
		t.Fatalf("date narrowing: %v", q)
	}
	if q.Get("movie_sort_by") != string(SortRelevance) {
		t.Fatalf("default sort: %v", q)
	}

	_, err = c.SearchActors(ctx, Query{Keyword: "楓花戀", Category: CategoryUncensored})
	requireNoErr(t, err)
	req := st.lastRequest(t, "/api/v1/actors")
	// The API ignores the search param, so it is never sent; matching is
	// done client-side over the fetched candidates.
	if req.Query.Get("search") != "" {
		t.Fatalf("actor search must not send search param: %v", req.Query)
	}
	if req.Query.Get("type") != "1" {
		t.Fatalf("actor search: %v", req.Query)
	}
	if got := req.Header.Get("jdsignature"); got == "" {
		t.Fatal("actor search must be signed too")
	}
}

func TestAPIErrorEnvelopeMapping(t *testing.T) {
	cases := []struct {
		action, message string
		want            error
	}{
		{"JWTVerificationError", "請登錄帳號", ErrAuthRequired},
		{"ResourceNotFound", "資源不存在", ErrNotFound},
		{"ParameterInvalid", "參數錯誤", ErrInvalidQuery},
		{"SomeOtherError", "boom", nil},
	}
	for _, tc := range cases {
		st := newStub(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, apiErrBody(tc.action, tc.message))
		})
		c := newAPIClient(t, st)
		_, err := c.Search(context.Background(), Query{Keyword: "x"})
		if err == nil {
			t.Fatalf("%s: expected error", tc.action)
		}
		if tc.want == nil {
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("%s: want *APIError, got %T %v", tc.action, err, err)
			}
			if apiErr.Code != tc.action || !strings.Contains(apiErr.Error(), tc.message) {
				t.Fatalf("api error: %+v", apiErr)
			}
			continue
		}
		requireSentinel(t, err, tc.want)
	}
}

func TestAPISearchEmptyResult(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v2/search": apiBody(`{"movies":[],"current_page":1}`),
	}))
	c := newAPIClient(t, st)
	_, err := c.Search(context.Background(), Query{Keyword: "不存在"})
	requireSentinel(t, err, ErrEmptyResult)
}

func TestAPIRankingPlayback(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v1/rankings/playback": apiBody(`{"movies":[{"id":"a1","number":"SSIS-100","title":"甲"},{"id":"a2","number":"SSIS-200","title":"乙"}],"current_page":1}`),
	}))
	c := newAPIClient(t, st)
	r, err := c.Playback(context.Background(), PeriodWeekly, Page{})
	requireNoErr(t, err)
	if r.Kind != RankingPlayback || r.Period != PeriodWeekly || r.Source != "api" {
		t.Fatalf("ranking: %+v", r)
	}
	if len(r.Movies) != 2 || r.Movies[0].Ranking != 1 || r.Movies[1].Ranking != 2 {
		t.Fatalf("ranks: %+v", r.Movies)
	}
	q := st.lastRequest(t, "/api/v1/rankings/playback").Query
	if q.Get("period") != "weekly" || q.Get("filter_by") != "high_score" {
		t.Fatalf("query: %v", q)
	}
}

func TestAPIRankingTop250Paging(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v1/movies/top": apiBody(`{"movies":[{"id":"a1","title":"甲"}],"total_pages":5}`),
	}))
	c := newAPIClient(t, st)
	r, err := c.Top250(context.Background(), Top250OfYear(2025), Page{Page: 2, Limit: 50})
	requireNoErr(t, err)
	if r.Movies[0].Ranking != 51 {
		t.Fatalf("start rank: %+v", r.Movies[0])
	}
	if r.MaxPage != 5 || r.Page != 2 {
		t.Fatalf("paging: %+v", r)
	}
	q := st.lastRequest(t, "/api/v1/movies/top").Query
	if q.Get("type") != "year" || q.Get("type_value") != "2025" || q.Get("start_rank") != "51" {
		t.Fatalf("query: %v", q)
	}
}

func TestAPIRankingActors(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v1/rankings/actors": apiBody(`{"actors":[
		  {"id":"83V","name":"桃園怜奈","name_zht":"桃園憐奈","avatar_url":"https://tp.spfcas.com/rhe951l4q/avatars/83/83V.jpg"},
		  {"id":"BzpA","name":"本庄鈴","name_zht":null,"avatar_url":null},
		  {"id":"Rk9R","name":"水戸かな"}]}`),
	}))
	c := newAPIClient(t, st)

	r, err := c.Ranking(context.Background(), RankingQuery{Kind: RankingActors, Category: CategoryUncensored})
	requireNoErr(t, err)
	if r.Kind != RankingActors || r.Source != "api" || len(r.Actors) != 3 {
		t.Fatalf("ranking: %+v", r)
	}
	if r.Actors[0].ID != "83V" || r.Actors[0].Ranking != 1 || r.Actors[1].Ranking != 2 {
		t.Fatalf("actors: %+v", r.Actors)
	}
	// Actor avatars live on the same CDN prefix and must be normalised.
	if r.Actors[0].AvatarURL != "https://c0.jdbstatic.com/avatars/83/83V.jpg" {
		t.Fatalf("avatar: %q", r.Actors[0].AvatarURL)
	}
	got := st.lastRequest(t, "/api/v1/rankings/actors").Query
	if got.Get("type") != "1" || got.Has("limit") || got.Has("page") {
		t.Fatalf("query: %v", got)
	}

	// The app answers with its whole sheet, so the window is cut locally: rows
	// keep their true rank, and Page/MaxPage describe the window.
	r, err = c.Ranking(context.Background(), RankingQuery{Kind: RankingActors, Page: Page{Page: 2, Limit: 1}})
	requireNoErr(t, err)
	if len(r.Actors) != 1 || r.Actors[0].ID != "BzpA" || r.Actors[0].Ranking != 2 {
		t.Fatalf("window: %+v", r.Actors)
	}
	if r.Page != 2 || r.MaxPage != 3 {
		t.Fatalf("paging: page=%d max=%d", r.Page, r.MaxPage)
	}

	// Beyond the sheet an empty window is honest, an error would be a lie.
	r, err = c.Ranking(context.Background(), RankingQuery{Kind: RankingActors, Page: Page{Page: 9, Limit: 5}})
	requireNoErr(t, err)
	if len(r.Actors) != 0 {
		t.Fatalf("out of range window: %+v", r.Actors)
	}

	// A page without a page size cannot be located inside a single sheet.
	_, err = c.Ranking(context.Background(), RankingQuery{Kind: RankingActors, Page: Page{Page: 2}})
	requireSentinel(t, err, ErrUnsupported)
}

// Per-category movie ranking calls /v1/rankings with type=zone.
func TestAPIRankingMoviesWithCategory(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v1/rankings":          apiBody(`{"movies":[{"id":"a1","title":"甲"},{"id":"a2","title":"乙"}]}`),
		"/api/v1/rankings/playback": apiBody(`{"movies":[{"id":"b1","title":"丙"}]}`),
	}))
	c := newAPIClient(t, st)

	// Per-category movie ranking calls /v1/rankings with type=zone.
	r, err := c.Ranking(context.Background(), RankingQuery{Kind: RankingMovies, Category: CategoryUncensored})
	requireNoErr(t, err)
	if r.Kind != RankingMovies || r.Category != CategoryUncensored || len(r.Movies) != 2 {
		t.Fatalf("ranking: %+v", r)
	}
	req := st.lastRequest(t, "/api/v1/rankings")
	if req.Query.Get("type") != "1" || req.Query.Get("period") != "daily" {
		t.Fatalf("ranking params: %v", req.Query)
	}

	// Without a category it maps onto 热播榜.
	r, err = c.Ranking(context.Background(), RankingQuery{Kind: RankingMovies})
	requireNoErr(t, err)
	if r.Kind != RankingMovies || len(r.Movies) != 1 {
		t.Fatalf("ranking: %+v", r)
	}
	if n := st.count("/api/v1/rankings/playback"); n != 1 {
		t.Fatalf("playback requests: %d", n)
	}
}

func TestAPIMovieDetailAndMagnets(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v4/movies/yxY7kW": apiBody(`{"share_info":"","movie":{
		  "id":"yxY7kW","number":"SSIS-001","title":"標題一","summary":"說明",
		  "duration":"120","score":4.45,"release_date":"2024-05-21",
		  "maker_id":"zKW","maker_name":"片商A","publisher_id":"aB3","publisher_name":"發行B",
		  "series_id":"xY9","series_name":"系列C","director_id":"d1","director_name":"導演D",
		  "reviews_count":25,"comments_count":9,"want_watch_count":1234,"watched_count":567,
		  "preview_video_url":"https://cdn/preview.mp4",
		  "preview_images":[{"thumb_url":"t1","large_url":"https://x/rhe951l4q/p/l1.jpg"}],
		  "tags":[{"id":"15","name":"巨乳"}],
		  "actors":[{"id":"21Jp","name":"楓花戀","gender":0}]}}`),
		"/api/v1/movies/yxY7kW/magnets": apiBody(`{"magnets":[
		  {"name":"SSIS-001 [CNSUB]","hash":"abc123","size":2684354560,"cnsub":true,"hd":false,"files_count":3,"created_at":1700000000,"pikpak_url":""},
		  {"name":"SSIS-001","hash":"def456","size":"1073741824","cnsub":false,"hd":true,"files_count":1,"created_at":"2026-01-02T03:04:05Z"}]}`),
	}))
	c := newAPIClient(t, st)

	d, err := c.Movie(context.Background(), "yxY7kW")
	requireNoErr(t, err)
	if d.Summary != "說明" || d.ReviewsCount != 25 || d.WantCount != 1234 || d.WatchedCount != 567 {
		t.Fatalf("counters: %+v", d)
	}
	if d.Maker == nil || d.Maker.ID != "zKW" || d.Maker.Href != "/makers/zKW" {
		t.Fatalf("maker: %+v", d.Maker)
	}
	if d.Publisher == nil || d.Series == nil || len(d.Directors) != 1 {
		t.Fatalf("credits: %+v", d)
	}
	if d.PreviewVideo != "https://cdn/preview.mp4" {
		t.Fatalf("preview video: %q", d.PreviewVideo)
	}
	if len(d.PreviewImages) != 1 || d.PreviewImages[0] != "https://c0.jdbstatic.com/p/l1.jpg" {
		t.Fatalf("preview images: %v", d.PreviewImages)
	}
	if len(d.ActorCredits) != 1 || d.ActorCredits[0].Gender != "female" || d.ActorCredits[0].ID != "21Jp" {
		t.Fatalf("actor credits: %+v", d.ActorCredits)
	}
	if len(d.Genres) != 1 || d.Genres[0].ID != "15" {
		t.Fatalf("genres: %+v", d.Genres)
	}
	// The client merges the separately fetched magnet list.
	if len(d.Magnets) != 2 || d.MagnetsCount != 2 {
		t.Fatalf("magnets: %+v", d.Magnets)
	}
	if st.count("/api/v1/movies/yxY7kW/magnets") != 1 {
		t.Fatalf("magnet requests: %d", st.count("/api/v1/movies/yxY7kW/magnets"))
	}
	first := d.Magnets[0]
	if first.SizeBytes != 2684354560 || first.SizeText != "2.50 GB" || first.Files != 3 {
		t.Fatalf("magnet sizing: %+v", first)
	}
	if first.CreatedAt != "2023-11-14" {
		t.Fatalf("magnet created_at: %q", first.CreatedAt)
	}
	if len(first.Tags) == 0 || first.Tags[0] != "中字" {
		t.Fatalf("magnet tags: %v", first.Tags)
	}
}

func TestAPIReviews(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v1/movies/yxY7kW/reviews": apiBody(`{"reviews":[
		  {"id":9001,"user_id":12,"username":"網友A","watched_count":30,"status":"watched","status_title":"已看過",
		   "score":"8","content":"不錯","likes_count":5,"liked":false,"created_at":"2026-09-01T10:00:00Z"}],
		  "total":25}`),
	}))
	c := newAPIClient(t, st)
	page, err := c.Reviews(context.Background(), ReviewQuery{MovieID: "yxY7kW", Sort: SortNewest, Page: Page{Page: 1, Limit: 10}})
	requireNoErr(t, err)
	if page.Total != 25 || len(page.Reviews) != 1 {
		t.Fatalf("page: %+v", page)
	}
	r := page.Reviews[0]
	if r.ID != "9001" || r.Author != "網友A" || r.Rating != 8 || r.Date != "2026-09-01" || r.Likes != 5 {
		t.Fatalf("review: %+v", r)
	}
	q := st.lastRequest(t, "/api/v1/movies/yxY7kW/reviews").Query
	if q.Get("sort_by") != "latest" || q.Get("limit") != "10" {
		t.Fatalf("query: %v", q)
	}
}

func TestAPIActorProfileAndTags(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v1/actors/21Jp": apiBody(`{"share_info":"","actor":{
		  "id":"21Jp","type":0,"name":"楓花戀","name_zht":"楓花戀","other_name":"Kaede",
		  "avatar_url":"https://x/rhe951l4q/a/1.jpg","uncensored":false,"gender":0,
		  "birthday":"2000-01-01","height":160,"bust":88,"cup":"E","waist":58,"hips":85,
		  "birthplace":"日本","twitter_id":"kaede","instagram_id":"","videos_count":120},
		  "filter_tags":[{"category":"主題","category_id":"4","tags":[{"id":15,"name":"巨乳"}]}]}`),
		"/api/v1/tags":          apiBody(`{"tags":[{"category":"主題","category_id":"4","tags":[{"id":15,"name":"巨乳"},{"id":151,"name":"制服扮演"}]}]}`),
		"/api/v1/lists/related": apiBody(`{"lists":[{"id":"L1","name":"我的最愛","movies_count":12}]}`),
	}))
	c := newAPIClient(t, st)
	ctx := context.Background()

	a, err := c.Actor(ctx, "21Jp")
	requireNoErr(t, err)
	if a.ID != "21Jp" || a.Name != "楓花戀" || a.Gender != "female" || a.Height != 160 || a.Cup != "E" {
		t.Fatalf("actor: %+v", a)
	}
	if a.AvatarURL != "https://c0.jdbstatic.com/a/1.jpg" || a.VideosCount != 120 || a.Twitter != "kaede" {
		t.Fatalf("actor details: %+v", a)
	}

	groups, err := c.TagGroups(ctx, CategoryCensored)
	requireNoErr(t, err)
	if len(groups) != 1 || groups[0].CategoryID != "4" || len(groups[0].Options) != 2 {
		t.Fatalf("groups: %+v", groups)
	}
	// The app only accepts a numeric actor type; "censored" makes it answer 500.
	if got := st.lastRequest(t, "/api/v1/tags").Query.Get("type"); got != "0" {
		t.Fatalf("tag type: %q", got)
	}

	lists, err := c.RelatedLists(ctx, "yxY7kW", Page{})
	requireNoErr(t, err)
	if len(lists) != 1 || lists[0].Href != "/lists/L1" || lists[0].Kind != "list" {
		t.Fatalf("lists: %+v", lists)
	}
}

func TestAPILoginStoresToken(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v1/sessions":   apiBody(`{"token":"jwt.token.value"}`),
		"/api/v1/movies/top": apiBody(`{"movies":[{"id":"a1","title":"甲"}]}`),
	}))
	c := newAPIClient(t, st)
	ctx := context.Background()

	requireNoErr(t, c.Login(ctx, Credentials{Username: "user@mail.test", Password: "pw"}))
	req := st.lastRequest(t, "/api/v1/sessions")
	if req.Method != http.MethodPost {
		t.Fatalf("method: %s", req.Method)
	}
	if req.Form.Get("username") != "user@mail.test" || req.Form.Get("device_uuid") == "" {
		t.Fatalf("login form params: %v", req.Form)
	}
	if req.Header.Get("Authorization") != "" {
		t.Fatal("login must not send an old token")
	}

	if _, err := c.Top250(ctx, Top250All, Page{}); err != nil {
		t.Fatalf("top250: %v", err)
	}
	if got := st.lastRequest(t, "/api/v1/movies/top").Header.Get("Authorization"); got != "Bearer jwt.token.value" {
		t.Fatalf("authorization: %q", got)
	}
}

func TestAPILoginWithImportedToken(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{}))
	c := newAPIClient(t, st)
	requireNoErr(t, c.Login(context.Background(), Credentials{AppToken: "imported"}))
	if _, appToken := c.t.session(); appToken != "imported" {
		t.Fatalf("app token not stored: %q", appToken)
	}
	if st.total() != 0 {
		t.Fatal("importing a token must not hit the network")
	}
}

func TestAPIDisabledBackendIsNotRegistered(t *testing.T) {
	c, err := New(WithAPIBase(""), WithSites("https://example.invalid"))
	requireNoErr(t, err)
	if got := c.Backends(); len(got) != 1 || got[0] != "web" {
		t.Fatalf("backends: %v", got)
	}
	if c.API() != nil {
		t.Fatal("api backend should be nil when disabled")
	}
	if c.Web() == nil {
		t.Fatal("web backend should be set")
	}
}

func TestAPICategoryTagBrowse(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v1/movies/tags": apiBody(`{"movies":[{"id":"a1","number":"SSIS-531","title":"甲"},{"id":"a2","number":"SSIS-698","title":"乙"}],"current_page":2}`),
	}))
	c := newAPIClient(t, st)
	res, err := c.CategoryMovies(context.Background(), CategoryQuery{
		TagIDs: map[string]string{"subject": "23"},
		Page:   Page{Page: 2, Limit: 20},
	})
	requireNoErr(t, err)
	if res.Source != "api" || len(res.Movies) != 2 || res.Current != 2 {
		t.Fatalf("result: %+v", res)
	}
	// 短页（2 < limit 20）→ 合成 maxPage = 当前页（末页）。
	if res.MaxPage != 2 {
		t.Fatalf("MaxPage = %d, want 2 (short page = last)", res.MaxPage)
	}
	q := st.lastRequest(t, "/api/v1/movies/tags").Query
	if q.Get("filter_by") != "0:t::23::" {
		t.Fatalf("filter_by: %q", q.Get("filter_by"))
	}
	if q.Get("sort_by") != "hit" || q.Get("page") != "2" || q.Get("limit") != "20" {
		t.Fatalf("query: %v", q)
	}
}

// 端点不报 total_pages：满页合成 page+1（大概率还有下一页），短页即末页。
func TestAPICategoryMaxPageSynthesis(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v1/movies/tags": apiBody(`{"movies":[{"id":"a1","number":"SSIS-531","title":"甲"},{"id":"a2","number":"SSIS-698","title":"乙"}],"current_page":3}`),
	}))
	c := newAPIClient(t, st)
	res, err := c.CategoryMovies(context.Background(), CategoryQuery{
		TagIDs: map[string]string{"subject": "23"},
		Page:   Page{Page: 3, Limit: 2},
	})
	requireNoErr(t, err)
	if res.MaxPage != 4 {
		t.Fatalf("full page: MaxPage = %d, want 4 (current+1)", res.MaxPage)
	}
}

// 多个 tag id（values）全部拼接；keys（web 组号）在 app 端被忽略。
func TestAPICategoryTagBrowseMultiTag(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v1/movies/tags": apiBody(`{"movies":[],"total_entries":0}`),
	}))
	c := newAPIClient(t, st)
	_, err := c.CategoryMovies(context.Background(), CategoryQuery{
		TagIDs: map[string]string{"subject": "23", "cloth": "57"},
	})
	if !errors.Is(err, ErrEmptyResult) {
		t.Fatalf("err = %v, want ErrEmptyResult", err)
	}
	q := st.lastRequest(t, "/api/v1/movies/tags").Query
	if q.Get("filter_by") != "0:t::23,57::" {
		t.Fatalf("filter_by: %q", q.Get("filter_by"))
	}
}

func TestAPICategoryActorMask(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v1/movies/tags": apiBody(`{"movies":[{"id":"a1","number":"IPX-811","title":"甲"}],"current_page":1}`),
	}))
	c := newAPIClient(t, st)
	res, err := c.CategoryMovies(context.Background(), CategoryQuery{
		ActorID: "kzx6",
		Page:    Page{Page: 1, Limit: 10},
	})
	requireNoErr(t, err)
	if len(res.Movies) != 1 {
		t.Fatalf("movies: %+v", res.Movies)
	}
	q := st.lastRequest(t, "/api/v1/movies/tags").Query
	if q.Get("filter_by") != "0:a:kzx6" {
		t.Fatalf("filter_by: %q", q.Get("filter_by"))
	}
	if q.Get("sort_by") != "release" {
		t.Fatalf("actor sort: %q", q.Get("sort_by"))
	}
}

// 无 app API 字母的 scoped listing（发行商）仍由 web 后端承担。
func TestAPICategoryUnsupportedScope(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{}))
	c := newAPIClient(t, st)
	_, err := c.CategoryMovies(context.Background(), CategoryQuery{Publisher: "aB3"})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
	if st.total() != 0 {
		t.Fatal("unsupported scope must not hit the network")
	}
}

// 实体 scope 全部走同一端点：字母 a/m/s/d/c；refs 即共享的 web/app 实体 id
// （探测证实 maker ZXX、code SSIS 无需解析），排序固定 release。
func TestAPICategoryEntityScopes(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v1/movies/tags": apiBody(`{"movies":[{"id":"a1","number":"IPZZ-936","title":"甲"}],"current_page":1}`),
	}))
	c := newAPIClient(t, st)
	cases := []struct {
		name string
		q    CategoryQuery
		mask string
	}{
		{"maker", CategoryQuery{Maker: "ZXX"}, "0:m:ZXX"},
		{"series", CategoryQuery{Series: "eAb"}, "0:s:eAb"},
		{"director", CategoryQuery{Director: "65e0"}, "0:d:65e0"},
		{"video code", CategoryQuery{VideoCode: " ssis "}, "0:c:SSIS"},
	}
	for _, tc := range cases {
		res, err := c.CategoryMovies(context.Background(), tc.q)
		requireNoErr(t, err)
		if len(res.Movies) != 1 || res.Movies[0].Code != "IPZZ-936" {
			t.Fatalf("%s: movies %+v", tc.name, res.Movies)
		}
		q := st.lastRequest(t, "/api/v1/movies/tags").Query
		if q.Get("filter_by") != tc.mask {
			t.Fatalf("%s: filter_by = %q, want %q", tc.name, q.Get("filter_by"), tc.mask)
		}
		if q.Get("sort_by") != "release" || q.Get("filter_by_tags") != "" {
			t.Fatalf("%s: query %v", tc.name, q)
		}
	}
}

// main flags：下载=m、中字=c，逗号连接；tag mask 插入第三段，实体 mask 追加
// ":{main}::" 尾段。
func TestAPICategoryMainFlags(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v1/movies/tags": apiBody(`{"movies":[{"id":"a1","number":"SSIS-469","title":"甲"}],"current_page":1}`),
	}))
	c := newAPIClient(t, st)
	cases := []struct {
		name string
		q    CategoryQuery
		mask string
	}{
		{"tags downloadable", CategoryQuery{TagIDs: map[string]string{"subject": "23"}, DownloadableOnly: true}, "0:t:m:23::"},
		{"tags subtitle", CategoryQuery{TagIDs: map[string]string{"subject": "23"}, WithSubtitle: true}, "0:t:c:23::"},
		{"tags both", CategoryQuery{TagIDs: map[string]string{"subject": "23"}, DownloadableOnly: true, WithSubtitle: true}, "0:t:m,c:23::"},
		{"actor downloadable", CategoryQuery{ActorID: "kzx6", DownloadableOnly: true}, "0:a:kzx6:m::"},
		{"maker both", CategoryQuery{Maker: "ZXX", DownloadableOnly: true, WithSubtitle: true}, "0:m:ZXX:m,c::"},
	}
	for _, tc := range cases {
		if _, err := c.CategoryMovies(context.Background(), tc.q); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := st.lastRequest(t, "/api/v1/movies/tags").Query.Get("filter_by"); got != tc.mask {
			t.Fatalf("%s: filter_by = %q, want %q", tc.name, got, tc.mask)
		}
	}
}

// 实体 × 题材组合：叠加过滤走独立的 filter_by_tags 参数。
func TestAPICategoryEntityTagCombo(t *testing.T) {
	st := newStub(t, routerHandler(map[string]string{
		"/api/v1/movies/tags": apiBody(`{"movies":[{"id":"a1","number":"IPOK-032","title":"甲"}],"current_page":1}`),
	}))
	c := newAPIClient(t, st)
	res, err := c.CategoryMovies(context.Background(), CategoryQuery{
		ActorID: "kzx6",
		TagIDs:  map[string]string{"subject": "23", "cloth": "57"},
	})
	requireNoErr(t, err)
	if len(res.Movies) != 1 {
		t.Fatalf("movies: %+v", res.Movies)
	}
	q := st.lastRequest(t, "/api/v1/movies/tags").Query
	if q.Get("filter_by") != "0:a:kzx6" {
		t.Fatalf("filter_by: %q", q.Get("filter_by"))
	}
	if q.Get("filter_by_tags") != "23,57" {
		t.Fatalf("filter_by_tags: %q", q.Get("filter_by_tags"))
	}
	if q.Get("sort_by") != "release" {
		t.Fatalf("sort_by: %q", q.Get("sort_by"))
	}
}
