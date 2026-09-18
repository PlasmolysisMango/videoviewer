package javdb

import "context"

// Backend is one concrete data source for JavDB (the mobile JSON API or the
// web HTML site). A backend implements only the capability interfaces it can
// serve; the Client probes for them with a type assertion and skips the
// backend otherwise.
type Backend interface {
	// Name identifies the backend in results and logs ("api", "web").
	Name() string
}

// MovieSearcher searches titles by keyword.
type MovieSearcher interface {
	SearchMovies(ctx context.Context, q Query) (*SearchResult, error)
}

// ActorSearcher searches actor profiles by name.
type ActorSearcher interface {
	SearchActors(ctx context.Context, q Query) ([]Actor, error)
}

// Ranker serves rankings (热播 / 分类排行 / TOP250 / 演员排行).
type Ranker interface {
	Ranking(ctx context.Context, q RankingQuery) (*Ranking, error)
}

// Detailer serves full movie metadata.
type Detailer interface {
	MovieDetail(ctx context.Context, id string) (*Detail, error)
}

// MagnetLister serves torrent lists of a movie.
type MagnetLister interface {
	Magnets(ctx context.Context, movieID string) ([]Magnet, error)
}

// ReviewLister serves user comments of a movie.
type ReviewLister interface {
	Reviews(ctx context.Context, q ReviewQuery) (*ReviewPage, error)
}

// ActorDetailer serves one actor profile plus their filmography.
type ActorDetailer interface {
	Actor(ctx context.Context, id string) (*Actor, error)
	ActorMovies(ctx context.Context, actorID string, p Page) (*SearchResult, error)
}

// ListSearcher serves community lists ("影單"): keyword search and the
// movie list of one list.
type ListSearcher interface {
	SearchLists(ctx context.Context, keyword string, p Page) ([]ListSummary, error)
	ListMovies(ctx context.Context, listID string, p Page) (*SearchResult, error)
}

// ListPager serves category listings ("/censored", "/fc2", "/video_codes/XXX",
// "/makers/XXX", tag filter pages).
type ListPager interface {
	CategoryMovies(ctx context.Context, q CategoryQuery) (*SearchResult, error)
}

// TagProvider lists available filter tags / genres.
type TagProvider interface {
	TagGroups(ctx context.Context, scope Category) ([]TagGroup, error)
}

// Authenticator can exchange credentials for a session (web cookie or app JWT).
type Authenticator interface {
	Login(ctx context.Context, cred Credentials) error
}

// RankingQuery asks for one ranking page.
type RankingQuery struct {
	Kind     RankingKind
	Period   Period
	Category Category
	// Filter narrows a ranking (API playback uses "high_score", web uses
	// nothing). Empty means the backend default.
	Filter FilterBy
	// Slice narrows a TOP250 request (ignored by other kinds).
	Slice Top250Slice
	// StartRank is the TOP250 offset used by the mobile API (1 based).
	StartRank int
	Page
}

// ReviewQuery asks for one page of a movie's comments.
type ReviewQuery struct {
	MovieID string
	Sort    SortBy // SortRelevance("hotly"), "latest"
	Filter  FilterBy
	Page
}

// CategoryQuery lists movies inside a bucket or a scoped entity page.
// Exactly one of Category / VideoCode / Maker / Publisher / Series / Director /
// ActorID should be set; Category alone lists a top-level bucket. TagIDs may
// stand alone or ride along an entity scope (the app API narrows via
// filter_by_tags).
type CategoryQuery struct {
	Category  Category
	VideoCode string // "SSIS" -> /video_codes/SSIS
	Maker     string // "zKW" or maker name -> /makers/zKW
	Series    string
	Director  string
	Publisher string
	ActorID   string // -> /actors/<id>
	// TagIDs applies tag filters, keyed by category id ("4" -> c4=15). It may
	// also combine with an entity scope (app API: filter_by_tags).
	TagIDs map[string]string
	// DownloadableOnly restricts to entries with torrents (f=download on
	// makers/video_codes, t=d on actors).
	DownloadableOnly bool
	WithSubtitle     bool
	// SoloOnly restricts to works where the actor appears alone (演员页
	// filter_tags 的「單體作品」main flag s；共演无对应 flag，需客户端差集）。
	SoloOnly bool
	// SortBy 请求 app API 服务端排序（/v1/movies/tags 的 sort_by/order_by）；
	// 空=端点默认（实体 release desc / 标签 hit desc）。web 后端无对应能力，
	// 回退路径由调用方客户端排序。
	SortBy SortBy
	Page
}

// Credentials carries login material for both backends.
type Credentials struct {
	Username string
	Password string
	// Cookie imports an existing web session instead of a password login.
	Cookie string
	// AppToken imports an existing mobile app JWT.
	AppToken string
}

// ReviewPage is one page of comments.
type ReviewPage struct {
	Reviews []Review `json:"reviews"`
	Current int      `json:"current_page"`
	Total   int      `json:"total"`
	Source  string   `json:"-"`
}

var (
	_ Backend   = (*apiBackend)(nil)
	_ Backend   = (*webBackend)(nil)
	_ ListPager = (*apiBackend)(nil)
	_ ListPager = (*webBackend)(nil)
)
