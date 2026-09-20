package javdb

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
)

// userStub wires a Client at a stub API (no web backend: user state is
// API-only, so there must be no silent fallback in the tested paths).
func userStub(t *testing.T, routes map[string]string) (*Client, *stub) {
	t.Helper()
	st := newStub(t, routerHandler(routes))
	c, err := New(WithAPIBase(st.APIBase()), WithSites())
	requireNoErr(t, err)
	return c, st
}

func TestMarkMovie(t *testing.T) {
	routes := map[string]string{
		"/api/v1/movies/abc123/reviews": apiBody(`{"id":252151918,"status":"want_watch","score":0,"content":""}`),
	}
	c, st := userStub(t, routes)
	mark, err := c.MarkMovie(context.Background(), "abc123", MarkWantWatch)
	requireNoErr(t, err)
	if mark.ID != "252151918" {
		t.Fatalf("mark id = %q, want 252151918", mark.ID)
	}
	if mark.Status != MarkWantWatch {
		t.Fatalf("status = %q", mark.Status)
	}
	rec := st.lastRequest(t, "/api/v1/movies/abc123/reviews")
	if rec.Method != http.MethodPost {
		t.Fatalf("method = %s", rec.Method)
	}
	if got := rec.Form.Get("status"); got != "want_watch" {
		t.Fatalf("form status = %q", got)
	}
	if got := rec.Form.Get("score"); got != "0" {
		t.Fatalf("form score = %q", got)
	}
	if _, ok := rec.Form["content"]; !ok {
		t.Fatal("form must carry content")
	}
}

func TestMarkMovieRejectsBadInput(t *testing.T) {
	c, _ := userStub(t, nil)
	if _, err := c.MarkMovie(context.Background(), "", MarkWatched); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("empty id: %v", err)
	}
	if _, err := c.MarkMovie(context.Background(), "x", MarkStatus("liked")); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("bad status: %v", err)
	}
}

func TestUnmarkMovie(t *testing.T) {
	routes := map[string]string{
		"/api/v4/movies/abc123": apiBody(`{"movie":{"id":"abc123","review":{"id":252151918,"status":"watched"}}}`),
		// large ids must survive as strings, not scientific notation
		"/api/v1/movies/abc123/reviews/252151918": apiBody(`{"id":252151918,"deleted":true}`),
	}
	c, st := userStub(t, routes)
	if err := c.UnmarkMovie(context.Background(), "abc123"); err != nil {
		t.Fatalf("unmark: %v", err)
	}
	if n := st.count("/api/v4/movies/abc123"); n != 1 {
		t.Fatalf("detail hits = %d", n)
	}
	rec := st.lastRequest(t, "/api/v1/movies/abc123/reviews/252151918")
	if rec.Method != http.MethodDelete {
		t.Fatalf("method = %s, want DELETE", rec.Method)
	}
}

