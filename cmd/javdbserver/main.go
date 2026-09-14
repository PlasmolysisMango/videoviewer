// Package main implements a local HTTP server that exposes the javdb pkg
// capabilities as a REST API for Flutter frontend consumption.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"videoviewer/pkg/av"
	"videoviewer/pkg/javdb"
)

// server holds the javdb client, av client, and configuration.
type server struct {
	javdb *javdb.Client
	av    *av.Client
	addr  string
	dlDir string // download directory
}

// response helpers

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// CORS middleware for Flutter Web debugging.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// auth middleware checks for a valid Bearer token.
// For now, we just check if the client has a token set.
func (s *server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for login endpoint
		if r.URL.Path == "/api/login" {
			next(w, r)
			return
		}
		// Check if client has token (simplified - in production, validate JWT)
		cookie, token := s.javdb.Session()
		if cookie == "" && token == "" {
			writeError(w, http.StatusUnauthorized, "login required")
			return
		}
		next(w, r)
	}
}

// API handlers

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
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

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
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

func (s *server) handleMovie(w http.ResponseWriter, r *http.Request) {
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

func (s *server) handleRanking(w http.ResponseWriter, r *http.Request) {
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

func (s *server) handleMagnets(w http.ResponseWriter, r *http.Request) {
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

func (s *server) handleTags(w http.ResponseWriter, r *http.Request) {
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

func (s *server) handleActor(w http.ResponseWriter, r *http.Request) {
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

func (s *server) handleAVSearch(w http.ResponseWriter, r *http.Request) {
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

func (s *server) handleAVResolve(w http.ResponseWriter, r *http.Request) {
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

func (s *server) handleAVPlay(w http.ResponseWriter, r *http.Request) {
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

func (s *server) handleAVDownload(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "video code required")
		return
	}
	code := parts[3]
	source := r.URL.Query().Get("source")

	if s.dlDir == "" {
		writeError(w, http.StatusBadRequest, "download directory not configured")
		return
	}

	opt := av.DownloadOptions{
		Source:      source,
		RemuxToMP4:  true,
		Concurrency: 8,
	}

	result, err := s.av.Download(r.Context(), code, s.dlDir, opt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *server) handleAVDetail(w http.ResponseWriter, r *http.Request) {
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
func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
	})
}

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	apiBase := flag.String("api-base", javdb.DefaultAPIBase, "JavDB API base URL")
	token := flag.String("token", os.Getenv("JAVDB_TOKEN"), "App JWT token (or set JAVDB_TOKEN)")
	cookie := flag.String("cookie", os.Getenv("JAVDB_COOKIE"), "Web session cookie (or set JAVDB_COOKIE)")
	dlDir := flag.String("dl-dir", os.Getenv("JAVDB_DL_DIR"), "Download directory (or set JAVDB_DL_DIR)")
	flag.Parse()

	// Build javdb client options
	opts := []javdb.Option{
		javdb.WithAPIBase(*apiBase),
		javdb.WithSites("https://javdb.com"),
	}
	if *token != "" {
		opts = append(opts, javdb.WithAppToken(*token))
	}
	if *cookie != "" {
		opts = append(opts, javdb.WithCookie(*cookie))
	}

	javdbClient, err := javdb.New(opts...)
	if err != nil {
		log.Fatalf("create javdb client: %v", err)
	}

	// Build av client (MissAV/Jable/HohoJ)
	avClient, err := av.NewClient(av.ClientOptions{})
	if err != nil {
		log.Fatalf("create av client: %v", err)
	}

	srv := &server{
		javdb: javdbClient,
		av:    avClient,
		addr:  *addr,
		dlDir: *dlDir,
	}

	// Setup routes using standard library ServeMux (Go 1.22+ pattern matching)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", srv.handleHealth)
	mux.HandleFunc("POST /api/login", srv.handleLogin)
	// JavDB endpoints
	mux.HandleFunc("GET /api/search", srv.auth(srv.handleSearch))
	mux.HandleFunc("GET /api/movie/", srv.auth(srv.handleMovie))
	mux.HandleFunc("GET /api/ranking/", srv.auth(srv.handleRanking))
	mux.HandleFunc("GET /api/magnets/", srv.auth(srv.handleMagnets))
	mux.HandleFunc("GET /api/tags", srv.auth(srv.handleTags))
	mux.HandleFunc("GET /api/actor/", srv.auth(srv.handleActor))
	// AV endpoints (MissAV/Jable/HohoJ for playback and download)
	mux.HandleFunc("GET /api/av/search", srv.handleAVSearch)
	mux.HandleFunc("GET /api/av/detail/", srv.handleAVDetail)
	mux.HandleFunc("GET /api/av/resolve/", srv.handleAVResolve)
	mux.HandleFunc("GET /api/av/play/", srv.handleAVPlay)
	mux.HandleFunc("POST /api/av/download/", srv.handleAVDownload)

	// Apply CORS middleware
	handler := cors(mux)

	httpSrv := &http.Server{
		Addr:         *addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		httpSrv.Shutdown(ctx)
	}()

	log.Printf("JavDB HTTP server listening on %s", *addr)
	log.Printf("API base: %s", *apiBase)
	if *token != "" {
		log.Printf("App token: configured")
	}
	if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}
	log.Println("server stopped")
}
