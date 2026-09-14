package javdb

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Client is the entry point of the package. It fronts two backends and picks
// the first one that can answer, so callers get one stable API regardless of
// whether the mobile JSON API or the HTML site served the data:
//
//	api - the JavDB mobile app JSON API (clean, fast, no Cloudflare)
//	web - HTML scraping of javdb.com and its mirrors (rankings by category,
//	      maker/series/video-code listings, actor filmographies)
//
// A typical search therefore costs one JSON request, while a TOP250 request
// falls back to HTML when no app token is configured.
type Client struct {
	opts     Options
	t        *transport
	backends []Backend
	api      *apiBackend
	web      *webBackend
	cache    *cache
}

// APIBackend is the capability set of the mobile JSON API backend.
type APIBackend interface {
	Backend
	MovieSearcher
	ActorSearcher
	Ranker
	Detailer
	MagnetLister
	ReviewLister
	ActorDetailer
	TagProvider
	Authenticator
}

// WebBackend is the capability set of the HTML scraping backend.
type WebBackend interface {
	Backend
	MovieSearcher
	ActorSearcher
	Ranker
	Detailer
	MagnetLister
	ListPager
	ActorDetailer
	TagProvider
}

// RelatedLister is an extra API-only capability (community lists containing a movie).
type RelatedLister interface {
	RelatedLists(ctx context.Context, movieID string, p Page) ([]Link, error)
}

// New builds a Client. With no options both backends are enabled with the
// default sites and sane rate limiting.
func New(opts ...Option) (*Client, error) {
	merged := DefaultOptions()
	for _, with := range opts {
		with(&merged)
	}
	merged.Sites = normalizeSites(merged.Sites)
	merged.APIBase = strings.TrimSuffix(merged.APIBase, "/")

	t, err := newTransport(merged)
	if err != nil {
		return nil, err
	}
	c := &Client{opts: merged, t: t, cache: newCache(merged.CacheTTL)}
	if merged.APIBase != "" {
		c.api = newAPIBackend(t)
		c.backends = append(c.backends, c.api)
	}
	if len(merged.Sites) > 0 {
		c.web = newWebBackend(t)
		c.backends = append(c.backends, c.web)
	}
	if len(c.backends) == 0 {
		return nil, fmt.Errorf("javdb: no backend enabled (set Options.Sites or Options.APIBase)")
	}
	return c, nil
}

// MustNew is New for package-level variables; it panics on configuration errors.
func MustNew(opts ...Option) *Client {
	c, err := New(opts...)
	if err != nil {
		panic(err)
	}
	return c
}

// Backends lists the enabled backend names in fallback order.
func (c *Client) Backends() []string {
	names := make([]string, 0, len(c.backends))
	for _, b := range c.backends {
		names = append(names, b.Name())
	}
	return names
}

// API returns the mobile JSON API backend (nil when disabled).
func (c *Client) API() APIBackend {
	if c.api == nil {
		return nil
	}
	return c.api
}

// Web returns the HTML backend (nil when no site is configured).
func (c *Client) Web() WebBackend {
	if c.web == nil {
		return nil
	}
	return c.web
}

// Options returns a copy of the effective configuration.
func (c *Client) Options() Options { return c.opts }

// Site returns the preferred web mirror (useful for building absolute links).
func (c *Client) Site() string { return c.t.Site() }

// ResetCache drops all memoised results.
func (c *Client) ResetCache() { c.cache.Purge() }

// ---------------------------------------------------------------------------
// Search
// ---------------------------------------------------------------------------

// Search runs a query against the enabled backends. Scope selects the entity
// type; the first backend that returns entries wins.
func (c *Client) Search(ctx context.Context, q Query) (*SearchResult, error) {
	if strings.TrimSpace(q.Keyword) == "" {
		return nil, fmt.Errorf("%w: empty keyword", ErrInvalidQuery)
	}
	if q.Scope == ScopeActor {
		actors, err := c.SearchActors(ctx, q)
		if err != nil {
			return nil, err
		}
		res := &SearchResult{Query: q, Actors: actors, Current: q.pageOrDefault(1)}
		if len(actors) > 0 {
			res.Source = actors[0].Source
		}
		return res, nil
	}
	key := "search:" + searchKey(q)
	v, err := c.cache.Do(key, func() (any, error) {
		return call(ctx, c, "search "+q.Keyword, func(ctx context.Context, b Backend) (any, error) {
			s, ok := b.(MovieSearcher)
			if !ok {
				return nil, fmt.Errorf("%w: %s search", ErrUnsupported, b.Name())
			}
			res, err := s.SearchMovies(ctx, q)
			if err != nil {
				return nil, err
			}
			res.Query = q
			return res, nil
		})
	})
	if err != nil {
		return nil, err
	}
	return v.(*SearchResult), nil
}

