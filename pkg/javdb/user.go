package javdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// 用户态（标记 / 清单）基于 JavDB 移动端 API 的私有端点，2026-09 实测打通：
//
//	标记     POST   /v1/movies/{id}/reviews        form{status,score,content}
//	取消标记 DELETE /v1/movies/{id}/reviews/{rid}  rid 来自 v4 详情 data.movie.review.id
//	标记列表 GET    /v2/users/review_movies?status=&page=
//	我的清单 GET    /v1/lists?page=&sort_by=created（sort_by 必填，缺失报 500）
//	清单+影片 GET   /v1/lists/simple?movie_id=     每项带 has_movie（官方 App 弹窗数据源）
//	建清单   POST   /v1/lists                      form{name}
//	删清单   DELETE /v1/lists/{id}
//	改清单名 PATCH  /v1/lists/{id}                 form{name}
//	移出清单 POST   /v1/lists/{lid}/movie_actions  form{name,movie_id}（仅支持移除方向）
//
// 移动端 API 不存在"加入清单"端点（30+ 候选路径全部 404，逆向资料同样只含
// 移除方向），因此清单影片只能浏览与移除。

// MarkStatus is a user mark for a movie.
type MarkStatus string

const (
	// MarkWantWatch 想看。
	MarkWantWatch MarkStatus = "want_watch"
	// MarkWatched 看过。
	MarkWatched MarkStatus = "watched"
)

// Valid reports whether the status is one the mark endpoint accepts.
func (s MarkStatus) Valid() bool {
	return s == MarkWantWatch || s == MarkWatched
}

// UserMark is the persisted user mark for one movie.
type UserMark struct {
	// ID is the review record id (needed to remove the mark).
	ID     string     `json:"id,omitempty"`
	Status MarkStatus `json:"status,omitempty"`
}

// UserList is one of the user's own lists ("清單").
type UserList struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MoviesCount int    `json:"movies_count,omitempty"`
	IsDefault   bool   `json:"is_default,omitempty"`
	// HasMovie is only set by UserLists with a movieID: whether the movie is
	// already in this list.
	HasMovie bool `json:"has_movie,omitempty"`
}

// UserMarkedMovie is one entry of the user's want-watch / watched list.
type UserMarkedMovie struct {
	Movie
	Mark MarkStatus `json:"mark,omitempty"`
	// MarkedAt is when the mark was created (date part).
	MarkedAt string `json:"marked_at,omitempty"`
}

// ---------------------------------------------------------------------------
// apiBackend implementation
// ---------------------------------------------------------------------------

// apiSend issues a signed non-GET request with a form body and decodes the
// standard envelope. method is POST / PATCH / DELETE.
func (b *apiBackend) apiSend(ctx context.Context, method, path string, form url.Values, out any) error {
	data, err := b.apiSendRaw(ctx, method, path, form)
	if err != nil {
		return err
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("javdb: api %s: decode data: %w", path, err)
		}
	}
	return nil
}

// apiSendRaw issues a signed non-GET request and returns the raw data payload
// of the success envelope ("" when data is null).
func (b *apiBackend) apiSendRaw(ctx context.Context, method, path string, form url.Values) (json.RawMessage, error) {
	if !b.enabled() {
		return nil, fmt.Errorf("%w: api backend disabled", ErrUnsupported)
	}
	header := http.Header{}
	header.Set("jdsignature", b.t.sig.Get())
	header.Set("Accept", "application/json")
	header.Set("User-Agent", "Dart/3.5 (dart:io)")
	r := req{method: method, url: b.base + path, query: url.Values{}, header: header}
	if form != nil {
		r.body = []byte(form.Encode())
		r.contentType = "application/x-www-form-urlencoded; charset=utf-8"
	}
	body, status, err := b.t.do(ctx, r)
	if err != nil {
		return nil, err
	}
	var env apiEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("javdb: api %s: decode: %w (status %d)", path, err, status)
	}
	if env.Success != 1 {
		return env.Data, &APIError{Status: status, Code: env.Action, Message: env.Message, Path: path}
	}
	if string(env.Data) == "null" {
		return nil, nil
	}
	return env.Data, nil
}

