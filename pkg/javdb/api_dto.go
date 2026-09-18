package javdb

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// DTOs mirror the live mobile API payloads (verified 2026-09).
// Field types are deliberately loose: JavDB mixes numbers and numeric strings
// and returns null for absent values.

type apiMovie struct {
	ID               string          `json:"id"`
	Number           json.RawMessage `json:"number"`
	Title            string          `json:"title"`
	OriginTitle      string          `json:"origin_title"`
	ThumbURL         string          `json:"thumb_url"`
	CoverURL         string          `json:"cover_url"`
	Duration         json.RawMessage `json:"duration"`
	ReleaseDate      json.RawMessage `json:"release_date"`
	Score            json.RawMessage `json:"score"`
	MagnetsCount     int             `json:"magnets_count"`
	HasCNSub         bool            `json:"has_cnsub"`
	CanPlay          bool            `json:"can_play"`
	PlaySubtitle     int             `json:"play_subtitle"`
	HasPreviewVideo  bool            `json:"has_preview_video"`
	HasPreviewImages bool            `json:"has_preview_images"`
	NewMagnets       bool            `json:"new_magnets"`
	// 计数字段：列表端点可能省略（零值），详情端点必带；放在嵌入结构
	// 让列表行与 /v4 详情共用同一套解码。
	ReviewsCount   int `json:"reviews_count"`
	CommentsCount  int `json:"comments_count"`
	WantWatchCount int `json:"want_watch_count"`
	WatchedCount   int `json:"watched_count"`
	PreviewImages  []apiPreview   `json:"preview_images"`
	Tags           []apiNameID    `json:"tags"`
	Actors         json.RawMessage `json:"actors"`
}

type apiPreview struct {
	ThumbURL string `json:"thumb_url"`
	LargeURL string `json:"large_url"`
}

type apiNameID struct {
	ID   json.RawMessage `json:"id"`
	Name string          `json:"name"`
}

func (p apiNameID) idString() string {
	return rawToString(p.ID)
}

// apiMovieDTO is the richer /v4/movies/{id} payload.
type apiMovieDTO struct {
	apiMovie
	Type         int             `json:"type"`
	Summary      string          `json:"summary"`
	NumberLetter json.RawMessage `json:"number_letter"`
	MakerID      json.RawMessage `json:"maker_id"`
	MakerName       string          `json:"maker_name"`
	PublisherID     json.RawMessage `json:"publisher_id"`
	PublisherName   string          `json:"publisher_name"`
	SeriesID        json.RawMessage `json:"series_id"`
	SeriesName      string          `json:"series_name"`
	DirectorID      json.RawMessage `json:"director_id"`
	DirectorName    string          `json:"director_name"`
	PreviewVideoURL string          `json:"preview_video_url"`
	// 上榜记录在 app API 中出现过 string / []string / object 多种形态，
	// 无消费者，保留原始载荷避免形态变化炸掉整个详情解码。
	TopRankings    json.RawMessage `json:"top_rankings"`
	RelativeMovies []apiMovie      `json:"relative_movies"`
	Review         json.RawMessage `json:"review"`
}

func (m apiMovie) code() string { return NormalizeCode(rawToString(m.Number)) }

func (m apiMovie) toMovie(site string) Movie {
	mv := Movie{
		ID:           m.ID,
		Code:         m.code(),
		Title:        firstNonEmpty(m.Title, m.OriginTitle),
		OriginTitle:  m.OriginTitle,
		CoverURL:     FixImageURL(m.CoverURL),
		ThumbURL:     FixImageURL(m.ThumbURL),
		PosterURL:    posterFromCover(FixImageURL(m.CoverURL)),
		ReleaseDate:  rawToString(m.ReleaseDate),
		Duration:     int(ParseFloat(rawToString(m.Duration))),
		Score:        ParseFloat(rawToString(m.Score)),
		MagnetsCount: m.MagnetsCount,
		Comments:     m.CommentsCount,
		Wants:        m.WantWatchCount,
		HasCNSub:     m.HasCNSub,
		CanPlay:      m.CanPlay,
		NewMagnets:   m.NewMagnets,
		Source:       "api",
	}
	if mv.ID != "" {
		mv.Href = "/v/" + mv.ID
	}
	if mv.Code != "" && !IsPlausibleVideoCode(mv.Code) {
		// Defensive: never propagate a title blob as a code.
		mv.Code = ""
	}
	for _, t := range m.Tags {
		if t.Name != "" {
			// 题材名统一转简体展示（繁体源：JavDB 网页/app API）。
			mv.Tags = append(mv.Tags, ToSimplified(t.Name))
		}
	}
	for _, a := range parseActorNames(m.Actors) {
		mv.Actors = append(mv.Actors, a.Name)
	}
	_ = site // reserved: relative URLs could be absolutised here
	return mv
}

