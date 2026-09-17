package javdb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// webBackend scrapes the JavDB website HTML. It is the reference project's
// approach (JAVDB_AutoSpider) and covers what the app API does not:
// per-category rankings, category/maker/series/video-code listings and
// actor filmographies. Requests fail over across Options.Sites because
// javdb.com itself is Cloudflare-fronted while the numeric mirrors are not.
type webBackend struct {
	t *transport
}

func newWebBackend(t *transport) *webBackend { return &webBackend{t: t} }

// Name implements Backend.
func (b *webBackend) Name() string { return "web" }

// Site returns the currently preferred mirror.
func (b *webBackend) Site() string { return b.t.Site() }

// getHTML fetches a site path, walking the configured mirrors until one works.
// Every selector/detour problem surfaces as a classified error so the Client
// can decide whether falling back to another backend makes sense.
func (b *webBackend) getHTML(ctx context.Context, path string, q url.Values) (*goquery.Document, string, error) {
	sites := b.t.opts.Sites
	if len(sites) == 0 {
		return nil, "", ErrNoSite
	}
	var lastErr error
	for _, site := range sites {
		body, _, err := b.t.do(ctx, req{
			method: http.MethodGet,
			url:    site + ensureLeading(path),
			query:  q,
		})
		if err != nil {
			// Auth walls and 404s are answer-shaped: don't retry on other mirrors.
			if errors.Is(err, ErrAuthRequired) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidQuery) {
				return nil, site, err
			}
			lastErr = &SiteError{Site: site, Err: err}
			b.t.opts.Logger.Warn("javdb web mirror failed", "site", site, "path", path, "err", err)
			continue
		}
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
		if err != nil {
			lastErr = &SiteError{Site: site, Err: fmt.Errorf("parse %s: %w", path, err)}
			continue
		}
		if msg := pageBlockedMessage(doc); msg != "" {
			return nil, site, fmt.Errorf("%w: %s (%s)", ErrAuthRequired, msg, site)
		}
		return doc, site, nil
	}
	if lastErr == nil {
		lastErr = ErrNoSite
	}
	return nil, "", lastErr
}

// pageBlockedMessage reports an in-page gate that survived status inspection:
// login walls and the 18+ confirmation shell page both mean the session is
// missing/unusable, and both surface as ErrAuthRequired instead of empty data.
func pageBlockedMessage(doc *goquery.Document) string {
	var msg string
	doc.Find("div.empty-message, div#require-login, div.login-required").Each(func(_ int, s *goquery.Selection) {
		t := strings.TrimSpace(s.Text())
		if t != "" && msg == "" {
			msg = t
		}
	})
	if msg != "" && (strings.Contains(msg, "登入") || strings.Contains(msg, "登录") ||
		strings.Contains(msg, "Login") || strings.Contains(msg, "18")) {
		return msg
	}
	return ""
}

// ---------------------------------------------------------------------------
// Search
// ---------------------------------------------------------------------------

// SearchMovies implements MovieSearcher via /search.
func (b *webBackend) SearchMovies(ctx context.Context, q Query) (*SearchResult, error) {
	keyword := strings.TrimSpace(q.Keyword)
	if keyword == "" {
		return nil, fmt.Errorf("%w: empty keyword", ErrInvalidQuery)
	}
	if len(q.TagIDs) > 0 {
		// /search has no tag parameters either; use CategoryMovies with TagIDs
		// (which hits /tags) for tag-scoped browsing.
		return nil, fmt.Errorf("%w: web search tag filters", ErrUnsupported)
	}
	params := url.Values{}
	params.Set("q", keyword)
	params.Set("f", webSearchFilter(q))
	// The anonymous site only exposes a release-date toggle (sb=1); the other
	// sorts are app-API-only and are therefore applied client-side upstream.
	if q.Sort == SortNewest || q.Sort == SortOldest {
		params.Set("sb", "1")
	}
	if q.FromRecent {
		params.Set("from_recent", "1")
	}
	if q.Year > 0 {
		params.Set("year", strconv.Itoa(q.Year))
	}
	if q.Month > 0 {
		params.Set("month", strconv.Itoa(q.Month))
	}
	page := q.pageOrDefault(1)
	if page > 1 {
		params.Set("page", strconv.Itoa(page))
	}
	doc, _, err := b.getHTML(ctx, "/search", params)
	if err != nil {
		return nil, err
	}
	res := &SearchResult{Query: q, Current: page, Source: b.Name()}
	res.Movies = parseMovieList(doc, page)
	cur, maxPage := parsePagination(doc)
	if cur > 0 {
		res.Current = cur
	}
	res.MaxPage = maxPage
	if len(res.Movies) == 0 {
		return nil, fmt.Errorf("%w: %q on web", ErrEmptyResult, keyword)
	}
	for i := range res.Movies {
		res.Movies[i].Source = b.Name()
		if res.Movies[i].CoverURL == "" {
			res.Movies[i].CoverURL = FixImageURL(res.Movies[i].ThumbURL)
		}
	}
	return res, nil
}

