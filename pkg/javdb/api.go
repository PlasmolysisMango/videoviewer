package javdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// apiBackend talks to the JavDB mobile-app JSON API (jdforrepam.com).
// It is the preferred backend: no Cloudflare interstitial, no HTML drift,
// structured magnets/reviews/top-rankings. Discovered by the reference
// browser extension and reproduced here.
type apiBackend struct {
	t    *transport
	base string
}

func newAPIBackend(t *transport) *apiBackend {
	return &apiBackend{t: t, base: t.opts.APIBase}
}

// Name implements Backend.
func (b *apiBackend) Name() string { return "api" }

func (b *apiBackend) enabled() bool { return b.base != "" }

// getJSON issues a signed GET and decodes the standard envelope.
func (b *apiBackend) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	if !b.enabled() {
		return fmt.Errorf("%w: api backend disabled", ErrUnsupported)
	}
	header := http.Header{}
	header.Set("jdsignature", b.t.sig.Get())
	header.Set("Accept", "application/json")
	header.Set("User-Agent", "Dart/3.5 (dart:io)") // what the Flutter app sends
	body, status, err := b.t.do(ctx, req{
		method: http.MethodGet,
		url:    b.base + path,
		query:  q,
		header: header,
	})
	if err != nil {
		return err
	}
	var env apiEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("javdb: api %s: decode: %w (status %d)", path, err, status)
	}
	if env.Success != 1 {
		return &APIError{Status: status, Code: env.Action, Message: env.Message, Path: path}
	}
	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("javdb: api %s: decode data: %w", path, err)
		}
	}
	return nil
}