func (m apiMovieDTO) toDetail(site string) *Detail {
	mv := m.apiMovie.toMovie(site)
	d := &Detail{
		Movie:         mv,
		Summary:       m.Summary,
		PosterURL:     mv.CoverURL,
		CommentsCount: m.CommentsCount,
		ReviewsCount:  m.ReviewsCount,
		WantCount:     m.WantWatchCount,
		WatchedCount:  m.WatchedCount,
	}
	if m.PreviewVideoURL != "" {
		d.PreviewVideo = m.PreviewVideoURL
	}
	for _, p := range m.PreviewImages {
		if u := FixImageURL(p.LargeURL); u != "" {
			d.PreviewImages = append(d.PreviewImages, u)
		}
	}
	if id := rawToString(m.MakerID); id != "" || m.MakerName != "" {
		d.Maker = &Link{Name: m.MakerName, ID: id, Href: "/makers/" + id, Kind: "maker"}
	}
	if id := rawToString(m.PublisherID); id != "" || m.PublisherName != "" {
		d.Publisher = &Link{Name: m.PublisherName, ID: id, Href: "/publishers/" + id, Kind: "publisher"}
	}
	if id := rawToString(m.SeriesID); id != "" || m.SeriesName != "" {
		d.Series = &Link{Name: m.SeriesName, ID: id, Href: "/series/" + id, Kind: "series"}
	}
	if id := rawToString(m.DirectorID); id != "" || m.DirectorName != "" {
		d.Directors = append(d.Directors, Link{Name: m.DirectorName, ID: id, Href: "/directors/" + id, Kind: "director"})
	}
	for _, t := range m.Tags {
		d.Genres = append(d.Genres, Link{Name: ToSimplified(t.Name), ID: t.idString(), Href: "/tags/" + t.idString(), Kind: "tag"})
	}
	for _, a := range parseActorNames(m.Actors) {
		d.ActorCredits = append(d.ActorCredits, a)
	}
	for _, s := range m.RelativeMovies {
		d.FanartURLs = append(d.FanartURLs, FixImageURL(s.CoverURL))
	}
	if d.Title == "" {
		d.Title = d.OriginTitle
	}
	return d
}

// apiActorDTO is one entry of /v1/actors or one profile from /v1/actors/{id}.
type apiActorDTO struct {
	ID          string          `json:"id"`
	Type        int             `json:"type"`
	Name        string          `json:"name"`
	NameZhT     string          `json:"name_zht"`
	OtherName   string          `json:"other_name"`
	AvatarURL   string          `json:"avatar_url"`
	Uncensored  bool            `json:"uncensored"`
	Gender      json.RawMessage `json:"gender"`
	VideosCount int             `json:"videos_count"`
	Birthday    json.RawMessage `json:"birthday"`
	Age         json.RawMessage `json:"age"`
	Cons        json.RawMessage `json:"cons"`
	BloodType   string          `json:"blood_type"`
	Height      json.RawMessage `json:"height"`
	Bust        json.RawMessage `json:"bust"`
	Cup         string          `json:"cup"`
	Waist       json.RawMessage `json:"waist"`
	Hips        json.RawMessage `json:"hips"`
	Birthplace  string          `json:"birthplace"`
	TwitterID   string          `json:"twitter_id"`
	InstagramID string          `json:"instagram_id"`
}

func (a apiActorDTO) toActor(site string) Actor {
	out := Actor{
		ID:              a.ID,
		Name:            a.Name,
		NameTraditional: a.NameZhT,
		OtherName:       a.OtherName,
		AvatarURL:       FixImageURL(a.AvatarURL),
		Uncensored:      a.Uncensored,
		Gender:          apiGender(a.Gender),
		VideosCount:     a.VideosCount,
		Birthday:        rawToString(a.Birthday),
		Height:          int(ParseFloat(rawToString(a.Height))),
		Bust:            int(ParseFloat(rawToString(a.Bust))),
		Cup:             a.Cup,
		Waist:           int(ParseFloat(rawToString(a.Waist))),
		Hips:            int(ParseFloat(rawToString(a.Hips))),
		Birthplace:      a.Birthplace,
		Twitter:         a.TwitterID,
		Instagram:       a.InstagramID,
		Source:          "api",
	}
	if out.ID != "" {
		out.Href = "/actors/" + out.ID
	}
	_ = site
	return out
}

// apiGender maps the app's gender encoding (0 female / 1 male / null unknown).
func apiGender(raw json.RawMessage) string {
	switch rawToString(raw) {
	case "0":
		return "female"
	case "1":
		return "male"
	default:
		return ""
	}
}