// webSearchFilter translates a Query onto the site's "f" parameter.
// Observed values: all, code, actor, series, maker, director, list, cnsub,
// download, playable, preview, single, m.
func webSearchFilter(q Query) string {
	if q.WithSubtitle {
		return "cnsub"
	}
	switch q.Filter {
	case FilterWithSub:
		return "cnsub"
	case FilterPlayable:
		return "playable"
	case FilterDownloaded:
		return "download"
	case FilterSingle:
		return "single"
	}
	switch q.Scope {
	case ScopeActor:
		return "actor"
	case ScopeTag:
		return "list"
	}
	switch q.Category {
	case CategoryCensored:
		return "censored"
	case CategoryUncensored:
		return "uncensored"
	case CategoryWestern:
		return "western"
	case CategoryFC2:
		return "fc2"
	}
	if q.Filter == "" {
		return "m"
	}
	return string(q.Filter)
}

// SearchActors implements ActorSearcher.
// The keyword search (/search?f=actor) works anonymously and covers every
// actor including aliases (e.g. "清原美優" hits 清原みゆう), so it goes first;
// the popular-actors directory (/actors/...) is only a best-effort fallback
// for when the search page is unavailable. The Client prefers the API and
// also retries with a simplified->traditional rewrite of the keyword.
func (b *webBackend) SearchActors(ctx context.Context, q Query) ([]Actor, error) {
	keyword := strings.TrimSpace(q.Keyword)
	if keyword == "" {
		return nil, fmt.Errorf("%w: empty keyword", ErrInvalidQuery)
	}
	if matches, err := b.searchActorsKeyword(ctx, keyword, NormalizeCode(keyword), q); err == nil && len(matches) > 0 {
		return matches, nil
	}
	// Fallback: browse the public actor directory and filter client-side.
	path := "/actors"
	if q.Category != "" && q.Category != CategoryAll {
		path = "/actors/" + string(q.Category)
	}
	want := NormalizeCode(keyword)
	maxPages := maxInt(1, q.limitOrDefault(3))
	var matches []Actor
	var firstErr error
	for page := q.pageOrDefault(1); page <= maxPages; page++ {
		params := url.Values{}
		if page > 1 {
			params.Set("page", strconv.Itoa(page))
		}
		doc, _, err := b.getHTML(ctx, path, params)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		for _, a := range parseActorBoxes(doc) {
			if actorMatches(a, want, keyword) {
				a.Source = b.Name()
				matches = append(matches, a)
			}
		}
		if !pageHasNext(doc) {
			break
		}
	}
	if len(matches) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, fmt.Errorf("%w: actor %q on web", ErrEmptyResult, keyword)
	}
	return matches, nil
}

// searchActorsKeyword scrapes the anonymous actor keyword search
// (/search?q={kw}&f=actor); result cards carry aliases in their title attr.
func (b *webBackend) searchActorsKeyword(ctx context.Context, keyword, want string, q Query) ([]Actor, error) {
	maxPages := maxInt(1, q.limitOrDefault(3))
	var matches []Actor
	var firstErr error
	for page := q.pageOrDefault(1); page <= maxPages; page++ {
		params := url.Values{"q": {keyword}, "f": {"actor"}}
		if page > 1 {
			params.Set("page", strconv.Itoa(page))
		}
		doc, _, err := b.getHTML(ctx, "/search", params)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		for _, a := range parseActorBoxes(doc) {
			if actorMatches(a, want, keyword) {
				a.Source = b.Name()
				matches = append(matches, a)
			}
		}
		if !pageHasNext(doc) {
			break
		}
	}
	if len(matches) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, fmt.Errorf("%w: actor %q via web search", ErrEmptyResult, keyword)
	}
	return matches, nil
}

