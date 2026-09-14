// Package goserver provides gomobile-compatible exported functions for embedding
// the HTTP server in Android/iOS apps.
//
// These functions are designed to be called from Kotlin/Swift via gomobile bind.
// They manage a global server instance.
package goserver

import (
	"log"
	"sync"
)

var (
	globalServer *Server
	globalMu     sync.Mutex
)

// StartServer starts the HTTP server with the given configuration.
// This function is exported for gomobile bind.
//
// Parameters:
//   - addr: HTTP listen address (e.g., "127.0.0.1:18888")
//   - apiBase: JavDB API base URL (empty for default)
//   - token: JavDB App JWT token (empty if not available)
//   - cookie: JavDB web session cookie (empty if not available)
//   - downloadDir: Directory for downloaded files (empty to disable)
//
// Returns empty string on success, error message on failure.
//
//export StartServer
func StartServer(addr, apiBase, token, cookie, downloadDir string) string {
	globalMu.Lock()
	defer globalMu.Unlock()

	if globalServer != nil && globalServer.IsRunning() {
		return "" // Already running
	}

	cfg := DefaultConfig()
	if addr != "" {
		cfg.Addr = addr
	}
	if apiBase != "" {
		cfg.APIBase = apiBase
	}
	cfg.Token = token
	cfg.Cookie = cookie
	cfg.DownloadDir = downloadDir

	srv, err := New(cfg)
	if err != nil {
		log.Printf("goserver: failed to create server: %v", err)
		return err.Error()
	}

	if err := srv.Start(); err != nil {
		log.Printf("goserver: failed to start server: %v", err)
		return err.Error()
	}

	globalServer = srv
	log.Printf("goserver: server started on %s", cfg.Addr)
	return ""
}

// StopServer stops the HTTP server.
// This function is exported for gomobile bind.
// Returns empty string on success, error message on failure.
//
//export StopServer
func StopServer() string {
	globalMu.Lock()
	defer globalMu.Unlock()

	if globalServer == nil {
		return ""
	}

	if err := globalServer.Stop(); err != nil {
		log.Printf("goserver: failed to stop server: %v", err)
		return err.Error()
	}

	globalServer = nil
	log.Printf("goserver: server stopped")
	return ""
}

// IsServerRunning returns whether the server is currently running.
// This function is exported for gomobile bind.
//
//export IsServerRunning
func IsServerRunning() bool {
	globalMu.Lock()
	defer globalMu.Unlock()
	return globalServer != nil && globalServer.IsRunning()
}

// GetServerAddr returns the address the server is listening on.
// This function is exported for gomobile bind.
// Returns empty string if server is not running.
//
//export GetServerAddr
func GetServerAddr() string {
	globalMu.Lock()
	defer globalMu.Unlock()
	if globalServer == nil {
		return ""
	}
	return globalServer.cfg.Addr
}
