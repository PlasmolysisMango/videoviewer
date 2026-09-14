package javdb

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Category identifies a JavDB content bucket used by rankings and listings.
type Category string

const (
	CategoryAll        Category = "all"        // 全部
	CategoryCensored   Category = "censored"   // 有碼
	CategoryUncensored Category = "uncensored" // 無碼
	CategoryWestern    Category = "western"    // 歐美
	CategoryFC2        Category = "fc2"        // FC2
	CategoryAmateur    Category = "amateur"    // 业余
	CategoryAnime      Category = "anime"      // 動畫
	CategoryChinese    Category = "chinese"    // 國產
)

// Period is a ranking window.
type Period string

const (
	PeriodDaily   Period = "daily"
	PeriodWeekly  Period = "weekly"
	PeriodMonthly Period = "monthly"
	PeriodYearly  Period = "yearly"
	PeriodAllTime Period = "all"
)

// RankingKind selects which ranking to read.
type RankingKind string

const (
	// RankingPlayback 热播榜：按播放量/评分排序的近期热播条目。
	RankingPlayback RankingKind = "playback"
	// RankingMovies 分类排行榜（有码/无码/欧美/FC2 ...）日周月榜。
	RankingMovies RankingKind = "movies"
	// RankingTop250 TOP250 榜，支持全部/分类/年份三种切面。
	RankingTop250 RankingKind = "top250"
	// RankingActors 演员排行榜。
	RankingActors RankingKind = "actors"
	// RankingFanzaAward FANZA(DMM) 成人奖榜单。
	RankingFanzaAward RankingKind = "fanza_award"
)

// Top250Slice narrows a TOP250 query to a bucket.
// Type is one of "all", "video_type" or "year"; Value carries the matching
// option (video type index 0..3, or a 4-digit year).
type Top250Slice struct {
	Type  string
	Value string
}

var (
	// Top250All is the unfiltered TOP250 list.
	Top250All = Top250Slice{Type: "all"}
	// Top250Censored / Top250Uncensored / Top250Western / Top250FC2 slice TOP250 by video type.
	Top250Censored   = Top250Slice{Type: "video_type", Value: "0"}
	Top250Uncensored = Top250Slice{Type: "video_type", Value: "1"}
	Top250Western    = Top250Slice{Type: "video_type", Value: "2"}
	Top250FC2        = Top250Slice{Type: "video_type", Value: "3"}
)

// Top250OfYear builds a per-year TOP250 slice.
func Top250OfYear(year int) Top250Slice {
	return Top250Slice{Type: "year", Value: strconv.Itoa(year)}
}

// SortBy is a search ordering hint. Backends translate known values;
// unknown values are passed through so new server-side sorts keep working.
type SortBy string

const (
	SortRelevance   SortBy = "relevance"
	SortNewest      SortBy = "newest"
	SortOldest      SortBy = "oldest"
	SortHighest     SortBy = "highest"
	SortLowest      SortBy = "lowest"
	SortMostMagnet  SortBy = "most_magnets"
	SortMostPlayed  SortBy = "most_played"
	SortMostWatched SortBy = "most_watched"
)

// FilterBy narrows a search to a subset of entries.
type FilterBy string

const (
	FilterAll        FilterBy = "all"
	FilterWithSub    FilterBy = "c" // 含字幕磁链
	FilterPlayable   FilterBy = "p" // 可播放
	FilterDownloaded FilterBy = "m" // 可下载
	FilterSingle     FilterBy = "s" // 单体影片
	FilterNoWatched  FilterBy = "nowatched"
)

// SearchScope selects the entity type of a search request.
type SearchScope string

const (
	ScopeMovie SearchScope = "movie"
	ScopeActor SearchScope = "actor"
	ScopeTag   SearchScope = "tag"
)

// Page carries pagination request parameters.
// Limit <= 0 means "backend default".
type Page struct {
	Page  int
	Limit int
}

func (p Page) pageOrDefault(def int) int {
	if p.Page > 0 {
		return p.Page
	}
	return def
}

func (p Page) limitOrDefault(def int) int {
	if p.Limit > 0 {
		return p.Limit
	}
	return def
}

// Query describes a movie/actor/tag search.
type Query struct {
	// Keyword is the free-text search term, a video code ("SSIS-001") or an
	// actor name. Required for Search.
	Keyword string
	// Scope selects movies, actors or tag results. Empty means movies.
	Scope SearchScope
	// Category restricts the video bucket (censored/uncensored/western/fc2 ...).
	Category Category
	// Filter / Sort are ordering and subset hints.
	Filter FilterBy
	Sort   SortBy
	// Year and Month restrict release dates (Month is 1-12, both optional).
	Year  int
	Month int
	// TagIDs are category-scoped tag filters, e.g. {"4": "15"} sends c4=15
	// (熟女). Neither search endpoint supports them, so they are only honoured
	// by Client.CategoryMovies, which maps them onto the /tags listing.
	TagIDs map[string]string
	// WithSubtitle is shorthand for Filter = FilterWithSub.
	WithSubtitle bool
	// FromRecent searches recent (weekly) entries instead of the full archive.
	FromRecent bool
	Page
}