// apiEnvelope is the {"success":1,"action":null,"data":{...}} wrapper.
type apiEnvelope struct {
	Success int             `json:"success"`
	Action  string          `json:"action"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// ---------------------------------------------------------------------------
// Search
// ---------------------------------------------------------------------------

type apiSearchData struct {
	Movies       []apiMovie    `json:"movies"`
	Actors       []apiActorDTO `json:"actors"`
	Tags         []apiTagGroup `json:"tags"`
	CurrentPage  int           `json:"current_page"`
	TotalPages   int           `json:"total_pages"`
	TotalCount   int           `json:"total_count"`
	TotalEntries int           `json:"total_entries"`
}

// SearchMovies implements MovieSearcher via GET /v2/search.
func (b *apiBackend) SearchMovies(ctx context.Context, q Query) (*SearchResult, error) {
	keyword := strings.TrimSpace(q.Keyword)
	if keyword == "" {
		return nil, fmt.Errorf("%w: empty keyword", ErrInvalidQuery)
	}
	page := q.pageOrDefault(1)
	if len(q.TagIDs) > 0 {
		// The app search endpoint exposes no tag-filter parameters (only the HTML
		// /tags listing does). Refusing is better than silently dropping the
		// filter and returning unfiltered hits.
		return nil, fmt.Errorf("%w: api tag filters", ErrUnsupported)
	}
	limit := q.limitOrDefault(30)
	params := url.Values{}
	params.Set("q", keyword)
	params.Set("page", strconv.Itoa(page))
	params.Set("limit", strconv.Itoa(limit))
	params.Set("type", string(q.Scope))
	if q.Scope == "" || q.Scope == ScopeMovie {
		params.Set("type", "movie")
		params.Set("movie_type", apiMovieType(q.Category))
		params.Set("movie_filter_by", apiFilterBy(q.Filter, q.WithSubtitle))
		params.Set("movie_sort_by", apiSortBy(q.Sort))
		if q.FromRecent {
			params.Set("from_recent", "true")
		} else {
			params.Set("from_recent", "false")
		}
		if q.Year > 0 {
			params.Set("year", strconv.Itoa(q.Year))
		}
		if q.Month > 0 {
			params.Set("month", strconv.Itoa(q.Month))
		}
	}
	var out apiSearchData
	if err := b.getJSON(ctx, "/v2/search", params, &out); err != nil {
		return nil, err
	}
	res := &SearchResult{Query: q, Current: out.CurrentPage, MaxPage: out.TotalPages, Source: b.Name()}
	for _, m := range out.Movies {
		res.Movies = append(res.Movies, m.toMovie(b.t.Site()))
	}
	for _, a := range out.Actors {
		res.Actors = append(res.Actors, a.toActor(b.t.Site()))
	}
	res.Total = out.TotalCount
	if res.Total == 0 {
		res.Total = out.TotalEntries
	}
	if len(res.Movies) == 0 && len(res.Actors) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrEmptyResult, keyword)
	}
	if res.Current == 0 {
		res.Current = page
	}
	return res, nil
}

// SearchActors implements ActorSearcher via GET /v1/actors.
// NOTE: The JavDB API's /v1/actors endpoint ignores the "search" parameter
// and returns a static list of popular actors. We must filter client-side
// using the same actorMatches logic as the web backend.
func (b *apiBackend) SearchActors(ctx context.Context, q Query) ([]Actor, error) {
	keyword := strings.TrimSpace(q.Keyword)
	if keyword == "" {
		return nil, fmt.Errorf("%w: empty keyword", ErrInvalidQuery)
	}
	want := NormalizeCode(keyword)
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	// Fetch enough pages to find matches (API doesn't filter server-side)
	params := url.Values{}
	params.Set("type", apiActorType(q.Category))
	params.Set("limit", strconv.Itoa(max(limit*3, 60))) // fetch more to filter
	if p := q.pageOrDefault(1); p > 1 {
		params.Set("page", strconv.Itoa(p))
	}
	var out apiSearchData
	if err := b.getJSON(ctx, "/v1/actors", params, &out); err != nil {
		return nil, err
	}
	// Client-side filtering: API ignores search param
	var matches []Actor
	for _, a := range out.Actors {
		actor := a.toActor(b.t.Site())
		if actorMatches(actor, want, keyword) {
			matches = append(matches, actor)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("%w: actor %q", ErrEmptyResult, keyword)
	}
	// Apply limit
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

// ---------------------------------------------------------------------------
// Rankings
// ---------------------------------------------------------------------------

// Ranking implements Ranker.
//
//	playback -> GET /v1/rankings/playback
//	top250   -> GET /v1/movies/top            (needs app login)
//	actors   -> GET /v1/rankings/actors       (type is mandatory)
//	movies   -> GET /v1/rankings              (with type=zone for per-category)
func (b *apiBackend) Ranking(ctx context.Context, q RankingQuery) (*Ranking, error) {
	switch q.Kind {
	case RankingPlayback:
		return b.playback(ctx, q)
	case RankingTop250, "":
		return b.top250(ctx, q)
	case RankingActors:
		return b.actorRanking(ctx, q)
	case RankingMovies:
		if q.Category != "" && q.Category != CategoryAll {
			return b.categoryRanking(ctx, q)
		}
		return b.playback(ctx, q)
	default:
		return nil, fmt.Errorf("%w: api ranking kind %q", ErrUnsupported, q.Kind)
	}
}

// actorRanking reads GET /v1/rankings/actors?type=<0..3>; the endpoint insists on
// a type, ranks the current period and answers its whole sheet (97 rows - page
// and limit are ignored), so the requested window is cut locally.
func (b *apiBackend) actorRanking(ctx context.Context, q RankingQuery) (*Ranking, error) {
	if err := sheetPaging(q); err != nil {
		return nil, err
	}
	params := url.Values{"type": {apiActorType(q.Category)}}
	var out apiSearchData
	if err := b.getJSON(ctx, "/v1/rankings/actors", params, &out); err != nil {
		return nil, err
	}
	all := make([]Actor, 0, len(out.Actors))
	for _, a := range out.Actors {
		actor := a.toActor(b.t.Site())
		actor.Ranking = len(all) + 1
		actor.Source = b.Name()
		all = append(all, actor)
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("%w: api actor ranking", ErrEmptyResult)
	}
	r := &Ranking{
		Kind:     RankingActors,
		Period:   q.Period,
		Category: q.Category,
		Source:   b.Name(),
		Actors:   sheetWindow(all, q),
	}
	r.Page, r.MaxPage = sheetPages(q, len(all))
	return r, nil
}

func (b *apiBackend) playback(ctx context.Context, q RankingQuery) (*Ranking, error) {
	if err := sheetPaging(q); err != nil {
		return nil, err
	}
	period := q.Period
	if period == "" {
		period = PeriodDaily
	}
	params := url.Values{
		"period":    {string(period)},
		"filter_by": {orDefault(string(q.Filter), "high_score")},
	}
	var out apiSearchData
	if err := b.getJSON(ctx, "/v1/rankings/playback", params, &out); err != nil {
		return nil, err
	}
	r := b.rankingFromMovies(q, out)
	if len(r.Movies) == 0 {
		return nil, fmt.Errorf("%w: playback %s", ErrEmptyResult, period)
	}
	for i := range r.Movies {
		r.Movies[i].Ranking = i + 1
		r.Movies[i].Page = 1
	}
	total := len(r.Movies)
	r.Movies = sheetWindow(r.Movies, q)
	r.Page, r.MaxPage = sheetPages(q, total)
	return r, nil
}

// categoryRanking reads GET /v1/rankings?type=<zone>&period=<period>.
// This is the per-category movie ranking endpoint discovered in javdb-cli.
func (b *apiBackend) categoryRanking(ctx context.Context, q RankingQuery) (*Ranking, error) {
	if err := sheetPaging(q); err != nil {
		return nil, err
	}
	period := q.Period
	if period == "" {
		period = PeriodDaily
	}
	zone := apiZoneType(q.Category)
	if zone == "" {
		return nil, fmt.Errorf("%w: api movies ranking category %q", ErrUnsupported, q.Category)
	}
	params := url.Values{
		"type":   {zone},
		"period": {string(period)},
	}
	var out apiSearchData
	if err := b.getJSON(ctx, "/v1/rankings", params, &out); err != nil {
		return nil, err
	}
	r := b.rankingFromMovies(q, out)
	if len(r.Movies) == 0 {
		return nil, fmt.Errorf("%w: movies ranking %s %s", ErrEmptyResult, q.Category, period)
	}
	for i := range r.Movies {
		r.Movies[i].Ranking = i + 1
		r.Movies[i].Page = 1
	}
	total := len(r.Movies)
	r.Movies = sheetWindow(r.Movies, q)
	r.Page, r.MaxPage = sheetPages(q, total)
	return r, nil
}

// sheetPaging rejects a page beyond the first when no page size was requested:
// on a whole-sheet endpoint there is no way to know where such a window starts.
func sheetPaging(q RankingQuery) error {
	if q.Page.Page > 1 && q.Limit <= 0 {
		return fmt.Errorf("%w: api ranking page %d needs an explicit limit (the endpoint answers one sheet)", ErrUnsupported, q.Page.Page)
	}
	return nil
}

// sheetWindow cuts the caller's page out of a sheet the endpoint returns whole.
func sheetWindow[T any](items []T, q RankingQuery) []T {
	if q.Limit <= 0 {
		return items
	}
	start := (q.pageOrDefault(1) - 1) * q.Limit
	if start >= len(items) {
		return nil
	}
	return items[start:min(start+q.Limit, len(items))]
}

// sheetPages reports the window of a whole-sheet endpoint.
func sheetPages(q RankingQuery, total int) (page, maxPage int) {
	page = q.pageOrDefault(1)
	if q.Limit <= 0 {
		return page, 1
	}
	return page, (total + q.Limit - 1) / q.Limit
}

func (b *apiBackend) top250(ctx context.Context, q RankingQuery) (*Ranking, error) {
	slice := q.Slice
	if slice.Type == "" {
		slice = Top250All
	}
	page := q.pageOrDefault(1)
	startRank := q.StartRank
	if startRank <= 0 {
		startRank = 1 + (page-1)*q.limitOrDefault(50)
	}
	params := url.Values{
		"start_rank":     {strconv.Itoa(startRank)},
		"type":           {slice.Type},
		"type_value":     {slice.Value},
		"ignore_watched": {"false"},
		"page":           {strconv.Itoa(page)},
		"limit":          {strconv.Itoa(q.limitOrDefault(50))},
	}
	var out apiSearchData
	if err := b.getJSON(ctx, "/v1/movies/top", params, &out); err != nil {
		return nil, err
	}
	r := b.rankingFromMovies(q, out)
	for i := range r.Movies {
		r.Movies[i].Ranking = startRank + i
		r.Movies[i].Page = page
	}
	r.Page = page
	r.MaxPage = out.TotalPages
	return r, nil
}

func (b *apiBackend) rankingFromMovies(q RankingQuery, out apiSearchData) *Ranking {
	kind := q.Kind
	if kind == "" {
		kind = RankingTop250
	}
	r := &Ranking{
		Kind:     kind,
		Period:   q.Period,
		Category: q.Category,
		Page:     q.pageOrDefault(1),
		Source:   b.Name(),
	}
	for _, m := range out.Movies {
		r.Movies = append(r.Movies, m.toMovie(b.t.Site()))
	}
	return r
}

// ---------------------------------------------------------------------------
// Detail / magnets / reviews
// ---------------------------------------------------------------------------

type apiDetailData struct {
	ShareInfo string      `json:"share_info"`
	Movie     apiMovieDTO `json:"movie"`
}

// MovieDetail implements Detailer via GET /v4/movies/{id}.
func (b *apiBackend) MovieDetail(ctx context.Context, id string) (*Detail, error) {
	var out apiDetailData
	if err := b.getJSON(ctx, "/v4/movies/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	d := out.Movie.toDetail(b.t.Site())
	return d, nil
}

type apiMagnetData struct {
	Magnets []apiMagnet `json:"magnets"`
}

// Magnets implements MagnetLister via GET /v1/movies/{id}/magnets.
func (b *apiBackend) Magnets(ctx context.Context, movieID string) ([]Magnet, error) {
	var out apiMagnetData
	if err := b.getJSON(ctx, "/v1/movies/"+url.PathEscape(movieID)+"/magnets", nil, &out); err != nil {
		return nil, err
	}
	magnets := make([]Magnet, 0, len(out.Magnets))
	for _, m := range out.Magnets {
		magnets = append(magnets, m.toMagnet())
	}
	return magnets, nil
}

type apiReviewData struct {
	Reviews []apiReview `json:"reviews"`
	Total   int         `json:"total"`
}

// Reviews implements ReviewLister via GET /v1/movies/{id}/reviews.
func (b *apiBackend) Reviews(ctx context.Context, q ReviewQuery) (*ReviewPage, error) {
	if q.MovieID == "" {
		return nil, fmt.Errorf("%w: empty movie id", ErrInvalidQuery)
	}
	params := url.Values{
		"page":    {strconv.Itoa(q.pageOrDefault(1))},
		"limit":   {strconv.Itoa(q.limitOrDefault(20))},
		"sort_by": {apiReviewSort(q.Sort)},
	}
	if q.Filter != "" {
		params.Set("filter_by", string(q.Filter))
	}
	var out apiReviewData
	if err := b.getJSON(ctx, "/v1/movies/"+url.PathEscape(q.MovieID)+"/reviews", params, &out); err != nil {
		return nil, err
	}
	page := &ReviewPage{Current: q.pageOrDefault(1), Total: out.Total, Source: b.Name()}
	for _, r := range out.Reviews {
		page.Reviews = append(page.Reviews, r.toReview())
	}
	return page, nil
}

// ---------------------------------------------------------------------------
// Actors / related lists / tags
// ---------------------------------------------------------------------------

type apiActorResponse struct {
	ShareInfo  string        `json:"share_info"`
	Actor      apiActorDTO   `json:"actor"`
	FilterTags []apiTagGroup `json:"filter_tags"`
	Movies     []apiMovie    `json:"movies"`
}

// Actor implements ActorDetailer via GET /v1/actors/{id}.
func (b *apiBackend) Actor(ctx context.Context, id string) (*Actor, error) {
	var out apiActorResponse
	if err := b.getJSON(ctx, "/v1/actors/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	if out.Actor.ID == "" {
		return nil, fmt.Errorf("%w: actor %s", ErrNotFound, id)
	}
	a := out.Actor.toActor(b.t.Site())
	return &a, nil
}

// ActorMovies is not exposed by the app API; the HTML backend covers it.
func (b *apiBackend) ActorMovies(ctx context.Context, actorID string, p Page) (*SearchResult, error) {
	return nil, fmt.Errorf("%w: api actor filmography", ErrUnsupported)
}

// RelatedLists lists community lists that contain the movie
// (GET /v1/lists/related) — extra capability, no interface of its own.
func (b *apiBackend) RelatedLists(ctx context.Context, movieID string, p Page) ([]Link, error) {
	params := url.Values{
		"movie_id": {movieID},
		"page":     {strconv.Itoa(p.pageOrDefault(1))},
		"limit":    {strconv.Itoa(p.limitOrDefault(20))},
	}
	var out struct {
		Lists []struct {
			ID               string `json:"id"`
			Name             string `json:"name"`
			MoviesCount      int    `json:"movies_count"`
			CollectionsCount int    `json:"collections_count"`
			ViewsCount       int    `json:"views_count"`
			CreatedAt        string `json:"created_at"`
		} `json:"lists"`
	}
	if err := b.getJSON(ctx, "/v1/lists/related", params, &out); err != nil {
		return nil, err
	}
	lists := make([]Link, 0, len(out.Lists))
	for _, l := range out.Lists {
		lists = append(lists, Link{Name: l.Name, ID: l.ID, Href: "/lists/" + l.ID, Kind: "list"})
	}
	return lists, nil
}

// CategoryMovies implements ListPager for tag browsing and entity filmography
// (actor, maker, series, director, video code) via GET /v1/movies/tags — the
// endpoint behind the official app's 类别 tab, discovered in the reference
// javdb-cli and verified live. The filter_by mask is
// "{zone}:t:{main}:{tagIds}::" for tag browsing and "{zone}:{letter}:{id}"
// (plus a ":{main}::" tail when main flags apply) for entities, with letters
// a/m/s/d/c; the refs are the shared web/app entity ids, so no name resolution
// is attempted here. Publishers have no app-API letter and stay web-only via
// ErrUnsupported, like bucket listings; the caller then falls back to the HTML
// backend. DownloadableOnly and WithSubtitle map to the main flags m/c (live
// probes: every returned row had magnets_count>0 resp. has_cnsub=true). An
// entity query combined with TagIDs narrows via the separate filter_by_tags
// parameter. An empty result set is reported as ErrEmptyResult so the caller
// knows the filter truly matched nothing.
func (b *apiBackend) CategoryMovies(ctx context.Context, q CategoryQuery) (*SearchResult, error) {
	zone := apiZoneType(q.Category)
	if zone == "" {
		return nil, fmt.Errorf("%w: api category %q", ErrUnsupported, q.Category)
	}
	letter, ref, hasEntity := apiCategoryEntity(q)
	// App tag ids are globally unique (no web c{N} group coordinate needed);
	// join every value — keys are ignored. Sorting keeps the mask independent
	// of map iteration order.
	tagIDs := make([]string, 0, len(q.TagIDs))
	for _, id := range q.TagIDs {
		if id = strings.TrimSpace(id); id != "" {
			tagIDs = append(tagIDs, id)
		}
	}
	slices.Sort(tagIDs)
	if !hasEntity && len(q.TagIDs) > 0 && len(tagIDs) == 0 {
		return nil, fmt.Errorf("%w: api category: empty tag ids", ErrInvalidQuery)
	}
	main := apiMainFlags(q)
	var mask, sortBy string
	switch {
	case hasEntity:
		mask = fmt.Sprintf("%s:%s:%s", zone, letter, ref)
		if main != "" {
			mask += ":" + main + "::"
		}
		sortBy = "release"
	case len(tagIDs) > 0:
		mask = fmt.Sprintf("%s:t:%s:%s::", zone, main, strings.Join(tagIDs, ","))
		sortBy = "hit"
	default:
		return nil, fmt.Errorf("%w: api category listing (tags/entity only)", ErrUnsupported)
	}
	// 通用排序值映射到端点的 sort_by/order_by；空值维持端点默认。
	order := "desc"
	if q.SortBy != "" {
		sortBy, order = apiCategorySort(q.SortBy, sortBy)
	}
	params := url.Values{
		"filter_by": {mask},
		"sort_by":   {sortBy},
		"order_by":  {order},
		"page":      {strconv.Itoa(q.pageOrDefault(1))},
		"limit":     {strconv.Itoa(q.limitOrDefault(20))},
	}
	if hasEntity && len(tagIDs) > 0 {
		// Entity × tag rides the separate filter_by_tags parameter (verified
		// live: 0:a:kzx6 + filter_by_tags=23 narrows the filmography).
		params.Set("filter_by_tags", strings.Join(tagIDs, ","))
	}
	var out apiSearchData
	if err := b.getJSON(ctx, "/v1/movies/tags", params, &out); err != nil {
		return nil, err
	}
	res := &SearchResult{
		Query:   Query{Category: q.Category, TagIDs: q.TagIDs, Page: q.Page},
		Current: out.CurrentPage,
		MaxPage: out.TotalPages,
		Total:   out.TotalCount,
		Source:  b.Name(),
	}
	if res.Total == 0 {
		res.Total = out.TotalEntries
	}
	for _, m := range out.Movies {
		res.Movies = append(res.Movies, m.toMovie(b.t.Site()))
	}
	if res.Current == 0 {
		res.Current = q.pageOrDefault(1)
	}
	// The endpoint reports neither total_pages nor total_count; synthesise a
	// "has next" bound so the app pager stays usable: a full page implies a
	// likely next page, a short page is the last one.
	if res.MaxPage == 0 && len(res.Movies) > 0 {
		if len(res.Movies) >= q.limitOrDefault(20) {
			res.MaxPage = res.Current + 1
		} else {
			res.MaxPage = res.Current
		}
	}
	if len(res.Movies) == 0 && res.Total == 0 {
		return nil, fmt.Errorf("%w: api tag browse %v", ErrEmptyResult, q.TagIDs)
	}
	return res, nil
}

// apiCategorySort 将通用排序值映射到 /v1/movies/tags 的 sort_by/order_by。
// fallback 是该查询形态的端点默认值（实体 release / 标签 hit）；
// 未识别的排序值回退 fallback，由调用方客户端排序兜底。
// 上游实测（2026-09，演员/系列实体页）：支持 release（±asc/desc）、
// score（方向固定 desc，asc 被忽略）、hit；comments/likes/dd 等取值
// 均被忽略回默认。因此 SortLowest（score asc）与 SortMostComments
// 无可靠服务端排序，回退端点默认，由调用方客户端兜底。
func apiCategorySort(s SortBy, fallback string) (sortBy, order string) {
	order = "desc"
	switch s {
	case SortNewest:
		return "release", "desc"
	case SortOldest:
		return "release", "asc"
	case SortHighest:
		return "score", "desc"
	case SortMostPlayed, SortMostWatched:
		return "hit", "desc"
	default:
		// 实验用：raw:<sortBy>[:<order>] 直接透传给上游，便于校准映射。
		if raw, ok := strings.CutPrefix(string(s), "raw:"); ok {
			parts := strings.Split(raw, ":")
			if len(parts) == 2 {
				return parts[0], parts[1]
			}
			return raw, order
		}
		return fallback, order
	}
}

// apiCategoryEntity maps an entity scope onto its filter_by letter
// (a=actor, m=maker, s=series, d=director, c=video code). The refs are the
// same entity ids the web slugs use (live-verified: actor kzx6, maker ZXX,
// code SSIS), so the value is passed through untouched; publishers have no
// app-API letter. ok=false marks a non-entity query.
func apiCategoryEntity(q CategoryQuery) (letter, ref string, ok bool) {
	switch {
	case q.ActorID != "":
		return "a", url.PathEscape(q.ActorID), true
	case q.Maker != "":
		return "m", url.PathEscape(q.Maker), true
	case q.Series != "":
		return "s", url.PathEscape(q.Series), true
	case q.Director != "":
		return "d", url.PathEscape(q.Director), true
	case q.VideoCode != "":
		return "c", url.PathEscape(NormalizeCode(q.VideoCode)), true
	default:
		return "", "", false
	}
}

// apiMainFlags renders the filter_by main slot from the query flags:
// m=downloadable (magnets), c=Chinese subtitles, s=solo works (actor page's
// 「單體作品」), comma-joined. The letters were verified live; p (playable)
// exists but no query flag maps to it.
func apiMainFlags(q CategoryQuery) string {
	var flags []string
	if q.DownloadableOnly {
		flags = append(flags, "m")
	}
	if q.WithSubtitle {
		flags = append(flags, "c")
	}
	if q.SoloOnly {
		flags = append(flags, "s")
	}
	return strings.Join(flags, ",")
}

// TagGroups implements TagProvider via GET /v1/tags.
func (b *apiBackend) TagGroups(ctx context.Context, scope Category) ([]TagGroup, error) {
	// The endpoint answers with {category, category_id, tags[]}; the category
	// list contains 基本 (search flags) and 年份 (release years), which are the
	// parameters the app search actually accepts.
	if scope == "" {
		scope = CategoryCensored
	}
	params := url.Values{"type": {apiActorType(scope)}}
	var out struct {
		Tags []apiTagGroup `json:"tags"`
	}
	if err := b.getJSON(ctx, "/v1/tags", params, &out); err != nil {
		return nil, err
	}
	groups := make([]TagGroup, 0, len(out.Tags))
	for _, g := range out.Tags {
		groups = append(groups, g.toGroup())
	}
	return groups, nil
}

// Login implements Authenticator: POST /v1/sessions exchanges credentials for
// the app JWT used by TOP250 and personalised endpoints.
func (b *apiBackend) Login(ctx context.Context, cred Credentials) error {
	if cred.AppToken != "" {
		b.t.setAppToken(cred.AppToken)
		return nil
	}
	if cred.Username == "" || cred.Password == "" {
		return fmt.Errorf("%w: username and password required", ErrInvalidQuery)
	}
	// Match the reference javdb-cli: send username/password as form body.
	// device_* and platform params are required by the API.
	form := url.Values{}
	form.Set("username", cred.Username)
	form.Set("password", cred.Password)
	form.Set("device_uuid", "04b9534d-5118-53de-9f87-2ddded77111e")
	form.Set("device_name", "iPhone")
	form.Set("device_model", "iPhone")
	form.Set("platform", "ios")
	form.Set("system_version", "17.4")
	form.Set("app_version", "official")
	form.Set("app_version_number", "1.9.29")
	form.Set("app_channel", "official")

	header := http.Header{}
	header.Set("jdsignature", b.t.sig.Get())
	header.Set("Content-Type", "application/x-www-form-urlencoded")
	header.Set("User-Agent", "Dart/3.5 (dart:io)")

	body, status, err := b.t.do(ctx, req{
		method: http.MethodPost,
		url:    b.base + "/v1/sessions",
		header: header,
		body:   []byte(form.Encode()),
		noAuth: true,
	})
	if err != nil {
		return err
	}
	var env apiEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("javdb: api login: decode: %w", err)
	}
	if env.Success != 1 {
		return &APIError{Status: status, Code: env.Action, Message: env.Message, Path: "/v1/sessions"}
	}
	var data struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil || data.Token == "" {
		return fmt.Errorf("javdb: api login: no token in response")
	}
	b.t.setAppToken(data.Token)
	return nil
}

// ---------------------------------------------------------------------------
// parameter translation
// ---------------------------------------------------------------------------

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func apiMovieType(c Category) string {
	switch c {
	case "":
		return "all"
	default:
		return string(c)
	}
}

// apiActorType maps a category onto the numeric bucket of /v1/actors.
func apiActorType(c Category) string {
	switch c {
	case CategoryUncensored:
		return "1"
	case CategoryWestern:
		return "2"
	case CategoryAmateur:
		return "3"
	default:
		return "0"
	}
}

// apiZoneType maps a category onto the numeric zone of /v1/rankings.
// Returns empty string for unsupported categories.
func apiZoneType(c Category) string {
	switch c {
	case CategoryCensored, "":
		return "0"
	case CategoryUncensored:
		return "1"
	case CategoryWestern:
		return "2"
	case CategoryFC2:
		return "3"
	default:
		return ""
	}
}

func apiFilterBy(f FilterBy, withSub bool) string {
	if withSub {
		return string(FilterWithSub)
	}
	if f == "" {
		return string(FilterAll)
	}
	return string(f)
}

func apiSortBy(s SortBy) string {
	switch s {
	case "":
		return string(SortRelevance)
	default:
		return string(s)
	}
}

func apiReviewSort(s SortBy) string {
	switch s {
	case SortNewest:
		return "latest"
	case "", SortRelevance, "hot":
		return "hotly"
	default:
		return string(s)
	}
}