// apiReviewPayload is the review record the mark endpoints exchange. The id is
// a large integer the app renders in scientific notation when decoded as
// float64, so it is kept as raw JSON and rendered as a string.
type apiReviewPayload struct {
	ID        json.RawMessage `json:"id"`
	Status    string          `json:"status"`
	CreatedAt json.RawMessage `json:"created_at"`
}

func (r apiReviewPayload) toUserMark() UserMark {
	return UserMark{ID: rawToString(r.ID), Status: MarkStatus(r.Status)}
}

// MarkMovie marks a movie 想看 / 看过 (POST /v1/movies/{id}/reviews).
// Re-marking with the other status flips the mark.
func (b *apiBackend) MarkMovie(ctx context.Context, movieID string, status MarkStatus) (*UserMark, error) {
	if movieID == "" {
		return nil, fmt.Errorf("%w: empty movie id", ErrInvalidQuery)
	}
	if !status.Valid() {
		return nil, fmt.Errorf("%w: mark status %q", ErrInvalidQuery, status)
	}
	form := url.Values{
		"status":  {string(status)},
		"score":   {"0"},
		"content": {""},
	}
	data, err := b.apiSendRaw(ctx, http.MethodPost, "/v1/movies/"+url.PathEscape(movieID)+"/reviews", form)
	if err != nil {
		return nil, err
	}
	mark := parseReviewPayload(data)
	if mark.ID == "" {
		return nil, fmt.Errorf("javdb: api mark %s: no review id in response (data: %.160s)", movieID, string(data))
	}
	return mark, nil
}

// parseReviewPayload digs the review object out of a mark response. The
// endpoint has been seen answering the bare review and wrapped variants.
func parseReviewPayload(data json.RawMessage) *UserMark {
	for _, v := range []struct {
		path string
	}{{"."}, {"review"}, {"data"}, {"data.review"}} {
		var probe map[string]json.RawMessage
		if v.path == "." {
			if err := json.Unmarshal(data, &probe); err != nil {
				continue
			}
		} else {
			cur := data
			ok := true
			for _, seg := range strings.Split(v.path, ".") {
				var m map[string]json.RawMessage
				if err := json.Unmarshal(cur, &m); err != nil {
					ok = false
					break
				}
				segData, exist := m[seg]
				if !exist {
					ok = false
					break
				}
				cur = segData
			}
			if !ok {
				continue
			}
			if err := json.Unmarshal(cur, &probe); err != nil {
				continue
			}
		}
		if id, ok := probe["id"]; ok && rawToString(id) != "" {
			return &UserMark{
				ID:     rawToString(id),
				Status: MarkStatus(rawToString(probe["status"])),
			}
		}
	}
	return nil
}

// UnmarkMovie removes the user's mark (DELETE /v1/movies/{id}/reviews/{rid}).
// The review id comes from the v4 detail payload; a movie without a mark
// reports ErrNotFound.
func (b *apiBackend) UnmarkMovie(ctx context.Context, movieID string) error {
	if movieID == "" {
		return fmt.Errorf("%w: empty movie id", ErrInvalidQuery)
	}
	rid, err := b.userReviewID(ctx, movieID)
	if err != nil {
		return err
	}
	return b.apiSend(ctx, http.MethodDelete,
		"/v1/movies/"+url.PathEscape(movieID)+"/reviews/"+url.PathEscape(rid), nil, nil)
}

// userReviewID reads the current user's review id from the v4 detail payload
// (data.movie.review.id — the review nests inside movie, not data).
func (b *apiBackend) userReviewID(ctx context.Context, movieID string) (string, error) {
	var out struct {
		Movie struct {
			Review apiReviewPayload `json:"review"`
		} `json:"movie"`
	}
	if err := b.getJSON(ctx, "/v4/movies/"+url.PathEscape(movieID), nil, &out); err != nil {
		return "", err
	}
	if out.Movie.Review.ID == nil {
		return "", fmt.Errorf("%w: movie %s has no user mark", ErrNotFound, movieID)
	}
	return rawToString(out.Movie.Review.ID), nil
}