// SearchMovies is a shorthand for Search with a movie scope.
func (c *Client) SearchMovies(ctx context.Context, keyword string, opts ...QueryOption) (*SearchResult, error) {
	q := newQuery(keyword, opts)
	return c.Search(ctx, q)
}

// SearchActors resolves actor profiles by name (stage names and aliases).
func (c *Client) SearchActors(ctx context.Context, q Query) ([]Actor, error) {
	keyword := strings.TrimSpace(q.Keyword)
	if keyword == "" {
		return nil, fmt.Errorf("%w: empty keyword", ErrInvalidQuery)
	}
	v, err := call(ctx, c, "actor search "+keyword, func(ctx context.Context, b Backend) (any, error) {
		s, ok := b.(ActorSearcher)
		if !ok {
			return nil, fmt.Errorf("%w: %s actor search", ErrUnsupported, b.Name())
		}
		return s.SearchActors(ctx, q)
	})
	if err != nil {
		return nil, err
	}
	return v.([]Actor), nil
}

// FindByCode looks up one movie by its exact video code ("SSIS-001").
// It returns ErrNotFound when no backend reports an exact code match.
func (c *Client) FindByCode(ctx context.Context, code string) (*Movie, error) {
	want := NormalizeCode(code)
	if want == "" {
		return nil, fmt.Errorf("%w: empty code", ErrInvalidQuery)
	}
	res, err := c.Search(ctx, Query{Keyword: want, Scope: ScopeMovie, Filter: "code"})
	if err != nil {
		// Backends without a code-only filter still answer generic searches.
		if !errors.Is(err, ErrInvalidQuery) {
			res, err = c.Search(ctx, Query{Keyword: want, Scope: ScopeMovie})
		}
		if err != nil {
			return nil, err
		}
	}
	for i := range res.Movies {
		if res.Movies[i].Code == want {
			return &res.Movies[i], nil
		}
	}
	if len(res.Movies) == 1 && res.Movies[0].Code == "" {
		return &res.Movies[0], nil
	}
	return nil, fmt.Errorf("%w: code %s", ErrNotFound, want)
}

// ---------------------------------------------------------------------------
// Rankings
// ---------------------------------------------------------------------------

