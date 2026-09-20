package goserver

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

// persistedSession caches the JavDB auth material so a login survives server
// restarts (the mobile-API JWT and web cookie live only in memory otherwise).
// Username/Password enable the server to re-login automatically when the JWT
// expires (all four fields are secrets: the file is written with 0600
// permissions and must never be committed or logged).
type persistedSession struct {
	Cookie   string `json:"cookie,omitempty"`
	AppToken string `json:"app_token,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// sessionFile returns the session cache path (<data>/session.json), where
// <data> is the configured data dir or ~/.videoviewer on desktop.
func sessionFile() (string, error) {
	dir, err := storageDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "session.json"), nil
}

// loadSession reads the persisted session (auth material + saved credentials
// for auto re-login). Absent or corrupt files are treated as anonymous: a
// stale cache must never block startup.
func loadSession() (cookie, appToken, username, password string) {
	path, err := sessionFile()
	if err != nil {
		return "", "", "", ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", ""
	}
	var ps persistedSession
	if err := json.Unmarshal(data, &ps); err != nil {
		log.Printf("goserver: ignoring corrupt session cache: %v", err)
		return "", "", "", ""
	}
	return ps.Cookie, ps.AppToken, ps.Username, ps.Password
}

// saveSession persists the session atomically (temp file + rename).
func saveSession(cookie, appToken, username, password string) {
	if cookie == "" && appToken == "" && username == "" && password == "" {
		return
	}
	path, err := sessionFile()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	data, _ := json.Marshal(persistedSession{
		Cookie: cookie, AppToken: appToken, Username: username, Password: password,
	})
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		log.Printf("goserver: save session: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		log.Printf("goserver: save session: %v", err)
	}
}

// clearSession drops the persisted session (logout).
func clearSession() {
	path, err := sessionFile()
	if err != nil {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("goserver: clear session: %v", err)
	}
}