// UserMovieMark reports the current user's mark for one movie (ErrNotFound
// when unmarked).
func (b *apiBackend) UserMovieMark(ctx context.Context, movieID string) (*UserMark, error) {
	if movieID == "" {
		return nil, fmt.Errorf("%w: empty movie id", ErrInvalidQuery)
	}
	var out struct {
		Movie struct {
			Review apiReviewPayload `json:"review"`
		} `json:"movie"`
	}
	if err := b.getJSON(ctx, "/v4/movies/"+url.PathEscape(movieID), nil, &out); err != nil {
		return nil, err
	}
	if out.Movie.Review.ID == nil {
		return nil, fmt.Errorf("%w: movie %s has no user mark", ErrNotFound, movieID)
	}
	mark := out.Movie.Review.toUserMark()
	return &mark, nil
}

// UserMarkedMovies lists the user's 想看 / 看过 movies
// (GET /v2/users/review_movies). One page per call. The endpoint filters by
// status server-side and does not echo a per-movie review, so every entry
// carries the requested status.
func (b *apiBackend) UserMarkedMovies(ctx context.Context, status MarkStatus, p Page) ([]UserMarkedMovie, error) {
	if !status.Valid() {
		return nil, fmt.Errorf("%w: mark status %q", ErrInvalidQuery, status)
	}
	params := url.Values{
		"status": {string(status)},
		"page":   {strconv.Itoa(p.pageOrDefault(1))},
	}
	var out struct {
		Movies []apiMovieDTO `json:"movies"`
	}
	if err := b.getJSON(ctx, "/v2/users/review_movies", params, &out); err != nil {
		return nil, err
	}
	movies := make([]UserMarkedMovie, 0, len(out.Movies))
	for _, m := range out.Movies {
		movies = append(movies, UserMarkedMovie{
			Movie: m.apiMovie.toMovie(b.t.Site()),
			Mark:  status,
		})
	}
	if len(movies) == 0 {
		return nil, fmt.Errorf("%w: %s list", ErrEmptyResult, status)
	}
	return movies, nil
}

// UserLists lists the user's own lists. With a movieID the /lists/simple
// variant is used and each list carries HasMovie; otherwise plain /lists is
// served (sort_by is mandatory there).
func (b *apiBackend) UserLists(ctx context.Context, movieID string, p Page) ([]UserList, error) {
	var out struct {
		Lists []UserList `json:"lists"`
	}
	if movieID == "" {
		params := url.Values{
			"page":    {strconv.Itoa(p.pageOrDefault(1))},
			"sort_by": {"created"},
		}
		if err := b.getJSON(ctx, "/v1/lists", params, &out); err != nil {
			return nil, err
		}
	} else {
		params := url.Values{
			"movie_id": {movieID},
		}
		if err := b.getJSON(ctx, "/v1/lists/simple", params, &out); err != nil {
			return nil, err
		}
	}
	if len(out.Lists) == 0 {
		return nil, fmt.Errorf("%w: user lists", ErrEmptyResult)
	}
	return out.Lists, nil
}

// CreateUserList creates a list and returns it with the server-assigned id.
func (b *apiBackend) CreateUserList(ctx context.Context, name string) (*UserList, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: empty list name", ErrInvalidQuery)
	}
	var out struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := b.apiSend(ctx, http.MethodPost, "/v1/lists", url.Values{"name": {name}}, &out); err != nil {
		return nil, err
	}
	if out.ID == "" {
		return nil, fmt.Errorf("javdb: api create list: no id in response")
	}
	return &UserList{ID: out.ID, Name: firstNonEmpty(out.Name, name)}, nil
}

// DeleteUserList deletes one of the user's lists.
func (b *apiBackend) DeleteUserList(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("%w: empty list id", ErrInvalidQuery)
	}
	return b.apiSend(ctx, http.MethodDelete, "/v1/lists/"+url.PathEscape(id), nil, nil)
}

// RenameUserList renames one of the user's lists (PATCH /v1/lists/{id}).
func (b *apiBackend) RenameUserList(ctx context.Context, id, name string) error {
	if id == "" || name == "" {
		return fmt.Errorf("%w: rename list needs id and name", ErrInvalidQuery)
	}
	return b.apiSend(ctx, http.MethodPatch, "/v1/lists/"+url.PathEscape(id), url.Values{"name": {name}}, nil)
}