// Ranking resolves a ranking request across backends. Page selects the window:
// the app ranking endpoints ignore their paging parameters and answer one fixed
// sheet (61 rows for 热播榜, 97 for 演員排行), so those backends cut the window
// themselves and the clip below is the safety net for a backend that simply
// returns more rows than asked for.
func (c *Client) Ranking(ctx context.Context, q RankingQuery) (*Ranking, error) {
	if q.Kind == "" {
		q.Kind = RankingMovies
	}
	key := fmt.Sprintf("ranking:%s:%s:%s:%s:%s:%s:%d:%d:%d", q.Kind, q.Period, q.Category,
		q.Filter, q.Slice.Type, q.Slice.Value, q.StartRank, q.Page.Page, q.Limit)
	v, err := c.cache.Do(key, func() (any, error) {
		got, err := call(ctx, c, "ranking "+string(q.Kind), func(ctx context.Context, b Backend) (any, error) {
			r, ok := b.(Ranker)
			if !ok {
				return nil, fmt.Errorf("%w: %s ranking", ErrUnsupported, b.Name())
			}
			return r.Ranking(ctx, q)
		})
		if err != nil {
			return nil, err
		}
		return trimRanking(got.(*Ranking), q.Limit), nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*Ranking), nil
}

// trimRanking drops the rows past the requested page size. Rank numbers are
// assigned before clipping, so the surviving positions stay truthful.
func trimRanking(r *Ranking, limit int) *Ranking {
	if limit <= 0 {
		return r
	}
	if len(r.Movies) > limit {
		r.Movies = r.Movies[:limit]
	}
	if len(r.Actors) > limit {
		r.Actors = r.Actors[:limit]
	}
	return r
}

// Playback returns the 热播榜 (daily/weekly/monthly). p.Limit clips the sheet
// the backend returns; a zero Page keeps its natural size.
func (c *Client) Playback(ctx context.Context, period Period, p Page) (*Ranking, error) {
	return c.Ranking(ctx, RankingQuery{Kind: RankingPlayback, Period: orPeriod(period), Page: p})
}

// CategoryRanking returns a 分類排行榜 (有碼/無碼/歐美/FC2 ...) for a period.
func (c *Client) CategoryRanking(ctx context.Context, period Period, category Category, p Page) (*Ranking, error) {
	return c.Ranking(ctx, RankingQuery{Kind: RankingMovies, Period: orPeriod(period), Category: category, Page: p})
}

// Top250 returns the TOP250 list. The JSON API path needs an app login
// (see Login), otherwise the HTML ranking page is scraped, which in turn needs
// Options.Cookie; ErrAuthRequired is returned when neither is available.
func (c *Client) Top250(ctx context.Context, slice Top250Slice, p Page) (*Ranking, error) {
	if slice.Type == "" {
		slice = Top250All
	}
	return c.Ranking(ctx, RankingQuery{Kind: RankingTop250, Slice: slice, Page: p})
}

// ActorRanking returns the actor ranking (演員排行) for a category.
func (c *Client) ActorRanking(ctx context.Context, category Category, p Page) (*Ranking, error) {
	return c.Ranking(ctx, RankingQuery{Kind: RankingActors, Category: category, Page: p})
}

func orPeriod(p Period) Period {
	if p == "" {
		return PeriodDaily
	}
	return p
}

// ---------------------------------------------------------------------------
// Movie detail / magnets / reviews
// ---------------------------------------------------------------------------

// Movie returns full metadata for an id ("yxY7kW") or a video code ("MIDA-783").
// Video codes are resolved through a search first; tokens that look like ids
// but turn out to be unknown are retried as codes. Results are cached.
func (c *Client) Movie(ctx context.Context, idOrCode string) (*Detail, error) {
	token := strings.TrimSpace(idOrCode)
	if token == "" {
		return nil, fmt.Errorf("%w: empty movie reference", ErrInvalidQuery)
	}
	id := token
	resolved := true
	if !looksLikeID(token) {
		m, err := c.FindByCode(ctx, token)
		if err != nil {
			return nil, err
		}
		if m.ID == "" {
			return nil, fmt.Errorf("%w: code %s has no resolvable id", ErrNotFound, token)
		}
		id = m.ID
		resolved = false
	}
	detail, err := c.detailByID(ctx, id)
	if err != nil && resolved && errors.Is(err, ErrNotFound) {
		// The id guess was wrong: treat the token as a video code instead.
		if m, ferr := c.FindByCode(ctx, token); ferr == nil && m.ID != "" && m.ID != id {
			detail, err = c.detailByID(ctx, m.ID)
		}
	}
	return detail, err
}

func (c *Client) detailByID(ctx context.Context, id string) (*Detail, error) {
	key := "detail:" + id
	v, err := c.cache.Do(key, func() (any, error) {
		return call(ctx, c, "detail "+id, func(ctx context.Context, b Backend) (any, error) {
			d, ok := b.(Detailer)
			if !ok {
				return nil, fmt.Errorf("%w: %s detail", ErrUnsupported, b.Name())
			}
			detail, err := d.MovieDetail(ctx, id)
			if err != nil {
				return nil, err
			}
			if detail.ID == "" {
				detail.ID = id
			}
			if !detail.MagnetsFetched || len(detail.Magnets) == 0 {
				if m, ok := b.(MagnetLister); ok {
					if magnets, err := m.Magnets(ctx, id); err == nil {
						detail.Magnets = magnets
						detail.MagnetsFetched = true
						detail.MagnetsCount = len(magnets)
					}
				}
			}
			return detail, nil
		})
	})
	if err != nil {
		return nil, err
	}
	return v.(*Detail), nil
}

// Magnets returns the torrent list of a movie, from whichever backend has it.
func (c *Client) Magnets(ctx context.Context, idOrCode string) ([]Magnet, error) {
	id, err := c.resolveID(ctx, idOrCode)
	if err != nil {
		return nil, err
	}
	v, err := call(ctx, c, "magnets "+id, func(ctx context.Context, b Backend) (any, error) {
		m, ok := b.(MagnetLister)
		if !ok {
			return nil, fmt.Errorf("%w: %s magnets", ErrUnsupported, b.Name())
		}
		return m.Magnets(ctx, id)
	})
	if err != nil {
		return nil, err
	}
	magnets := v.([]Magnet)
	sortMagnets(magnets)
	return magnets, nil
}

// BestMagnet picks the most useful torrent for a movie: subtitles first, then
// size. It returns ErrNotFound when no magnet exists.
func (c *Client) BestMagnet(ctx context.Context, idOrCode string) (*Magnet, error) {
	magnets, err := c.Magnets(ctx, idOrCode)
	if err != nil {
		return nil, err
	}
	if len(magnets) == 0 {
		return nil, fmt.Errorf("%w: no magnets for %s", ErrNotFound, idOrCode)
	}
	m := magnets[0]
	return &m, nil
}

// Reviews returns one page of user comments.
func (c *Client) Reviews(ctx context.Context, q ReviewQuery) (*ReviewPage, error) {
	id, err := c.resolveID(ctx, q.MovieID)
	if err != nil {
		return nil, err
	}
	q.MovieID = id
	v, err := call(ctx, c, "reviews "+id, func(ctx context.Context, b Backend) (any, error) {
		r, ok := b.(ReviewLister)
		if !ok {
			return nil, fmt.Errorf("%w: %s reviews", ErrUnsupported, b.Name())
		}
		return r.Reviews(ctx, q)
	})
	if err != nil {
		return nil, err
	}
	return v.(*ReviewPage), nil
}

// RelatedLists lists community collections containing a movie (API only).
func (c *Client) RelatedLists(ctx context.Context, idOrCode string, p Page) ([]Link, error) {
	id, err := c.resolveID(ctx, idOrCode)
	if err != nil {
		return nil, err
	}
	v, err := call(ctx, c, "related lists "+id, func(ctx context.Context, b Backend) (any, error) {
		r, ok := b.(RelatedLister)
		if !ok {
			return nil, fmt.Errorf("%w: %s related lists", ErrUnsupported, b.Name())
		}
		return r.RelatedLists(ctx, id, p)
	})
	if err != nil {
		return nil, err
	}
	return v.([]Link), nil
}

// ---------------------------------------------------------------------------
// Actors / listings / tags
// ---------------------------------------------------------------------------

// Actor resolves an actor by id ("21Jp") or by stage name. ID-shaped tokens are
// looked up directly and fall back to a name search when unknown, because
// JavDB ids and video codes share the same alphabet.
func (c *Client) Actor(ctx context.Context, idOrName string) (*Actor, error) {
	token := strings.TrimSpace(idOrName)
	if token == "" {
		return nil, fmt.Errorf("%w: empty actor reference", ErrInvalidQuery)
	}
	if looksLikeID(token) {
		a, err := c.actorByID(ctx, token)
		if err == nil {
			return a, nil
		}
		if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrEmptyResult) {
			return nil, err
		}
	}
	list, err := c.SearchActors(ctx, Query{Keyword: token})
	if err != nil {
		return nil, err
	}
	id := token
	for _, a := range list {
		if a.ID != "" {
			id = a.ID
			break
		}
	}
	if id == token {
		// The search already produced the profile; no need to fetch it twice.
		for _, a := range list {
			if a.Name != "" {
				match := a
				return &match, nil
			}
		}
	}
	return c.actorByID(ctx, id)
}