func TestUnmarkMovieWithoutMark(t *testing.T) {
	routes := map[string]string{
		"/api/v4/movies/abc123": apiBody(`{"movie":{"id":"abc123","review":null}}`),
	}
	c, _ := userStub(t, routes)
	err := c.UnmarkMovie(context.Background(), "abc123")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestUserMovieMark(t *testing.T) {
	routes := map[string]string{
		"/api/v4/movies/abc123": apiBody(`{"movie":{"id":"abc123","review":{"id":99,"status":"watched"}}}`),
	}
	c, _ := userStub(t, routes)
	mark, err := c.UserMovieMark(context.Background(), "abc123")
	requireNoErr(t, err)
	if mark.Status != MarkWatched || mark.ID != "99" {
		t.Fatalf("mark = %+v", mark)
	}
}

func TestUserMarkedMovies(t *testing.T) {
	routes := map[string]string{
		"/api/v2/users/review_movies": apiBody(`{"movies":[
			{"id":"m1","number":"SSIS-414","title":"t1","review":{"id":252151918,"status":"want_watch","created_at":1750000000}},
			{"id":"m2","number":"MIDV-002","title":"t2","review":{"id":252151919,"status":"watched","created_at":1750000100}}
		]}`),
	}
	c, st := userStub(t, routes)
	got, err := c.UserMarkedMovies(context.Background(), MarkWantWatch, Page{Page: 2})
	requireNoErr(t, err)
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Code != "SSIS-414" || got[0].Mark != MarkWantWatch {
		t.Fatalf("entry0 = %+v", got[0])
	}
	// 端点按 status 服务端过滤，条目不回显 review：所有条目都携带请求的 status。
	if got[1].Mark != MarkWantWatch {
		t.Fatalf("entry1 mark = %q", got[1].Mark)
	}
	rec := st.lastRequest(t, "/api/v2/users/review_movies")
	if rec.Query.Get("status") != "want_watch" || rec.Query.Get("page") != "2" {
		t.Fatalf("query = %s", rec.Query.Encode())
	}
}

func TestUserMarkedMoviesBadStatus(t *testing.T) {
	c, _ := userStub(t, nil)
	if _, err := c.UserMarkedMovies(context.Background(), MarkStatus("x"), Page{}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("err = %v", err)
	}
}

func TestUserLists(t *testing.T) {
	routes := map[string]string{
		"/api/v1/lists": apiBody(`{"lists":[
			{"id":"w3ZV2","name":"預設清單","movies_count":40,"is_default":true},
			{"id":"Lxyz","name":"我的最愛","movies_count":3}
		]}`),
		"/api/v1/lists/simple": apiBody(`{"lists":[
			{"id":"w3ZV2","name":"預設清單","movies_count":40,"has_movie":false},
			{"id":"Lxyz","name":"我的最愛","movies_count":3,"has_movie":true}
		]}`),
	}
	c, st := userStub(t, routes)

	lists, err := c.UserLists(context.Background(), "", Page{})
	requireNoErr(t, err)
	if len(lists) != 2 || lists[0].ID != "w3ZV2" || !lists[0].IsDefault {
		t.Fatalf("lists = %+v", lists)
	}
	rec := st.lastRequest(t, "/api/v1/lists")
	if rec.Query.Get("sort_by") != "created" {
		t.Fatalf("sort_by = %q (mandatory)", rec.Query.Get("sort_by"))
	}

	lists, err = c.UserLists(context.Background(), "m1", Page{})
	requireNoErr(t, err)
	rec = st.lastRequest(t, "/api/v1/lists/simple")
	if rec.Query.Get("movie_id") != "m1" {
		t.Fatalf("movie_id = %q", rec.Query.Get("movie_id"))
	}
	if !lists[1].HasMovie {
		t.Fatalf("simple lists = %+v", lists)
	}
}

func TestCreateDeleteRenameList(t *testing.T) {
	routes := map[string]string{
		"/api/v1/lists":           apiBody(`{"id":"Lnew","name":"vv"}`),
		"/api/v1/lists/Lnew":      apiBody(`{"deleted":true}`),
		"/api/v1/lists/Lrenamed":  apiBody(`{"id":"Lrenamed"}`),
		"/api/v1/lists/Lrenamed2": apiBody(`{"id":"Lrenamed2"}`),
	}
	c, st := userStub(t, routes)

	l, err := c.CreateUserList(context.Background(), "vv")
	requireNoErr(t, err)
	if l.ID != "Lnew" || l.Name != "vv" {
		t.Fatalf("list = %+v", l)
	}
	rec := st.lastRequest(t, "/api/v1/lists")
	if rec.Method != http.MethodPost || rec.Form.Get("name") != "vv" {
		t.Fatalf("create req = %s %s", rec.Method, rec.Form.Encode())
	}

	if err := c.DeleteUserList(context.Background(), "Lnew"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	rec = st.lastRequest(t, "/api/v1/lists/Lnew")
	if rec.Method != http.MethodDelete {
		t.Fatalf("delete method = %s", rec.Method)
	}

	if err := c.RenameUserList(context.Background(), "Lrenamed", " newName "); err != nil {
		t.Fatalf("rename: %v", err)
	}
	rec = st.lastRequest(t, "/api/v1/lists/Lrenamed")
	if rec.Method != http.MethodPatch || rec.Form.Get("name") != " newName " {
		t.Fatalf("rename req = %s %s", rec.Method, rec.Form.Encode())
	}

	if err := c.RenameUserList(context.Background(), "", "n"); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("rename empty id: %v", err)
	}
	if _, err := c.CreateUserList(context.Background(), ""); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("create empty name: %v", err)
	}
}

func TestRemoveMovieFromList(t *testing.T) {
	routes := map[string]string{
		"/api/v1/lists/Lxyz/movie_actions": apiBody(`{"message":"已從清單中移除"}`),
	}
	c, st := userStub(t, routes)
	if err := c.RemoveMovieFromList(context.Background(), "Lxyz", "我的最愛", "m1"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	rec := st.lastRequest(t, "/api/v1/lists/Lxyz/movie_actions")
	if rec.Method != http.MethodPost {
		t.Fatalf("method = %s", rec.Method)
	}
	if rec.Form.Get("name") != "我的最愛" || rec.Form.Get("movie_id") != "m1" {
		t.Fatalf("form = %s", rec.Form.Encode())
	}
	if err := c.RemoveMovieFromList(context.Background(), "Lxyz", "", "m1"); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("missing name: %v", err)
	}
}

func TestUserStateNeedsAPI(t *testing.T) {
	// API backend disabled: user-state calls must fail instead of falling back.
	st := newStub(t, routerHandler(nil))
	c, err := New(WithAPIBase(""), WithSites(st.URL()))
	requireNoErr(t, err)
	_, err = c.MarkMovie(context.Background(), "m", MarkWatched)
	requireSentinel(t, err, ErrUnsupported)
	if err := c.UnmarkMovie(context.Background(), "m"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unmark: %v", err)
	}
}

func TestAPIErrorEnvelope(t *testing.T) {
	// success=0 answers (login wall style) must surface as APIError.
	routes := map[string]string{
		"/api/v1/movies/abc123/reviews": apiErrBody("Authentication", "請登錄帳號"),
	}
	c, _ := userStub(t, routes)
	_, err := c.MarkMovie(context.Background(), "abc123", MarkWatched)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want APIError, got %v", err)
	}
	if apiErr.Message != "請登錄帳號" {
		t.Fatalf("message = %q", apiErr.Message)
	}
}

// guard against unused import when the suite shrinks
var _ = url.Values{}
