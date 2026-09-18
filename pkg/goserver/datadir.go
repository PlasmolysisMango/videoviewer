package goserver

import (
	"log"
	"os"
	"path/filepath"
	"sync"
)

// dataDir overrides the directory where goserver persists its state
// (session cache, subscriptions). On Android the process HOME points at
// shared storage that the app cannot write (scoped storage), so the host
// must point this at its private files directory before StartServer.
var (
	dataDirMu sync.Mutex
	dataDir   string
)

// SetDataDir sets the directory for persistent state (session cache,
// subscriptions). Call it before StartServer on platforms where the process
// HOME is not writable (Android: pass the app's files dir). Exported for
// gomobile bind.
//
//export SetDataDir
func SetDataDir(dir string) {
	if dir == "" {
		return
	}
	dataDirMu.Lock()
	defer dataDirMu.Unlock()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("goserver: create data dir %s: %v", dir, err)
		return
	}
	dataDir = dir
}

// storageDir returns the directory for persistent state: the configured
// data dir if set, otherwise ~/.videoviewer as on desktop.
func storageDir() (string, error) {
	dataDirMu.Lock()
	dir := dataDir
	dataDirMu.Unlock()
	if dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", err
	}
	return filepath.Join(home, ".videoviewer"), nil
}