func (c *Client) actorByID(ctx context.Context, id string) (*Actor, error) {
	v, err := call(ctx, c, "actor "+id, func(ctx context.Context, b Backend) (any, error) {
		ad, ok := b.(ActorDetailer)
		if !ok {
			return nil, fmt.Errorf("%w: %s actor", ErrUnsupported, b.Name())
		}
		return ad.Actor(ctx, id)
	})
	if err != nil {
		return nil, err
	}
	return v.(*Actor), nil
}

// ActorMovies lists an actor's filmography.
func (c *Client) ActorMovies(ctx context.Context, idOrName string, p Page) (*SearchResult, error) {
	id := idOrName
	if !looksLikeID(idOrName) {
		a, err := c.Actor(ctx, idOrName)
		if err != nil {
			return nil, err
		}
		id = a.ID
	}
	v, err := call(ctx, c, "actor movies "+id, func(ctx context.Context, b Backend) (any, error) {
		ad, ok := b.(ActorDetailer)
		if !ok {
			return nil, fmt.Errorf("%w: %s actor movies", ErrUnsupported, b.Name())
		}
		return ad.ActorMovies(ctx, id, p)
	})
	if err != nil {
		return nil, err
	}
	return v.(*SearchResult), nil
}

// CategoryMovies browses a bucket or a scoped listing page
// (latest uploads, /censored, a video-code prefix, a maker, a series ...).
func (c *Client) CategoryMovies(ctx context.Context, q CategoryQuery) (*SearchResult, error) {
	v, err := call(ctx, c, "category listing", func(ctx context.Context, b Backend) (any, error) {
		lp, ok := b.(ListPager)
		if !ok {
			return nil, fmt.Errorf("%w: %s listings", ErrUnsupported, b.Name())
		}
		return lp.CategoryMovies(ctx, q)
	})
	if err != nil {
		return nil, err
	}
	return v.(*SearchResult), nil
}

