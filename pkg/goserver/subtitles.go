package goserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"videoviewer/pkg/javdb"
	"videoviewer/pkg/subs"
)

// Subtitle endpoints wrap pkg/subs: search (list per source), explicit
// download, and an auto endpoint that picks the best language (zh first) and
// returns ready-to-use SRT bytes. Downloads are cached on disk under
// <data>/subtitles so replays skip the network entirely.

// subtitlesClient lazily builds the aggregation client; it shares the server's
// proxy configuration. pkg/subs uses plain std transport — subtitle sites have
// no bot protection beyond scanlover's Cloudflare (which reports a challenge).
func (s *Server) subtitlesClient() *subs.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subs == nil {
		s.subs = subs.New(subs.ClientOptions{Proxy: s.cfg.Proxy, Timeout: 25 * time.Second})
	}
	return s.subs
}

// handleSubtitlesSearch lists subtitle entries for a video code.
// GET /api/subtitles/search?code=SSIS-414
func (s *Server) handleSubtitlesSearch(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	rep, err := s.subtitlesClient().Search(ctx, code)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// handleSubtitlesAuto picks the best-language subtitle for a code and returns
// the SRT payload. Response headers carry the picked source/lang/filename so
// the frontend can display what it got.
// GET /api/subtitles/auto?code=SSIS-414
func (s *Server) handleSubtitlesAuto(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}

	// Disk cache: the auto pick is deterministic for a code, so a cached file
	// short-circuits both the search and the download.
	autoName := fmt.Sprintf("%s.auto.srt", sanitizeFile(code))
	if path, err := subtitleCachePath(autoName); err == nil {
		if info, serr := os.Stat(path); serr == nil && info.Size() > 0 {
			if data, rerr := os.ReadFile(path); rerr == nil {
				hdr := subtitleHeaders("auto", "", autoName)
				if m, ok := readSubMeta(autoName); ok {
					hdr = subtitleHeaders(m.Source, m.Lang, m.Name)
					data = simplifySubtitle(m.Lang, data)
				}
				writeSRT(w, data, hdr)
				return
			}
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	c := s.subtitlesClient()
	rep, err := c.Search(ctx, code)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	items := rep.Items
	if len(items) == 0 {
		writeError(w, http.StatusNotFound, "no subtitles found for "+code)
		return
	}
	// Rank candidates by language (zh first) and try downloading each in
	// order, capped at a few attempts: the top entry's detail page or file
	// can be dead while the rest are fine — manual search lists them all
	// and would succeed, so auto must fall through to the next candidate.
	sort.SliceStable(items, func(i, j int) bool {
		return langRank(items[i].Lang) < langRank(items[j].Lang)
	})
	pick := items[0]
	downloaded := subs.Downloaded{}
	const maxAttempts = 6
	for i, it := range items {
		if i >= maxAttempts {
			break
		}
		got, derr := c.Download(ctx, it.Source, it.Ref, it.Lang)
		if derr == nil && len(got.Body) > 0 && subtitleUsable(got.Body) {
			pick = it
			downloaded = got
			break
		}
	}
	if len(downloaded.Body) == 0 {
		writeError(w, http.StatusBadGateway, "all subtitle downloads failed for "+code)
		return
	}
	downloaded.Body = simplifySubtitle(downloaded.Lang, downloaded.Body)
	cacheSubtitle(autoName, downloaded.Body)
	cacheSubMeta(autoName, subMeta{Source: pick.Source, Lang: downloaded.Lang, Name: downloaded.Name})
	writeSRT(w, downloaded.Body, subtitleHeaders(pick.Source, downloaded.Lang, downloaded.Name))
}

// handleSubtitlesDownload fetches one specific subtitle.
// GET /api/subtitles/download?code=SSIS-414&source=subtitlecat&ref=%2Fsubs%2F...&lang=zh
// code is only used for the disk cache key / filename.
func (s *Server) handleSubtitlesDownload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	code := strings.TrimSpace(q.Get("code"))
	source := strings.TrimSpace(q.Get("source"))
	ref := strings.TrimSpace(q.Get("ref"))
	lang := strings.TrimSpace(q.Get("lang"))
	if code == "" || source == "" || ref == "" {
		writeError(w, http.StatusBadRequest, "code, source and ref are required")
		return
	}

	if path, ok := cachedSubtitleFile(code, source, lang); ok {
		if data, err := os.ReadFile(path); err == nil {
			writeSRT(w, simplifySubtitle(lang, data), subtitleHeaders(source, lang, filepath.Base(path)))
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	d, err := s.subtitlesClient().Download(ctx, source, ref, lang)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	name := fmt.Sprintf("%s.%s.%s.srt", sanitizeFile(code), sanitizeFile(source), sanitizeFile(lang))
	d.Body = simplifySubtitle(lang, d.Body)
	cacheSubtitle(name, d.Body)
	writeSRT(w, d.Body, subtitleHeaders(source, d.Lang, d.Name))
}

// simplifySubtitle converts Chinese subtitles (lang empty or zh*) to
// simplified characters via the shared t2s mapping; conversion is idempotent
// for already-simplified text and skipped for non-Chinese languages.
// Applied before caching so disk replays serve simplified text directly.
func simplifySubtitle(lang string, data []byte) []byte {
	l := strings.ToLower(lang)
	if l != "" && !strings.HasPrefix(l, "zh") {
		return data
	}
	return []byte(javdb.ToSimplified(string(data)))
}

// subtitleUsable rejects payloads dominated by U+FFFD replacement characters:
// some upstream subtitle files were transcoded badly at the source and arrive
// as "valid" UTF-8 full of U+FFFD — no decoding can recover them, so auto must
// treat them as failures and fall through to the next candidate. A tiny
// fraction of damaged lines is tolerated (<0.5%).
func subtitleUsable(b []byte) bool {
	runes := []rune(string(b))
	if len(runes) == 0 {
		return false
	}
	bad := 0
	for _, r := range runes {
		if r == utf8.RuneError {
			bad++
		}
	}
	return bad*200 < len(runes)
}

// langRank mirrors pkg/subs ranking at the endpoint level: zh < en < ja < rest.
func langRank(lang string) int {
	l := strings.ToLower(lang)
	switch {
	case strings.HasPrefix(l, "zh"):
		return 0
	case strings.HasPrefix(l, "en"):
		return 1
	case strings.HasPrefix(l, "ja"), strings.HasPrefix(l, "jp"):
		return 2
	}
	return 3
}

func subtitleHeaders(source, lang, name string) [][2]string {
	return [][2]string{
		{"X-Sub-Source", source},
		{"X-Sub-Lang", lang},
		{"X-Sub-Name", name},
	}
}

func writeSRT(w http.ResponseWriter, data []byte, hdr [][2]string) {
	for _, h := range hdr {
		w.Header().Set(h[0], h[1])
	}
	w.Header().Set("Content-Type", "application/x-subrip; charset=utf-8")
	w.Write(data)
}

var subtitleFileRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// sanitizeFile strips characters that are unsafe in cache filenames.
func sanitizeFile(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "x"
	}
	return subtitleFileRe.ReplaceAllString(s, "_")
}

// subtitleCachePath returns <data>/subtitles/<name>.
func subtitleCachePath(name string) (string, error) {
	dir, err := storageDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "subtitles", name), nil
}

// cachedSubtitleFile reports a non-empty cached file for (code, source, lang);
// source "auto" with empty lang addresses the auto-pick cache slot.
func cachedSubtitleFile(code, source, lang string) (string, bool) {
	name := fmt.Sprintf("%s.%s.%s.srt", sanitizeFile(code), sanitizeFile(source), sanitizeFile(lang))
	path, err := subtitleCachePath(name)
	if err != nil {
		return "", false
	}
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return path, true
	}
	return "", false
}

// cacheSubtitle writes a cache entry; failures are ignored (cache is optional).
func cacheSubtitle(name string, data []byte) {
	path, err := subtitleCachePath(name)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

// subMeta records where a cached auto-picked subtitle came from so cache hits
// can still report the provenance in response headers.
type subMeta struct {
	Source string `json:"source"`
	Lang   string `json:"lang"`
	Name   string `json:"name"`
}

func cacheSubMeta(name string, m subMeta) {
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	path, err := subtitleCachePath(name + ".meta")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, b, 0o600)
}

func readSubMeta(name string) (subMeta, bool) {
	var m subMeta
	path, err := subtitleCachePath(name + ".meta")
	if err != nil {
		return m, false
	}
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &m) != nil {
		return m, false
	}
	return m, true
}
