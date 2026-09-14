package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	logDir  = "/var/log/videoviewer/uploads"
	port    = ":9090"
)

func main() {
	// Create log directory
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		log.Fatalf("Failed to create log dir: %v", err)
	}

	http.HandleFunc("/api/upload-log", handleUpload)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	log.Printf("Log receiver listening on %s", port)
	log.Printf("Logs stored in: %s", logDir)
	
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatal(err)
	}
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if len(body) == 0 {
		http.Error(w, "Empty body", http.StatusBadRequest)
		return
	}

	// Generate filename with timestamp
	timestamp := time.Now().Format("20060102_150405")
	filename := filepath.Join(logDir, fmt.Sprintf("client_%s.json", timestamp))

	// Write log to file
	if err := os.WriteFile(filename, body, 0o644); err != nil {
		http.Error(w, "Failed to write log", http.StatusInternalServerError)
		return
	}

	log.Printf("Log uploaded: %s (%d bytes)", filename, len(body))
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"file":   filename,
	})
}