// TagGroups lists available filter tags for a bucket.
func (c *Client) TagGroups(ctx context.Context, scope Category) ([]TagGroup, error) {
	key := "tags:" + string(scope)
	v, err := c.cache.Do(key, func() (any, error) {
		return call(ctx, c, "tags", func(ctx context.Context, b Backend) (any, error) {
			tp, ok := b.(TagProvider)
			if !ok {
				return nil, fmt.Errorf("%w: %s tags", ErrUnsupported, b.Name())
			}
			return tp.TagGroups(ctx, scope)
		})
	})
	if err != nil {
		return nil, err
	}
	return v.([]TagGroup), nil
}

// ---------------------------------------------------------------------------
// Session
// ---------------------------------------------------------------------------

// Login authenticates for gated data. Importing a cookie or an app token is
// preferred (JavDB rate-limits password logins); password login goes to the
// mobile API, which yields a JWT good for TOP250 and personal lists.
func (c *Client) Login(ctx context.Context, cred Credentials) error {
	if cred.Cookie != "" {
		c.t.setCookie(cred.Cookie)
	}
	if cred.AppToken != "" {
		c.t.setAppToken(cred.AppToken)
	}
	if cred.Username == "" || cred.Password == "" {
		return nil
	}
	var errs []error
	for _, b := range c.backends {
		auth, ok := b.(Authenticator)
		if !ok {
			continue
		}
		if err := auth.Login(ctx, cred); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", b.Name(), err))
			continue
		}
		return nil
	}
	if len(errs) == 0 {
		return fmt.Errorf("%w: password login", ErrUnsupported)
	}
	return &MultiError{Op: "login", Errs: errs}
}

// Session reports the auth material the client is using: the web cookie and the
// app JWT. Either may be empty when anonymous. Both are secrets - keep them out
// of logs and never commit them.
func (c *Client) Session() (cookie, appToken string) { return c.t.session() }

// ---------------------------------------------------------------------------
// backend fan-out
// ---------------------------------------------------------------------------

// call runs fn against each backend in priority order and returns the first
// success. ErrUnsupported skips immediately; deterministic errors
// (ErrAuthRequired, ErrNotFound) are still remembered but later backends get a
// chance, because the HTML site and the app API gate different data.
func call(ctx context.Context, c *Client, op string, fn func(context.Context, Backend) (any, error)) (any, error) {
	var errs []error
	for _, b := range c.backends {
		v, err := fn(ctx, b)
		switch {
		case err == nil:
			return v, nil
		case errors.Is(err, ErrUnsupported):
			c.opts.Logger.Debug("javdb backend cannot serve op", "backend", b.Name(), "op", op)
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return nil, err
		default:
			errs = append(errs, fmt.Errorf("%s: %w", b.Name(), err))
			c.opts.Logger.Warn("javdb backend failed", "backend", b.Name(), "op", op, "err", err)
		}
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, op)
	}
	// A uniform failure mode is more useful than an aggregate.
	if uniformKind(errs) != nil {
		return nil, uniformKind(errs)
	}
	return nil, &MultiError{Op: op, Errs: errs}
}

// uniformKind returns the first error when every failure shares the same
// sentinel classification, otherwise nil.
func uniformKind(errs []error) error {
	kind := kindOf(errs[0])
	if kind == nil {
		return nil
	}
	for _, e := range errs[1:] {
		if kindOf(e) != kind {
			return nil
		}
	}
	return kind
}

