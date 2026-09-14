package goserver

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"videoviewer/pkg/av"
	"videoviewer/pkg/javdb"
)

// API handlers

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password required")
		return
	}

	cred := javdb.Credentials{Username: req.Username, Password: req.Password}
	if err := s.javdb.Login(r.Context(), cred); err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	_, token := s.javdb.Session()
	writeJSON(w, http.StatusOK, map[string]string{
		"token":    token,
		"username": req.Username,
	})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "q parameter required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 20
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}

	results, err := s.javdb.SearchMovies(r.Context(), q, javdb.WithPage(page, limit))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"movies":  results.Movies,
		"page":    results.Current,
		"maxPage": results.MaxPage,
	})
}

func (s *Server) handleMovie(w http.ResponseWriter, r *http.Request) {
	// Extract ID from path: /api/movie/{id}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "movie ID required")
		return
	}
	id := parts[3]

	movie, err := s.javdb.Movie(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	// Also fetch magnets
	magnets, err := s.javdb.BestMagnet(r.Context(), id)
	if err != nil {
		// Non-fatal, movie detail still useful
		magnets = nil
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"movie":   movie,
		"magnets": magnets,
	})
}

func (s *Server) handleRanking(w http.ResponseWriter, r *http.Request) {
	// Extract kind from path: /api/ranking/{kind}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "ranking kind required")
		return
	}
	kind := javdb.RankingKind(parts[3])

	category := javdb.Category(r.URL.Query().Get("category"))
	period := javdb.Period(r.URL.Query().Get("period"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 20
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}

	q := javdb.RankingQuery{
		Kind:     kind,
		Category: category,
		Period:   period,
		Page:     javdb.Page{Page: page, Limit: limit},
	}

	ranking, err := s.javdb.Ranking(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"movies":  ranking.Movies,
		"actors":  ranking.Actors,
		"page":    ranking.Page,
		"maxPage": ranking.MaxPage,
	})
}

func (s *Server) handleMagnets(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "movie ID required")
		return
	}
	id := parts[3]

	magnets, err := s.javdb.BestMagnet(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"magnets": magnets,
	})
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	tags, err := s.javdb.TagGroups(r.Context(), javdb.Category(category))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tags": tags,
	})
}

func (s *Server) handleActor(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "actor ID required")
		return
	}
	id := parts[3]

	actor, err := s.javdb.Actor(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"actor": actor,
	})
}

// AV handlers (MissAV/Jable/HohoJ for playback and download)

func (s *Server) handleAVSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "q parameter required")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 20
	}
	source := r.URL.Query().Get("source")

	videos, err := s.av.Search(r.Context(), av.Query{
		Keyword: q,
		Page:    page,
		Limit:   limit,
		Source:  source,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"videos": videos,
		"page":   page,
	})
}

func (s *Server) handleAVResolve(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "video code required")
		return
	}
	code := parts[3]
	source := r.URL.Query().Get("source")

	streams, err := s.av.Resolve(r.Context(), code, source)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"streams": streams,
	})
}

func (s *Server) handleAVPlay(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "video code required")
		return
	}
	code := parts[3]
	source := r.URL.Query().Get("source")

	stream, err := s.av.Play(r.Context(), code, av.DownloadOptions{Source: source})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"stream": stream,
	})
}

func (s *Server) handleAVDownload(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "video code required")
		return
	}
	code := parts[3]
	source := r.URL.Query().Get("source")

	if s.cfg.DownloadDir == "" {
		writeError(w, http.StatusBadRequest, "download directory not configured")
		return
	}

	opt := av.DownloadOptions{
		Source:      source,
		RemuxToMP4:  true,
		Concurrency: 8,
	}

	result, err := s.av.Download(r.Context(), code, s.cfg.DownloadDir, opt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleAVDetail(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "video code required")
		return
	}
	code := parts[3]
	source := r.URL.Query().Get("source")

	video, err := s.av.Detail(r.Context(), code, source)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"video": video,
	})
}

// health check endpoint
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"time":    time.Now().Format(time.RFC3339),
		"version": "1.0.0",
	})
}

// handleLogUpload accepts log data from Flutter clients and stores it
func (s *Server) handleLogUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	defer r.Body.Close()

	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "empty body")
		return
	}

	// Create log directory if not exists
	logDir := filepath.Join(s.cfg.DownloadDir, "logs")
	if s.cfg.DownloadDir == "" {
		logDir = filepath.Join(os.TempDir(), "videoviewer-logs")
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create log dir")
		return
	}

	// Generate filename with timestamp
	timestamp := time.Now().Format("20060102_150405")
	filename := filepath.Join(logDir, fmt.Sprintf("client_%s.json", timestamp))

	// Write log to file
	if err := os.WriteFile(filename, body, 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write log")
		return
	}

	log.Printf("Log uploaded: %s (%d bytes)", filename, len(body))
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"file":   filename,
	})
}