type apiMagnet struct {
	Name       string          `json:"name"`
	Hash       string          `json:"hash"`
	Size       json.RawMessage `json:"size"`
	CNSub      bool            `json:"cnsub"`
	HD         bool            `json:"hd"`
	FilesCount int             `json:"files_count"`
	CreatedAt  json.RawMessage `json:"created_at"`
	PikPakURL  string          `json:"pikpak_url"`
}

func (m apiMagnet) toMagnet() Magnet {
	sizeBytes := int64(ParseFloat(rawToString(m.Size)))
	text := rawToString(m.Size)
	out := Magnet{
		Name:      m.Name,
		Hash:      m.Hash,
		CNSub:     m.CNSub,
		HD:        m.HD,
		Files:     m.FilesCount,
		PikPakURL: m.PikPakURL,
		CreatedAt: strings.SplitN(textDate(m.CreatedAt), "T", 2)[0],
		SizeBytes: sizeBytes,
		Source:    "api",
	}
	// The endpoint reports bytes, so keep a human-readable form too. Note that
	// the app's "size" is the size of the .torrent metadata (a few KB), not the
	// video total the HTML page shows - the two backends are therefore not
	// comparable on this field, which is why magnet ordering prefers the name
	// markers over size.
	if out.SizeBytes > 0 {
		out.SizeText = humanBytes(out.SizeBytes)
	} else {
		out.SizeText = text
		out.SizeBytes = ParseSizeBytes(text)
	}
	if out.CNSub {
		out.Tags = append(out.Tags, "中字")
	}
	if out.HD {
		out.Tags = append(out.Tags, "高清")
	}
	return out
}

type apiReview struct {
	ID           json.RawMessage `json:"id"`
	UserID       json.RawMessage `json:"user_id"`
	Username     string          `json:"username"`
	WatchedCount int             `json:"watched_count"`
	Status       string          `json:"status"`
	StatusTitle  string          `json:"status_title"`
	Score        json.RawMessage `json:"score"`
	Content      string          `json:"content"`
	LikesCount   int             `json:"likes_count"`
	Liked        bool            `json:"liked"`
	CreatedAt    json.RawMessage `json:"created_at"`
}

func (r apiReview) toReview() Review {
	return Review{
		ID:      rawToString(r.ID),
		Author:  r.Username,
		Content: r.Content,
		Rating:  ParseFloat(rawToString(r.Score)),
		Date:    strings.SplitN(textDate(r.CreatedAt), "T", 2)[0],
		Likes:   r.LikesCount,
		Source:  "api",
	}
}

type apiTagGroup struct {
	Category   string      `json:"category"`
	CategoryID string      `json:"category_id"`
	Tags       []apiNameID `json:"tags"`
	// HTML pages use these keys instead; shared for convenience.
	Name string          `json:"name"`
	ID   json.RawMessage `json:"id"`
}

func (g apiTagGroup) toGroup() TagGroup {
	out := TagGroup{CategoryID: g.CategoryID, Name: ToSimplified(g.Category)}
	for _, t := range g.Tags {
		out.Options = append(out.Options, TagOption{Name: ToSimplified(t.Name), ID: t.idString()})
	}
	return out
}

// ---------------------------------------------------------------------------
// lenient decode helpers
// ---------------------------------------------------------------------------

// rawToString renders any JSON scalar as a string ("4.02", "2026-07-15", "0").
func rawToString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	s := strings.TrimSpace(string(raw))
	if s == "null" {
		return ""
	}
	if strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) && len(s) >= 2 {
		var out string
		if err := json.Unmarshal(raw, &out); err == nil {
			return out
		}
	}
	return s
}

// textDate normalises a date that may be "2026-07-16", a unix timestamp or an
// RFC3339 string.
func textDate(raw json.RawMessage) string {
	s := rawToString(raw)
	if s == "" {
		return ""
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
		if len(s) <= 10 {
			n *= 1000 // seconds -> ms for the common "created_at" epoch form
		}
		return formatMillis(n)
	}
	return s
}

// parseActorNames accepts either ["name", ...] or [{name:...}, ...] or null.
func parseActorNames(raw json.RawMessage) []Actor {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return nil
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err == nil {
		out := make([]Actor, 0, len(names))
		for _, n := range names {
			out = append(out, Actor{Name: n})
		}
		return out
	}
	var objs []apiActorDTO
	if err := json.Unmarshal(raw, &objs); err == nil {
		out := make([]Actor, 0, len(objs))
		for _, o := range objs {
			out = append(out, o.toActor(""))
		}
		return out
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// humanBytes renders bytes as "1.24 GB".
func humanBytes(b int64) string {
	if b <= 0 {
		return ""
	}
	const unit = 1024
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(b)
	i := 0
	for f >= unit && i < len(units)-1 {
		f /= unit
		i++
	}
	return fmt.Sprintf("%.2f %s", f, units[i])
}