func kindOf(err error) error {
	for _, sentinel := range []error{ErrAuthRequired, ErrNotFound, ErrChallenge, ErrRateLimited, ErrMaintenance, ErrInvalidQuery, ErrEmptyResult} {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	return nil
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9]{2,10}$`)

// looksLikeID distinguishes a JavDB internal entity id ("yxY7kW", "21Jp") from
// a video code ("SSIS-001", "062216_001", "ABP123"). IDs are short base62
// tokens without separators that mix letter cases; codes are upper-case, purely
// numeric or carry a -/_/. separator.
func looksLikeID(s string) bool {
	if !idRe.MatchString(s) {
		return false
	}
	hasUpper, hasLower, hasDigit := charClasses(s)
	switch {
	case hasUpper && hasLower:
		return true
	case !hasUpper && !hasDigit && len(s) >= 4:
		return true // lower-case letters only: ids exist, codes do not
	default:
		return false
	}
}

func charClasses(s string) (hasUpper, hasLower, hasDigit bool) {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= '0' && r <= '9':
			hasDigit = true
		}
	}
	return hasUpper, hasLower, hasDigit
}

func (c *Client) resolveID(ctx context.Context, idOrCode string) (string, error) {
	token := strings.TrimSpace(idOrCode)
	if token == "" {
		return "", fmt.Errorf("%w: empty reference", ErrInvalidQuery)
	}
	if looksLikeID(token) {
		return token, nil
	}
	m, err := c.FindByCode(ctx, token)
	if err != nil {
		return "", err
	}
	if m.ID == "" {
		return "", fmt.Errorf("%w: %s has no id", ErrNotFound, token)
	}
	return m.ID, nil
}

// sortMagnets orders by subtitle availability, then size (largest first).
func sortMagnets(magnets []Magnet) {
	score := func(m Magnet) int {
		s := 0
		switch m.Category() {
		case "subtitle":
			s += 100
		case "hacked_subtitle":
			s += 90
		case "hacked_no_subtitle":
			s += 40
		default:
			s += 10
		}
		if m.HD {
			s += 5
		}
		return s
	}
	for i := 1; i < len(magnets); i++ {
		for j := i; j > 0; j-- {
			a, b := magnets[j-1], magnets[j]
			if score(a) > score(b) || (score(a) == score(b) && a.SizeBytes >= b.SizeBytes) {
				break
			}
			magnets[j-1], magnets[j] = b, a
		}
	}
}

func searchKey(q Query) string {
	parts := []string{q.Scope.String(), NormalizeCode(q.Keyword), string(q.Category), string(q.Filter), string(q.Sort), q.WithSubtitleString()}
	if q.FromRecent {
		parts = append(parts, "recent")
	}
	for _, cid := range sortedKeys(q.TagIDs) {
		parts = append(parts, "c"+cid+"="+q.TagIDs[cid])
	}
	return strings.Join(parts, "|") + fmt.Sprintf("|%d/%d/%d/%d", q.Page, q.Limit, q.Year, q.Month)
}

// ---------------------------------------------------------------------------
// Query builders
// ---------------------------------------------------------------------------

// QueryOption mutates a Query.
type QueryOption func(*Query)

// WithScope sets the searched entity type.
func WithScope(s SearchScope) QueryOption { return func(q *Query) { q.Scope = s } }

// WithCategory restricts the search to one bucket.
func WithCategory(c Category) QueryOption { return func(q *Query) { q.Category = c } }

// WithSort sets the ordering hint.
func WithSort(s SortBy) QueryOption { return func(q *Query) { q.Sort = s } }

// WithFilter sets the subset filter.
func WithFilter(f FilterBy) QueryOption { return func(q *Query) { q.Filter = f } }

// WithSubtitle restricts results to entries with Chinese-subtitled torrents.
func WithSubtitle() QueryOption { return func(q *Query) { q.WithSubtitle = true } }

// WithRecent searches the recent (weekly) window instead of the archive.
func WithRecent() QueryOption { return func(q *Query) { q.FromRecent = true } }

// WithYear restricts the release year.
func WithYear(y int) QueryOption { return func(q *Query) { q.Year = y } }

// WithPage requests a specific page size.
func WithPage(page, limit int) QueryOption {
	return func(q *Query) { q.Page.Page = page; q.Page.Limit = limit }
}

func newQuery(keyword string, opts []QueryOption) Query {
	q := Query{Keyword: keyword, Scope: ScopeMovie}
	for _, with := range opts {
		with(&q)
	}
	return q
}

// String implements fmt.Stringer for SearchScope.
func (s SearchScope) String() string {
	if s == "" {
		return string(ScopeMovie)
	}
	return string(s)
}

// WithSubtitleString keeps search keys stable when the flag toggles.
func (q Query) WithSubtitleString() string {
	if q.WithSubtitle {
		return "sub"
	}
	return ""
}