func pageHasNext(doc *goquery.Document) bool {
	_, hasNext := doc.Find("a.pagination-next").Attr("href")
	return hasNext
}

func actorMatches(a Actor, normalized, raw string) bool {
	// a.ID lets an id-miss fallback (Actor by id -> name search) still find
	// the actor when the id itself is what was searched.
	for _, cand := range []string{a.ID, a.Name, a.NameTraditional, a.OtherName} {
		if cand == "" {
			continue
		}
		if strings.Contains(NormalizeCode(cand), normalized) || strings.Contains(cand, raw) {
			return true
		}
	}
	return false
}

// pageScraped is kept for tests: it asserts a listing page was fully walked.
func pageScraped(doc *goquery.Document, page int) bool {
	cur, _ := parsePagination(doc)
	return !pageHasNext(doc) && cur <= page
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// Rankings
// ---------------------------------------------------------------------------

// Ranking implements Ranker against the site's /rankings pages.
func (b *webBackend) Ranking(ctx context.Context, q RankingQuery) (*Ranking, error) {
	kind := q.Kind
	if kind == "" {
		kind = RankingMovies
	}
	path, params := webRankingURL(q, kind)
	if path == "" {
		return nil, fmt.Errorf("%w: web ranking kind %q", ErrUnsupported, kind)
	}
	page := q.pageOrDefault(1)
	if page > 1 {
		params.Set("page", strconv.Itoa(page))
	}
	doc, _, err := b.getHTML(ctx, path, params)
	if err != nil {
		return nil, err
	}
	r := &Ranking{Kind: kind, Period: q.Period, Category: q.Category, Page: page, Source: b.Name()}
	r.Title = strings.TrimSpace(doc.Find("title").First().Text())

	if kind == RankingActors {
		r.Actors = parseActorBoxes(doc)
		for i := range r.Actors {
			r.Actors[i].Ranking = i + 1 + (page-1)*len(r.Actors)
			r.Actors[i].Source = b.Name()
		}
		if len(r.Actors) == 0 {
			return nil, fmt.Errorf("%w: actor ranking on web", ErrEmptyResult)
		}
		return r, nil
	}

	r.Movies = parseMovieList(doc, page)
	if len(r.Movies) == 0 {
		return nil, fmt.Errorf("%w: %s ranking on web", ErrEmptyResult, kind)
	}
	// TOP/ranking pages print a numeric badge; when absent, derive positions
	// from document order so callers always get a stable rank.
	ranked := true
	for i := range r.Movies {
		r.Movies[i].Source = b.Name()
		if r.Movies[i].Ranking == 0 {
			ranked = false
		}
		r.Movies[i].Page = page
	}
	if !ranked {
		for i := range r.Movies {
			r.Movies[i].Ranking = i + 1 + (page-1)*len(r.Movies)
		}
	}
	if r.Period == "" {
		r.Period = ParsePeriod(params.Encode())
	}
	_, r.MaxPage = parsePagination(doc)
	return r, nil
}

func webRankingURL(q RankingQuery, kind RankingKind) (string, url.Values) {
	params := url.Values{}
	switch kind {
	case RankingMovies:
		if p := q.Period; p != "" {
			params.Set("p", string(p))
		}
		if c := q.Category; c != "" && c != CategoryAll {
			params.Set("t", string(c))
		}
		return "/rankings/movies", params
	case RankingPlayback:
		if p := q.Period; p != "" {
			params.Set("p", string(p))
		}
		return "/rankings/playback", params
	case RankingTop250:
		slice := q.Slice
		switch slice.Type {
		case "year":
			params.Set("t", "y"+slice.Value)
		case "video_type":
			params.Set("t", map[string]string{"0": "censored", "1": "uncensored", "2": "western", "3": "fc2"}[slice.Value])
		}
		return "/rankings/top", params
	case RankingActors:
		if c := q.Category; c != "" && c != CategoryAll {
			params.Set("t", string(c))
		}
		return "/rankings/actors", params
	case RankingFanzaAward:
		return "/rankings/fanza_award", params
	default:
		return "", nil
	}
}

// ---------------------------------------------------------------------------
// Category listings
// ---------------------------------------------------------------------------

// CategoryMovies implements ListPager.
func (b *webBackend) CategoryMovies(ctx context.Context, q CategoryQuery) (*SearchResult, error) {
	path, params := webCategoryURL(q)
	page := q.pageOrDefault(1)
	if page > 1 {
		params.Set("page", strconv.Itoa(page))
	}
	doc, _, err := b.getHTML(ctx, path, params)
	if err != nil {
		return nil, err
	}
	res := &SearchResult{
		Query:   Query{Keyword: path, Scope: ScopeMovie, Category: q.Category, Page: q.Page},
		Movies:  parseMovieList(doc, page),
		Current: page,
		Source:  b.Name(),
	}
	for i := range res.Movies {
		res.Movies[i].Source = b.Name()
	}
	_, res.MaxPage = parsePagination(doc)
	if len(res.Movies) == 0 {
		return nil, fmt.Errorf("%w: category %s", ErrEmptyResult, path)
	}
	return res, nil
}

func webCategoryURL(q CategoryQuery) (string, url.Values) {
	params := url.Values{}
	switch {
	case q.ActorID != "":
		params.Set("t", "d") // only entries with torrents, matching the site default
		return "/actors/" + url.PathEscape(q.ActorID), params
	case q.VideoCode != "":
		params.Set("f", "download")
		return "/video_codes/" + url.PathEscape(NormalizeCode(q.VideoCode)), params
	case q.Maker != "":
		params.Set("f", "download")
		return "/makers/" + url.PathEscape(q.Maker), params
	case q.Publisher != "":
		params.Set("f", "download")
		return "/publishers/" + url.PathEscape(q.Publisher), params
	case q.Series != "":
		params.Set("f", "download")
		return "/series/" + url.PathEscape(q.Series), params
	case q.Director != "":
		params.Set("f", "download")
		return "/directors/" + url.PathEscape(q.Director), params
	case len(q.TagIDs) > 0:
		for _, cid := range sortedKeys(q.TagIDs) {
			params.Set("c"+cid, q.TagIDs[cid])
		}
		return "/tags", params
	case q.Category != "" && q.Category != CategoryAll:
		if q.WithSubtitle {
			params.Set("f", "c")
		}
		return "/" + string(q.Category), params
	default:
		return "/", params
	}
}

// ActorMovies implements ActorDetailer. Anonymous /actors/{id} pages are
// publicly browsable, but the t=d torrent filter redirects to the login wall,
// so it is only requested when a web session cookie is configured.
func (b *webBackend) ActorMovies(ctx context.Context, actorID string, p Page) (*SearchResult, error) {
	params := url.Values{}
	if cookie, _ := b.t.session(); cookie != "" {
		params.Set("t", "d") // logged-in site default: only entries with torrents
	}
	page := p.pageOrDefault(1)
	if page > 1 {
		params.Set("page", strconv.Itoa(page))
	}
	doc, _, err := b.getHTML(ctx, "/actors/"+url.PathEscape(actorID), params)
	if err != nil {
		return nil, err
	}
	res := &SearchResult{
		Query:   Query{Keyword: actorID, Scope: ScopeMovie, Page: p},
		Movies:  parseMovieList(doc, page),
		Current: page,
		Source:  b.Name(),
	}
	for i := range res.Movies {
		res.Movies[i].Source = b.Name()
	}
	_, res.MaxPage = parsePagination(doc)
	if len(res.Movies) == 0 {
		return nil, fmt.Errorf("%w: filmography of actor %s", ErrEmptyResult, actorID)
	}
	return res, nil
}

// SearchLists implements ListSearcher by scraping the list tab of /search
// (f=list), which works without login.
func (b *webBackend) SearchLists(ctx context.Context, keyword string, p Page) ([]ListSummary, error) {
	params := url.Values{"q": {keyword}, "f": {"list"}}
	if page := p.pageOrDefault(1); page > 1 {
		params.Set("page", strconv.Itoa(page))
	}
	doc, _, err := b.getHTML(ctx, "/search", params)
	if err != nil {
		return nil, err
	}
	var out []ListSummary
	doc.Find("#lists a.box").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		id := strings.TrimPrefix(href, "/lists/")
		if id == "" || id == href {
			return
		}
		name := strings.TrimSpace(a.Find("strong").First().Text())
		if name == "" {
			name = strings.TrimSpace(a.Text())
		}
		out = append(out, ListSummary{
			ID:          id,
			Name:        name,
			MoviesCount: ParseInt(a.Find("span").First().Text()),
			Href:        "/lists/" + id,
			Source:      b.Name(),
		})
	})
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: lists %q", ErrEmptyResult, keyword)
	}
	return out, nil
}