// Link is a named reference to another JavDB entity
// (actor, maker, publisher, series, director, tag, video code prefix).
type Link struct {
	Name string `json:"name"`
	// ID is the site identifier when known ("21Jp", "zKW", ...).
	ID string `json:"id,omitempty"`
	// Href is the site-relative path ("/actors/21Jp").
	Href string `json:"href,omitempty"`
	Kind string `json:"kind,omitempty"`
}

// String implements fmt.Stringer.
func (l Link) String() string { return l.Name }

// Actor is an actor profile.
type Actor struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	NameTraditional string `json:"name_zht,omitempty"`
	OtherName       string `json:"other_name,omitempty"`
	AvatarURL       string `json:"avatar_url,omitempty"`
	Href            string `json:"href,omitempty"`
	Uncensored      bool   `json:"uncensored,omitempty"`
	Gender          string `json:"gender,omitempty"`
	VideosCount     int    `json:"videos_count,omitempty"`
	// Ranking is the position on an actor ranking page (0 when unranked).
	Ranking    int    `json:"ranking,omitempty"`
	Birthday   string `json:"birthday,omitempty"`
	Height     int    `json:"height,omitempty"`
	Bust       int    `json:"bust,omitempty"`
	Cup        string `json:"cup,omitempty"`
	Waist      int    `json:"waist,omitempty"`
	Hips       int    `json:"hips,omitempty"`
	Birthplace string `json:"birthplace,omitempty"`
	Twitter    string `json:"twitter,omitempty"`
	Instagram  string `json:"instagram,omitempty"`
	Source     string `json:"-"`
}