// RemoveMovieFromList removes a movie from one of the user's lists
// (POST /v1/lists/{lid}/movie_actions). The endpoint needs both the list id
// and its name; the mobile API offers no "add" counterpart.
func (b *apiBackend) RemoveMovieFromList(ctx context.Context, listID, listName, movieID string) error {
	if listID == "" || listName == "" || movieID == "" {
		return fmt.Errorf("%w: removing from a list needs list id, name and movie id", ErrInvalidQuery)
	}
	form := url.Values{
		"name":     {listName},
		"movie_id": {movieID},
	}
	return b.apiSend(ctx, http.MethodPost, "/v1/lists/"+url.PathEscape(listID)+"/movie_actions", form, nil)
}

// ---------------------------------------------------------------------------
// Client facade
// ---------------------------------------------------------------------------

// userAPI resolves the api backend for user-state calls; they have no HTML
// counterpart, so there is no fallback.
func (c *Client) userAPI() (*apiBackend, error) {
	if c.api == nil || !c.api.enabled() {
		return nil, fmt.Errorf("%w: user state needs the api backend", ErrUnsupported)
	}
	return c.api, nil
}

// MarkMovie marks a movie 想看 / 看过 for the logged-in account.
func (c *Client) MarkMovie(ctx context.Context, movieID string, status MarkStatus) (*UserMark, error) {
	api, err := c.userAPI()
	if err != nil {
		return nil, err
	}
	return api.MarkMovie(ctx, movieID, status)
}

// UnmarkMovie removes the user's mark from a movie.
func (c *Client) UnmarkMovie(ctx context.Context, movieID string) error {
	api, err := c.userAPI()
	if err != nil {
		return err
	}
	return api.UnmarkMovie(ctx, movieID)
}

// UserMovieMark reports the user's mark for a movie (javdb.ErrNotFound when
// the movie is unmarked).
func (c *Client) UserMovieMark(ctx context.Context, movieID string) (*UserMark, error) {
	api, err := c.userAPI()
	if err != nil {
		return nil, err
	}
	return api.UserMovieMark(ctx, movieID)
}

// UserMarkedMovies lists one page of the user's 想看 / 看过 movies.
func (c *Client) UserMarkedMovies(ctx context.Context, status MarkStatus, p Page) ([]UserMarkedMovie, error) {
	api, err := c.userAPI()
	if err != nil {
		return nil, err
	}
	return api.UserMarkedMovies(ctx, status, p)
}

// UserLists lists the user's own lists; pass a movieID to get the
// has_movie-annotated variant for the movie's list-picker dialog.
func (c *Client) UserLists(ctx context.Context, movieID string, p Page) ([]UserList, error) {
	api, err := c.userAPI()
	if err != nil {
		return nil, err
	}
	return api.UserLists(ctx, movieID, p)
}

// CreateUserList creates a user list.
func (c *Client) CreateUserList(ctx context.Context, name string) (*UserList, error) {
	api, err := c.userAPI()
	if err != nil {
		return nil, err
	}
	return api.CreateUserList(ctx, name)
}

// DeleteUserList deletes one of the user's lists.
func (c *Client) DeleteUserList(ctx context.Context, id string) error {
	api, err := c.userAPI()
	if err != nil {
		return err
	}
	return api.DeleteUserList(ctx, id)
}

// RenameUserList renames one of the user's lists.
func (c *Client) RenameUserList(ctx context.Context, id, name string) error {
	api, err := c.userAPI()
	if err != nil {
		return err
	}
	return api.RenameUserList(ctx, id, name)
}

// RemoveMovieFromList removes a movie from one of the user's lists.
// The mobile API offers no "add to list" endpoint, so callers can only browse
// and remove.
func (c *Client) RemoveMovieFromList(ctx context.Context, listID, listName, movieID string) error {
	api, err := c.userAPI()
	if err != nil {
		return err
	}
	return api.RemoveMovieFromList(ctx, listID, listName, movieID)
}