// ListMovies implements ListSearcher by scraping /lists/{id}; the movie grid
// reuses the shared list parser.
func (b *webBackend) ListMovies(ctx context.Context, listID string, p Page) (*SearchResult, error) {
	params := url.Values{}
	if page := p.pageOrDefault(1); page > 1 {
		params.Set("page", strconv.Itoa(page))
	}
	doc, _, err := b.getHTML(ctx, "/lists/"+url.PathEscape(listID), params)
	if err != nil {
		return nil, err
	}
	page := p.pageOrDefault(1)
	res := &SearchResult{
		Query:   Query{Keyword: listID, Scope: ScopeMovie, Page: p},
		Movies:  parseMovieList(doc, page),
		Current: page,
		Source:  b.Name(),
	}
	for i := range res.Movies {
		res.Movies[i].Source = b.Name()
	}
	_, res.MaxPage = parsePagination(doc)
	if len(res.Movies) == 0 {
		return nil, fmt.Errorf("%w: movies of list %s", ErrEmptyResult, listID)
	}
	return res, nil
}

// Actor implements ActorDetailer by scraping /actors/{id}.
func (b *webBackend) Actor(ctx context.Context, id string) (*Actor, error) {
	doc, site, err := b.getHTML(ctx, "/actors/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	a := &Actor{ID: id, Href: "/actors/" + id, Source: b.Name()}
	doc.Find("span.actor-section-name").First().Each(func(_ int, s *goquery.Selection) {
		a.Name = strings.TrimSpace(s.Text())
	})
	if a.Name == "" {
		a.Name = strings.TrimSuffix(strings.TrimSpace(doc.Find("title").First().Text()), " | JavDB 成人影片數據庫")
	}
	doc.Find("img.avatar, img.actor-photo").First().Each(func(_ int, s *goquery.Selection) {
		src, _ := s.Attr("src")
		a.AvatarURL = FixImageURL(src)
	})
	doc.Find(".section-column .info, .actor-info").First().Each(func(_ int, s *goquery.Selection) {
		for _, line := range strings.Split(s.Text(), "\n") {
			kv := strings.SplitN(strings.TrimSpace(line), ":", 2)
			if len(kv) != 2 {
				continue
			}
			switch strings.TrimSpace(kv[0]) {
			case "生日", "出生日期":
				a.Birthday = strings.TrimSpace(kv[1])
			case "身高":
				a.Height = ParseInt(kv[1])
			case "三圍", "三围":
				parts := strings.Split(strings.TrimSpace(kv[1]), "-")
				if len(parts) == 3 {
					a.Bust, a.Waist, a.Hips = ParseInt(parts[0]), ParseInt(parts[1]), ParseInt(parts[2])
				}
			case "出生地":
				a.Birthplace = strings.TrimSpace(kv[1])
			}
		}
	})
	if a.Name == "" {
		return nil, fmt.Errorf("%w: actor %s on %s", ErrNotFound, id, site)
	}
	return a, nil
}

// ---------------------------------------------------------------------------
// Detail / magnets
// ---------------------------------------------------------------------------

// MovieDetail implements Detailer via /v/{id}.
func (b *webBackend) MovieDetail(ctx context.Context, id string) (*Detail, error) {
	if !looksLikeID(id) && IsPlausibleVideoCode(id) {
		// A video code cannot be resolved without a search round-trip; ask the
		// caller (Client.Movie) to resolve the id first. looksLikeID wins over
		// IsPlausibleVideoCode because ids such as "yxY7kW" also contain a digit.
		return nil, fmt.Errorf("%w: web detail needs a movie id, got code %q", ErrInvalidQuery, id)
	}
	doc, _, err := b.getHTML(ctx, "/v/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	d := parseDetail(doc, id, b.Name())
	if d.Title == "" && d.Code == "" {
		return nil, fmt.Errorf("%w: movie %s", ErrNotFound, id)
	}
	return d, nil
}

// Magnets implements MagnetLister by scraping the detail page's magnet table.
func (b *webBackend) Magnets(ctx context.Context, movieID string) ([]Magnet, error) {
	doc, _, err := b.getHTML(ctx, "/v/"+url.PathEscape(movieID), nil)
	if err != nil {
		return nil, err
	}
	magnets := parseMagnets(doc)
	if len(magnets) == 0 {
		return nil, fmt.Errorf("%w: magnets for %s (login may be required)", ErrEmptyResult, movieID)
	}
	return magnets, nil
}

// Reviews is not implemented for the web backend: the site serves comments
// only to logged-in users while the app API serves them publicly.
func (b *webBackend) Reviews(ctx context.Context, q ReviewQuery) (*ReviewPage, error) {
	return nil, fmt.Errorf("%w: web reviews", ErrUnsupported)
}

// TagGroups implements TagProvider from the public genres page.
func (b *webBackend) TagGroups(ctx context.Context, scope Category) ([]TagGroup, error) {
	path := "/genres"
	if scope != "" && scope != CategoryAll {
		path = "/genres/" + string(scope)
	}
	doc, _, err := b.getHTML(ctx, path, nil)
	if err != nil {
		return nil, err
	}
	var group TagGroup
	group.Name = "类别"
	doc.Find("a[href^='/tags']").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		name := strings.TrimSpace(s.Text())
		if name == "" {
			return
		}
		categoryID, id := tagHrefID(href)
		if group.CategoryID == "" {
			group.CategoryID = categoryID
		}
		group.Options = append(group.Options, TagOption{Name: ToSimplified(name), ID: id})
	})
	if len(group.Options) == 0 {
		return nil, fmt.Errorf("%w: genres on web", ErrEmptyResult)
	}
	sort.Slice(group.Options, func(i, j int) bool { return group.Options[i].Name < group.Options[j].Name })
	return []TagGroup{group}, nil
}

