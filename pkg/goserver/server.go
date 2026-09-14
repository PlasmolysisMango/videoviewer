// Package goserver provides an embeddable HTTP server for the videoviewer app.
// It exposes pkg/javdb and pkg/av capabilities as a REST API for Flutter frontend.
//
// This package is designed to be used in two ways:
//   - As a standalone executable via cmd/javdbserver
//   - Embedded in Flutter app via gomobile (Android) or subprocess (Windows)
package goserver

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"videoviewer/pkg/av"
	"videoviewer/pkg/javdb"
)

// Config holds the server configuration.
type Config struct {
	// Addr is the HTTP listen address (e.g., "127.0.0.1:18888")
	Addr string
	// APIBase is the JavDB API base URL
	APIBase string
	// Token is the JavDB App JWT token
	Token string
	// Cookie is the JavDB web session cookie
	Cookie string
	// DownloadDir is the directory for downloaded files
	DownloadDir string
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Addr:    "127.0.0.1:18888",
		APIBase: javdb.DefaultAPIBase,
	}
}

// Server holds the javdb client, av client, and configuration.
type Server struct {
	javdb *javdb.Client
	av    *av.Client
	cfg   Config

	httpServer *http.Server
	mu         sync.Mutex
	running    bool
}

// New creates a new Server with the given configuration.
func New(cfg Config) (*Server, error) {
	// Build javdb client options
	opts := []javdb.Option{
		javdb.WithAPIBase(cfg.APIBase),
		javdb.WithSites("https://javdb.com"),
	}
	if cfg.Token != "" {
		opts = append(opts, javdb.WithAppToken(cfg.Token))
	}
	if cfg.Cookie != "" {
		opts = append(opts, javdb.WithCookie(cfg.Cookie))
	}

	javdbClient, err := javdb.New(opts...)
	if err != nil {
		return nil, err
	}

	// Build av client (MissAV/Jable/HohoJ)
	avClient, err := av.NewClient(av.ClientOptions{})
	if err != nil {
		return nil, err
	}

	return &Server{
		javdb: javdbClient,
		av:    avClient,
		cfg:   cfg,
	}, nil
}

// Start starts the HTTP server in a goroutine.
// It returns immediately. Use Stop() to shut down.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	// Setup routes using standard library ServeMux (Go 1.22+ pattern matching)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	// JavDB endpoints
	mux.HandleFunc("GET /api/search", s.auth(s.handleSearch))
	mux.HandleFunc("GET /api/movie/", s.auth(s.handleMovie))
	mux.HandleFunc("GET /api/ranking/", s.auth(s.handleRanking))
	mux.HandleFunc("GET /api/magnets/", s.auth(s.handleMagnets))
	mux.HandleFunc("GET /api/tags", s.auth(s.handleTags))
	mux.HandleFunc("GET /api/actor/", s.auth(s.handleActor))
	// AV endpoints (MissAV/Jable/HohoJ for playback and download)
	mux.HandleFunc("GET /api/av/search", s.handleAVSearch)
	mux.HandleFunc("GET /api/av/detail/", s.handleAVDetail)
	mux.HandleFunc("GET /api/av/resolve/", s.handleAVResolve)
	mux.HandleFunc("GET /api/av/play/", s.handleAVPlay)
	mux.HandleFunc("POST /api/av/download/", s.handleAVDownload)

	// Apply CORS middleware
	handler := cors(mux)

	s.httpServer = &http.Server{
		Addr:         s.cfg.Addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.Printf("JavDB HTTP server listening on %s", s.cfg.Addr)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	s.running = true
	return nil
}

// Stop gracefully shuts down the HTTP server.
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running || s.httpServer == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := s.httpServer.Shutdown(ctx)
	s.running = false
	return err
}

// IsRunning returns whether the server is currently running.
func (s *Server) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
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
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
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
