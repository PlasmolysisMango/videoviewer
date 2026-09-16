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
	"net/url"
	"sync"
	"time"

	"videoviewer/pkg/av"
	"videoviewer/pkg/av/browser"
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
	// Proxy is the HTTP/SOCKS5 proxy URL (e.g., "http://127.0.0.1:7890" or "socks5://127.0.0.1:7891")
	Proxy string
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

	// imgClient 转发 Web 前端的图片请求（绕过第三方 CDN 的 CORS 限制）。
	imgClient  *http.Client
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
	// 配置未显式给凭据时，恢复上次会话的登录态（重启不丢登录）。
	if cfg.Cookie == "" && cfg.Token == "" {
		if cookie, appToken := loadSession(); cookie != "" || appToken != "" {
			log.Printf("goserver: restoring persisted JavDB session")
			if cookie != "" {
				opts = append(opts, javdb.WithCookie(cookie))
			}
			if appToken != "" {
				opts = append(opts, javdb.WithAppToken(appToken))
			}
		}
	}
	if cfg.Proxy != "" {
		opts = append(opts, javdb.WithProxy(cfg.Proxy))
	}

	javdbClient, err := javdb.New(opts...)
	if err != nil {
		return nil, err
	}

	// Build av client (MissAV/Jable/HohoJ).
	// MissAV/Jable 位于 Cloudflare 之后，标准库 Transport 会被 403 拦截，
	// 因此默认注入 tls-client 浏览器指纹（同 cmd/vl 的做法）；代理一并下沉到 requester。
	avOpts := av.ClientOptions{}
	if r, err := browser.New(browser.Options{
		Profile:        "chrome_150",
		Proxy:          cfg.Proxy,
		Timeout:        25 * time.Second,
		FollowRedirect: true,
	}); err == nil {
		avOpts.HTTP = av.Options{Requester: r}
	} else {
		log.Printf("goserver: browser profile init failed (%v), falling back to std transport", err)
		avOpts.HTTP = av.Options{Proxy: cfg.Proxy}
	}
	avClient, err := av.NewClient(avOpts)
	if err != nil {
		return nil, err
	}

	// 图片代理客户端：与主链路一致地走配置的代理。
	imgTransport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.Proxy != "" {
		if pu, perr := url.Parse(cfg.Proxy); perr == nil {
			imgTransport.Proxy = http.ProxyURL(pu)
		}
	}
	imgClient := &http.Client{Transport: imgTransport, Timeout: 30 * time.Second}

	return &Server{
		javdb:     javdbClient,
		av:        avClient,
		cfg:       cfg,
		imgClient: imgClient,
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
	// JavDB endpoints - no auth required for browsing
	mux.HandleFunc("GET /api/search", s.handleSearch)
	mux.HandleFunc("GET /api/movie/", s.handleMovie)
	mux.HandleFunc("GET /api/ranking/", s.handleRanking)
	mux.HandleFunc("GET /api/magnets/", s.handleMagnets)
	mux.HandleFunc("GET /api/tags", s.handleTags)
	mux.HandleFunc("GET /api/genre", s.handleGenre)
	mux.HandleFunc("POST /api/web-cookie", s.handleWebCookie)
	mux.HandleFunc("GET /api/actor/", s.handleActor)
	mux.HandleFunc("GET /api/actor-movies/", s.handleActorMovies)
mux.HandleFunc("GET /api/series-movies/", s.handleSeriesMovies)
	mux.HandleFunc("GET /api/lists/search", s.handleListSearch)
	mux.HandleFunc("GET /api/lists/", s.handleListMovies)
	mux.HandleFunc("GET /api/subscriptions", s.handleSubscriptions)
	mux.HandleFunc("POST /api/subscriptions", s.handleSubscriptions)
	mux.HandleFunc("DELETE /api/subscriptions/", s.handleSubscriptionDelete)
	// AV endpoints (MissAV/Jable/HohoJ for playback and download)
	mux.HandleFunc("GET /api/av/sources", s.handleAVSources)
	mux.HandleFunc("GET /api/av/search", s.handleAVSearch)
	mux.HandleFunc("GET /api/av/detail/", s.handleAVDetail)
	mux.HandleFunc("GET /api/av/probe/", s.handleAVProbe)
	mux.HandleFunc("GET /api/av/resolve/", s.handleAVResolve)
	mux.HandleFunc("GET /api/av/play/", s.handleAVPlay)
	mux.HandleFunc("POST /api/av/download/", s.handleAVDownload)
	mux.HandleFunc("POST /api/av/cf-cookie", s.handleAVCFCookie)
	// Image proxy for Flutter Web (third-party CDNs send no CORS headers)
	mux.HandleFunc("GET /api/img", s.handleImage)
	// HLS relay: rewrite playlist & stream segments so browsers (no Referer control)
	// can play Referer-gated CDNs like surrit
	mux.HandleFunc("GET /api/hls/playlist", s.handleHlsPlaylist)
	mux.HandleFunc("GET /api/hls/segment", s.handleHlsSegment)

	// Apply CORS middleware
	handler := cors(mux)

	s.httpServer = &http.Server{
		Addr:         s.cfg.Addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second, // 加长以适应 Probe 的随机延迟
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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
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