// tagHrefID reads a tag's filter coordinates out of the two URL shapes the site
// uses: "/tags?c4=15" (category 4, tag 15) and "/tags/abc" (path id).
func tagHrefID(href string) (categoryID, tagID string) {
	if i := strings.Index(href, "?"); i >= 0 {
		for _, pair := range strings.Split(href[i+1:], "&") {
			kv := strings.SplitN(pair, "=", 2)
			if len(kv) == 2 && strings.HasPrefix(kv[0], "c") && len(kv[0]) > 1 {
				return kv[0][1:], kv[1]
			}
		}
		return "", ""
	}
	return "", HrefID(href)
}

// ParseTagHref reads the c{N}={id} filter coordinates out of a /tags?... href
// ("/tags?c2=12" -> "2", "12"). Returns empty strings for other href shapes.
func ParseTagHref(href string) (group, id string) {
	return tagHrefID(href)
}

// TagFilters returns the detail page's genre links as tag filter coordinates,
// ready for CategoryQuery.TagIDs. Links without c{N} coordinates are skipped.
func (d *Detail) TagFilters() []TagFilter {
	if d == nil {
		return nil
	}
	var out []TagFilter
	for _, g := range d.Genres {
		group, id := ParseTagHref(g.Href)
		if group == "" || id == "" {
			continue
		}
		out = append(out, TagFilter{Group: group, ID: id, Name: g.Name})
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