// Movie is a listing card / search hit: the compact form of a title.
type Movie struct {
	ID           string   `json:"id"`
	Code         string   `json:"number"`
	Title        string   `json:"title"`
	OriginTitle  string   `json:"origin_title,omitempty"`
	CoverURL     string   `json:"cover_url,omitempty"`
	ThumbURL     string   `json:"thumb_url,omitempty"`
	ReleaseDate  string   `json:"release_date,omitempty"`
	Duration     int      `json:"duration,omitempty"`
	Score        float64  `json:"score,omitempty"`
	RateText     string   `json:"rate_text,omitempty"`
	Ratings      int      `json:"ratings_count,omitempty"`
	MagnetsCount int      `json:"magnets_count,omitempty"`
	HasCNSub     bool     `json:"has_cnsub,omitempty"`
	CanPlay      bool     `json:"can_play,omitempty"`
	NewMagnets   bool     `json:"new_magnets,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Actors       []string `json:"actors,omitempty"`
	Ranking      int      `json:"ranking,omitempty"`
	Page         int      `json:"page,omitempty"`
	Href         string   `json:"href,omitempty"`
	Source       string   `json:"-"`
}

// URL returns the canonical absolute web URL for the movie, when the ID is known.
func (m Movie) URL(site string) string {
	if m.Href != "" {
		if strings.HasPrefix(m.Href, "http") {
			return m.Href
		}
		return strings.TrimSuffix(site, "/") + ensureLeading(m.Href)
	}
	if m.ID != "" {
		return strings.TrimSuffix(site, "/") + "/v/" + m.ID
	}
	return ""
}

// Magnet is one torrent entry attached to a movie.
type Magnet struct {
	Name     string   `json:"name"`
	Hash     string   `json:"hash,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	SizeText string   `json:"size_text,omitempty"`
	// SizeBytes is parsed from SizeText. Beware: the app API reports the size of
	// the .torrent metadata (kilobytes) while the HTML page reports the video
	// total, so this field is only comparable within one Source.
	SizeBytes int64  `json:"size_bytes,omitempty"`
	Files     int    `json:"files_count,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	CNSub     bool   `json:"cnsub,omitempty"`
	HD        bool   `json:"hd,omitempty"`
	PikPakURL string `json:"pikpak_url,omitempty"`
	Source    string `json:"-"`
}

// MagnetURL returns the magnet URI, deriving it from the hash when present.
func (m Magnet) MagnetURL() string {
	if m.Hash != "" {
		return "magnet:?xt=urn:btih:" + m.Hash
	}
	return m.PikPakURL
}

// Category classifies a magnet by filename markers (subtitle / hacked / plain).
func (m Magnet) Category() string { return classifyMagnetName(m.Name, m.Tags) }

// Review is one user comment.
type Review struct {
	ID      string  `json:"id"`
	Author  string  `json:"author,omitempty"`
	Content string  `json:"content"`
	Rating  float64 `json:"rating,omitempty"`
	Date    string  `json:"date,omitempty"`
	Likes   int     `json:"likes,omitempty"`
	Source  string  `json:"-"`
}

// TagOption is one selectable filter value.
type TagOption struct {
	Name     string `json:"name"`
	ID       string `json:"id"`
	Selected bool   `json:"selected,omitempty"`
}

// TagGroup is a filter category ("主題", "體型") whose ID maps to the c{N}
// URL parameter (category_id "4" => c4).
type TagGroup struct {
	CategoryID string      `json:"category_id"`
	Name       string      `json:"name"`
	Options    []TagOption `json:"options,omitempty"`
}

// Tag is a playability/attribute flag exposed by the API (可播放, 含字幕 ...).
type Tag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Ranking is the result of a ranking request.
type Ranking struct {
	Kind     RankingKind `json:"kind"`
	Period   Period      `json:"period,omitempty"`
	Category Category    `json:"category,omitempty"`
	Title    string      `json:"title,omitempty"`
	Page     int         `json:"page"`
	MaxPage  int         `json:"max_page,omitempty"`
	Movies   []Movie     `json:"movies"`
	Actors   []Actor     `json:"actors,omitempty"`
	Source   string      `json:"-"`
}

// SearchResult is the result of a search request.
type SearchResult struct {
	Query   Query   `json:"query"`
	Movies  []Movie `json:"movies,omitempty"`
	Actors  []Actor `json:"actors,omitempty"`
	Tags    []Tag   `json:"tags,omitempty"`
	Current int     `json:"current_page"`
	// MaxPage and Total are 0 when the backend does not report them: the app
	// search endpoint answers only movies + current_page, so callers cannot
	// treat 0 as "one page".
	MaxPage int    `json:"max_page,omitempty"`
	Total   int    `json:"total,omitempty"`
	Source  string `json:"-"`
}

// Detail is the full information of one movie.
type Detail struct {
	Movie
	Summary        string   `json:"summary,omitempty"`
	CodePrefix     *Link    `json:"code_prefix,omitempty"`
	Maker          *Link    `json:"maker,omitempty"`
	Publisher      *Link    `json:"publisher,omitempty"`
	Series         *Link    `json:"series,omitempty"`
	Directors      []Link   `json:"directors,omitempty"`
	Genres         []Link   `json:"tags,omitempty"`
	ActorCredits   []Actor  `json:"actor_credits,omitempty"`
	PosterURL      string   `json:"poster_url,omitempty"`
	FanartURLs     []string `json:"fanart_urls,omitempty"`
	PreviewImages  []string `json:"preview_images,omitempty"`
	PreviewVideo   string   `json:"preview_video_url,omitempty"`
	PlaySources    []Link   `json:"play_sources,omitempty"`
	WantCount      int      `json:"want_count,omitempty"`
	WatchedCount   int      `json:"watched_count,omitempty"`
	CommentsCount  int      `json:"comments_count,omitempty"`
	ReviewsCount   int      `json:"reviews_count,omitempty"`
	Magnets        []Magnet `json:"magnets,omitempty"`
	MagnetsFetched bool     `json:"magnets_fetched,omitempty"`
}

// LeadActor returns the first billed actor, or nil.
func (d *Detail) LeadActor() *Actor {
	if d == nil || len(d.ActorCredits) == 0 {
		return nil
	}
	a := d.ActorCredits[0]
	return &a
}

// UnmarshalJSON accepts both numeric and string encodings for fields the
// upstream API is inconsistent about (score "4.02" vs 4.02, counts null).
func (m *Movie) UnmarshalJSON(b []byte) error {
	type alias Movie
	var aux struct {
		alias
		Score       json.RawMessage `json:"score"`
		Duration    json.RawMessage `json:"duration"`
		ReleaseDate json.RawMessage `json:"release_date"`
	}
	aux.alias = alias(*m)
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	*m = Movie(aux.alias)
	m.Score = parseFloatLoose(aux.Score)
	m.Duration = int(parseFloatLoose(aux.Duration))
	m.ReleaseDate = parseStringLoose(aux.ReleaseDate)
	return nil
}

func parseFloatLoose(raw json.RawMessage) float64 {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return 0
	}
	s = strings.Trim(s, `"`)
	if s == "" || s == "-" {
		return 0
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return v
	}
	return 0
}

func parseStringLoose(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return str
	}
	return strings.Trim(s, `"`)
}

// ensureLeading prefixes a path with "/" when missing.
func ensureLeading(p string) string {
	if strings.HasPrefix(p, "/") {
		return p
	}
	return "/" + p
}

// mustJSON is a debugging helper used by the CLI.
func mustJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
