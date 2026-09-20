// Package subs implements subtitle (SRT) search and download for JAV titles
// from public subtitle sites.
//
// Sources:
//   - subtitlecat.com — machine-translated subtitles; fully anonymous. Search
//     an index page, open a detail page, grab the per-language .srt links.
//   - avsubtitles.com — community subtitles (better quality); fully anonymous.
//     The site issues an anonymous PHPSESSID on first visit which is replayed
//     (cookie jar) on the download request, so no account is needed.
//
// scanlover.com was evaluated (2026-09-20) and dropped: the whole forum sits
// behind a Cloudflare challenge, which is out of scope.
//
// All downloads are normalized: non-UTF-8 payloads are transcoded to UTF-8 and
// ZIP bundles are unpacked to the largest contained .srt entry.
package subs

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Sentinel errors returned by sources / the aggregate client.
var (
	// ErrNotFound means the searched code has no subtitle rows at all.
	ErrNotFound = errors.New("subs: no subtitle found")
	// ErrAuthRequired means the source can list subtitles but downloading
	// needs a session cookie.
	ErrAuthRequired = errors.New("subs: download requires session cookie")
	// ErrUnsupported marks a source whose scraping is not implemented yet.
	ErrUnsupported = errors.New("subs: source not supported")
)

// Item is one searchable subtitle entry. Ref is an opaque token handed back to
// Client.Download; its meaning is source-specific.
type Item struct {
	Source    string `json:"source"`
	Title     string `json:"title"`
	Lang      string `json:"lang,omitempty"`
	DetailURL string `json:"detail_url,omitempty"`
	Ref       string `json:"ref"`
	Size      string `json:"size,omitempty"`
	Downloads int    `json:"downloads,omitempty"`
}

// SearchReport aggregates per-source outcomes so callers can see why a source
// contributed nothing instead of guessing.
type SearchReport struct {
	Items  []Item            `json:"items"`
	Errors map[string]string `json:"errors,omitempty"`
}

// Downloaded is the payload returned by Client.Download: subtitle bytes
// (guaranteed UTF-8), the language actually picked, and a human name.
type Downloaded struct {
	Name string `json:"name"`
	Lang string `json:"lang"`
	Body []byte `json:"-"`
}

// Source is a single subtitle site scraper.
type Source interface {
	Name() string
	// Search lists subtitle entries for a video code such as "SSIS-414".
	Search(ctx context.Context, code string) ([]Item, error)
	// Download fetches the subtitle content. lang may be empty for the
	// source's default preference (Chinese first, then English, then Japanese).
	Download(ctx context.Context, ref, lang string) (Downloaded, error)
}

// ClientOptions configures the aggregate client.
type ClientOptions struct {
	// Proxy is an optional HTTP/SOCKS5 proxy URL.
	Proxy string
	// Timeout bounds a single HTTP request; defaults to 25s.
	Timeout time.Duration
	// AVSubtitlesCookie is an optional explicit "PHPSESSID=..." session that
	// overrides the automatic anonymous one (only useful for logged-in perks).
	AVSubtitlesCookie string
}

// Client fans a search out to every source and normalizes downloads.
type Client struct {
	sources []Source
	http    *fetcher
}

// New builds a Client with all known sources.
func New(opts ClientOptions) *Client {
	f := newFetcher(opts.Proxy, opts.Timeout)
	sources := []Source{
		&subtitlecatSource{http: f, base: "https://www.subtitlecat.com"},
		&avsubtitlesSource{http: f, cookie: opts.AVSubtitlesCookie, base: "https://www.avsubtitles.com"},
	}
	return &Client{sources: sources, http: f}
}

// Search queries every source in parallel and merges the results. A failing
// source is recorded in the report's Errors map (keyed by source name) instead
// of failing the whole call.
func (c *Client) Search(ctx context.Context, code string) (*SearchReport, error) {
	code = NormalizeCode(code)
	if code == "" {
		return nil, fmt.Errorf("%w: empty code", ErrInvalidQuery)
	}

	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		items = make([]Item, 0, 8)
		errs  = map[string]string{}
	)
	for _, src := range c.sources {
		wg.Add(1)
		go func(src Source) {
			defer wg.Done()
			its, err := src.Search(ctx, code)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if !errors.Is(err, ErrNotFound) {
					errs[src.Name()] = err.Error()
				}
				return
			}
			items = append(items, its...)
		}(src)
	}
	wg.Wait()

	if len(items) == 0 {
		if len(errs) == 0 {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, code)
		}
		return nil, fmt.Errorf("%w: %s (%s)", ErrNotFound, code, strings.Join(values(errs), "; "))
	}
	rankLangs(items)
	return &SearchReport{Items: items, Errors: errs}, nil
}

// Download fetches the subtitle referenced by (source, ref). When lang is
// empty the source applies its own preference order.
func (c *Client) Download(ctx context.Context, source, ref, lang string) (Downloaded, error) {
	for _, src := range c.sources {
		if src.Name() != source {
			continue
		}
		d, err := src.Download(ctx, ref, lang)
		if err != nil {
			return Downloaded{}, err
		}
		// The source knows which language it fetched; use that to pick the
		// right legacy encoding probe (GBK vs Shift-JIS vs Big5…).
		d.Body = ToUTF8WithLang(d.Body, d.Lang)
		return d, nil
	}
	return Downloaded{}, fmt.Errorf("%w: unknown source %q", ErrInvalidQuery, source)
}

// ErrInvalidQuery reports malformed caller input.
var ErrInvalidQuery = errors.New("subs: invalid query")

// NormalizeCode upper-cases a video code and strips decorations so
// "ssis-414", "SSIS 414" and "SSIS-414 " all match.
func NormalizeCode(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

// rankLangs sorts items so Chinese variants surface first, then English, then
// Japanese — the display order used by the app's subtitle picker.
func rankLangs(items []Item) {
	score := func(lang string) int {
		l := strings.ToLower(lang)
		switch {
		case strings.HasPrefix(l, "zh"), strings.Contains(l, "chs"), strings.Contains(l, "cht"):
			return 0
		case strings.HasPrefix(l, "en"):
			return 1
		case strings.HasPrefix(l, "ja"), strings.HasPrefix(l, "jp"):
			return 2
		default:
			return 3
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		si, sj := score(items[i].Lang), score(items[j].Lang)
		if si != sj {
			return si < sj
		}
		return items[i].Downloads > items[j].Downloads
	})
}

func values(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+": "+v)
	}
	sort.Strings(out)
	return out
}
