// Package main implements a local HTTP server that exposes the javdb pkg
// capabilities as a REST API for Flutter frontend consumption.
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"videoviewer/pkg/goserver"
)

func main() {
	addr := flag.String("addr", ":18888", "HTTP listen address")
	apiBase := flag.String("api-base", goserver.DefaultConfig().APIBase, "JavDB API base URL")
	token := flag.String("token", os.Getenv("JAVDB_TOKEN"), "App JWT token (or set JAVDB_TOKEN)")
	cookie := flag.String("cookie", os.Getenv("JAVDB_COOKIE"), "Web session cookie (or set JAVDB_COOKIE)")
	dlDir := flag.String("dl-dir", os.Getenv("JAVDB_DL_DIR"), "Download directory (or set JAVDB_DL_DIR)")
	proxy := flag.String("proxy", os.Getenv("HTTP_PROXY"), "HTTP/SOCKS5 proxy URL (e.g., http://127.0.0.1:7890 or socks5://127.0.0.1:7891)")
	flag.Parse()

	cfg := goserver.Config{
		Addr:        *addr,
		APIBase:     *apiBase,
		Token:       *token,
		Cookie:      *cookie,
		DownloadDir: *dlDir,
		Proxy:       *proxy,
	}

	srv, err := goserver.New(cfg)
	if err != nil {
		log.Fatalf("create server: %v", err)
	}

	// Graceful shutdown on SIGINT/SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	if err := srv.Start(); err != nil {
		log.Fatalf("start server: %v", err)
	}

	log.Printf("API base: %s", *apiBase)
	if *token != "" {
		log.Printf("App token: configured")
	}
	if *proxy != "" {
		log.Printf("Proxy: %s", *proxy)
	}

	// Wait for shutdown signal
	<-sigCh
	log.Println("shutting down...")

	if err := srv.Stop(); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	log.Println("server stopped")
}
