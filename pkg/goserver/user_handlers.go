package goserver

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"videoviewer/pkg/javdb"
)

// 用户态端点：标记（想看/看过）与清单，数据与 JavDB 登录账号双向同步。
// JWT 失效（约一天）时，若保存过凭据则自动重登一次并重试原请求，
// 前端无感知；无凭据时返回 401 让前端引导登录。

// authFailure reports whether err means "the JavDB session expired / is not
// logged in". Covers both the transport-level 401/login wall (wrapped into
// ErrAuthRequired) and the app API's success=0 login envelopes.
func authFailure(err error) bool {
	if err == nil || errors.Is(err, javdb.ErrEmptyResult) {
		return false
	}
	if errors.Is(err, javdb.ErrAuthRequired) {
		return true
	}
	var apiErr *javdb.APIError
	if errors.As(err, &apiErr) {
		return strings.Contains(apiErr.Message, "登錄") ||
			strings.Contains(apiErr.Message, "登录") ||
			strings.Contains(apiErr.Message, "log in") ||
			strings.Contains(apiErr.Message, "login")
	}
	return false
}

// relogin logs back in with the saved credentials, refreshing the persisted
// session on success.
func (s *Server) relogin(ctx context.Context) error {
	s.mu.Lock()
	user, pass := s.username, s.password
	s.mu.Unlock()
	if user == "" || pass == "" {
		return errors.New("no saved credentials")
	}
	log.Printf("goserver: JavDB session expired, re-logging in as %s", user)
	if err := s.javdb.Login(ctx, javdb.Credentials{Username: user, Password: pass}); err != nil {
		return err
	}
	cookie, token := s.javdb.Session()
	saveSession(cookie, token, user, pass)
	return nil
}

// withReauth runs fn once; on an auth failure with saved credentials it
// re-logs-in and retries exactly once.
func (s *Server) withReauth(ctx context.Context, fn func(context.Context) error) error {
	err := fn(ctx)
	if !authFailure(err) {
		return err
	}
	if lerr := s.relogin(ctx); lerr != nil {
		log.Printf("goserver: auto re-login failed: %v", lerr)
		return err
	}
	return fn(ctx)
}

// writeUserErr maps a user-state error onto the HTTP status: 401 for login
// failures (frontend shows the login page), 404 for missing resources, 500
// otherwise.
func writeUserErr(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
	case authFailure(err):
		writeError(w, http.StatusUnauthorized, "login required")
	case errors.Is(err, javdb.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, javdb.ErrInvalidQuery):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// ---------------------------------------------------------------------------
// marks (想看 / 看过)
// ---------------------------------------------------------------------------

func (s *Server) handleUserMarksGet(w http.ResponseWriter, r *http.Request) {
	status := javdb.MarkStatus(r.URL.Query().Get("status"))
	if status == "" {
		status = javdb.MarkWantWatch
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	var movies []javdb.UserMarkedMovie
	err := s.withReauth(r.Context(), func(ctx context.Context) error {
		var e error
		movies, e = s.javdb.UserMarkedMovies(ctx, status, javdb.Page{Page: page, Limit: 30})
		return e
	})
	if errors.Is(err, javdb.ErrEmptyResult) {
		// 空列表是正常态，不是错误。
		writeJSON(w, http.StatusOK, map[string]any{"movies": []javdb.UserMarkedMovie{}, "status": status, "page": page})
		return
	}
	if err != nil {
		writeUserErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"movies": movies, "status": status, "page": page})
}

func (s *Server) handleUserMarkSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MovieID string `json:"movie_id"`
		Status  string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.MovieID == "" {
		writeError(w, http.StatusBadRequest, "movie_id required")
		return
	}
	status := javdb.MarkStatus(body.Status)
	if !status.Valid() {
		writeError(w, http.StatusBadRequest, "status must be want_watch or watched")
		return
	}
	var mark *javdb.UserMark
	err := s.withReauth(r.Context(), func(ctx context.Context) error {
		var e error
		mark, e = s.javdb.MarkMovie(ctx, body.MovieID, status)
		return e
	})
	if err != nil {
		writeUserErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mark": mark})
}

func (s *Server) handleUserMarkClear(w http.ResponseWriter, r *http.Request) {
	movieID := r.URL.Query().Get("movie_id")
	if movieID == "" {
		writeError(w, http.StatusBadRequest, "movie_id required")
		return
	}
	err := s.withReauth(r.Context(), func(ctx context.Context) error {
		e := s.javdb.UnmarkMovie(ctx, movieID)
		if errors.Is(e, javdb.ErrNotFound) {
			return nil // 本来就没有标记，幂等成功
		}
		return e
	})
	if err != nil {
		writeUserErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

// ---------------------------------------------------------------------------
// lists (清單)
// ---------------------------------------------------------------------------

func (s *Server) handleUserListsGet(w http.ResponseWriter, r *http.Request) {
	movieID := r.URL.Query().Get("movie_id")
	var lists []javdb.UserList
	err := s.withReauth(r.Context(), func(ctx context.Context) error {
		var e error
		lists, e = s.javdb.UserLists(ctx, movieID, javdb.Page{})
		return e
	})
	if errors.Is(err, javdb.ErrEmptyResult) {
		writeJSON(w, http.StatusOK, map[string]any{"lists": []javdb.UserList{}})
		return
	}
	if err != nil {
		writeUserErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lists": lists})
}

func (s *Server) handleUserListCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	list, err := s.withReauthList(r.Context(), func(ctx context.Context) (*javdb.UserList, error) {
		return s.javdb.CreateUserList(ctx, strings.TrimSpace(body.Name))
	})
	if err != nil {
		writeUserErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"list": list})
}

func (s *Server) handleUserListDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}
	err := s.withReauth(r.Context(), func(ctx context.Context) error {
		return s.javdb.DeleteUserList(ctx, id)
	})
	if err != nil {
		writeUserErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

func (s *Server) handleUserListRename(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || id == "" || strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, "id and name required")
		return
	}
	err := s.withReauth(r.Context(), func(ctx context.Context) error {
		return s.javdb.RenameUserList(ctx, id, strings.TrimSpace(body.Name))
	})
	if err != nil {
		writeUserErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

func (s *Server) handleUserListRemoveMovie(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		MovieID  string `json:"movie_id"`
		ListName string `json:"list_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || id == "" || body.MovieID == "" || body.ListName == "" {
		writeError(w, http.StatusBadRequest, "id, movie_id and list_name required")
		return
	}
	err := s.withReauth(r.Context(), func(ctx context.Context) error {
		return s.javdb.RemoveMovieFromList(ctx, id, body.ListName, body.MovieID)
	})
	if err != nil {
		writeUserErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

// withReauthList is withReauth for calls returning a value.
func (s *Server) withReauthList(ctx context.Context, fn func(context.Context) (*javdb.UserList, error)) (*javdb.UserList, error) {
	var out *javdb.UserList
	err := s.withReauth(ctx, func(ctx context.Context) error {
		var e error
		out, e = fn(ctx)
		return e
	})
	return out, err
}
