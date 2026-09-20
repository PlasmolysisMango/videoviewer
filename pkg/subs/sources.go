package subs

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// fetcher is a minimal HTTP client shared by all sources: browser-ish UA,
// optional proxy, bounded retries, and an automatic cookie jar — avsubtitles
// issues an anonymous PHPSESSID on the first page view and requires it (plus
// a referer) on the actual download request, no account needed.
type fetcher struct {
	client *http.Client
}

func newFetcher(proxy string, timeout time.Duration) *fetcher {
	if timeout <= 0 {
		timeout = 25 * time.Second
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	if proxy != "" {
		if pu, err := url.Parse(proxy); err == nil {
			t.Proxy = http.ProxyURL(pu)
		}
	}
	jar, _ := cookiejar.New(nil)
	return &fetcher{client: &http.Client{Transport: t, Timeout: timeout, Jar: jar}}
}

const subsUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"

// get fetches a URL following redirects. extraCookie is appended when set.
func (f *fetcher) get(ctx context.Context, rawURL, referer, extraCookie string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", subsUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	if extraCookie != "" {
		// Explicit credential (e.g. a logged-in PHPSESSID) wins over the jar.
		req.Header.Set("Cookie", extraCookie)
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := f.client.Do(req)
		if err != nil {
			lastErr = err
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
			continue
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode >= 400 {
			return body, fmt.Errorf("HTTP %d from %s", resp.StatusCode, rawURL)
		}
		return body, nil
	}
	return nil, lastErr
}

// ---------------------------------------------------------------------------
// subtitlecat.com — anonymous machine-translated subtitles
// ---------------------------------------------------------------------------

type subtitlecatSource struct {
	http *fetcher
	// base is overridable for offline tests; empty means the real site.
	base string
}

func (s *subtitlecatSource) Name() string { return "subtitlecat" }

func (s *subtitlecatSource) site() string {
	if s.base != "" {
		return s.base
	}
	return "https://www.subtitlecat.com"
}

var (
	// Search results use site-relative hrefs ("subs/1671/x.html", no leading
	// slash); detail pages use "/subs/..." and leave spaces unencoded.
	scRowRe  = regexp.MustCompile(`<tr[^>]*>(?s:.*?)</tr>`)
	scHrefRe = regexp.MustCompile(`href="((?:/)?subs/\d+/[^"]+\.html)"[^>]*>([^<]+)<`)
	scSizeRe = regexp.MustCompile(`Size\s*([\d.,]+)\s*(KB|MB|B)`)
	scDlRe   = regexp.MustCompile(`Downloads\s*(\d+)`)
	scSrtRe  = regexp.MustCompile(`href="((?:/)?subs/\d+/[^"]+\.srt)"`)
	scLangRe = regexp.MustCompile(`-([a-zA-Z]{2}(?:-[A-Za-z]{2})?)\.srt$`)
)

// scRef normalizes a captured subs/ href into a site-absolute ref.
func scRef(href string) string {
	if !strings.HasPrefix(href, "/") {
		href = "/" + href
	}
	return href
}

func (s *subtitlecatSource) Search(ctx context.Context, code string) ([]Item, error) {
	code = NormalizeCode(code)
	base := s.site()
	body, err := s.http.get(ctx, base+"/index.php?search="+url.QueryEscape(code), base, "")
	if err != nil {
		return nil, err
	}

	type rowMeta struct {
		ref, title, size string
		downloads        int
	}
	var rows []rowMeta
	seen := map[string]bool{}
	for _, row := range scRowRe.FindAllString(string(body), -1) {
		m := scHrefRe.FindStringSubmatch(row)
		if m == nil || seen[m[1]] {
			continue
		}
		ref := scRef(m[1])
		seen[ref] = true
		r := rowMeta{ref: ref, title: strings.TrimSpace(m[2])}
		// Row tail carries "Size 131 KB" / "Downloads 2 downloads" hints.
		if mm := scSizeRe.FindStringSubmatch(row); mm != nil {
			r.size = strings.TrimSpace(mm[1] + " " + mm[2])
		}
		if mm := scDlRe.FindStringSubmatch(row); mm != nil {
			r.downloads = parseIntSafe(mm[1])
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%w on subtitlecat: %s", ErrNotFound, code)
	}

	// 精确番号命中优先：subtitlecat 搜索是模糊匹配（ABW-244 会混入
	// ABW-242、[ABW] 动漫等无关条目）。标题去空白后以番号开头的排最前，
	// 其余含番号的次之，无关的按下载量兜底，避免前 N 个详情页全被无关行占用。
	codeKey := strings.ToLower(code)
	flat := func(t string) string {
		return strings.ToLower(strings.Join(strings.Fields(t), ""))
	}
	score := func(title string) int {
		t := flat(title)
		switch {
		case strings.HasPrefix(t, codeKey):
			return 0
		case strings.Contains(t, codeKey):
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		si, sj := score(rows[i].title), score(rows[j].title)
		if si != sj {
			return si < sj
		}
		return rows[i].downloads > rows[j].downloads
	})

	// Each row is a hub of per-language .srt files (original + machine
	// translations). Open the first few to discover the actual languages so
	// the caller's picker can rank them (Chinese first) instead of seeing an
	// opaque "multi" bundle.
	var items []Item
	seenLang := map[string]bool{}
	for i, row := range rows {
		if i >= scMaxDetailPages {
			break
		}
		detailURL := base + row.ref
		db, err := s.http.get(ctx, detailURL, base+"/", "")
		if err != nil {
			continue
		}
		for _, lm := range scSrtRe.FindAllStringSubmatch(string(db), -1) {
			ln := ""
			if mm := scLangRe.FindStringSubmatch(scRef(lm[1])); mm != nil {
				ln = strings.ToLower(mm[1])
			}
			if ln == "" || seenLang[ln] {
				continue
			}
			seenLang[ln] = true
			items = append(items, Item{
				Source:    s.Name(),
				Title:     row.title,
				Lang:      ln,
				DetailURL: detailURL,
				Ref:       row.ref,
				Size:      row.size,
				Downloads: row.downloads,
			})
		}
	}
	// Detail pages unreachable (or no per-lang links): expose the rows as
	// multi-language entries so Download can still resolve a variant.
	if len(items) == 0 {
		for _, row := range rows {
			items = append(items, Item{
				Source:    s.Name(),
				Title:     row.title,
				Lang:      "multi",
				DetailURL: base + row.ref,
				Ref:       row.ref,
				Size:      row.size,
				Downloads: row.downloads,
			})
		}
	}
	return items, nil
}

func (s *subtitlecatSource) Download(ctx context.Context, ref, lang string) (Downloaded, error) {
	detailURL := s.site() + ref
	body, err := s.http.get(ctx, detailURL, s.site()+"/", "")
	if err != nil {
		return Downloaded{}, err
	}
	links := scSrtRe.FindAllStringSubmatch(string(body), -1)
	if len(links) == 0 {
		return Downloaded{}, fmt.Errorf("%w: no srt links on %s", ErrNotFound, detailURL)
	}

	pick := ""
	pickLang := ""
	best := 99
	for _, m := range links {
		ln := ""
		if mm := scLangRe.FindStringSubmatch(m[1]); mm != nil {
			ln = strings.ToLower(mm[1])
		}
		if lang != "" && (ln == strings.ToLower(lang) || langMatch(ln, lang)) {
			pick, pickLang = m[1], ln
			break
		}
		if lang == "" {
			if sc := langScore(ln); sc < best {
				best, pick, pickLang = sc, m[1], ln
			}
		}
	}
	if pick == "" {
		pick, pickLang = links[0][1], ""
	}
	pick = scRef(pick)

	srtURL := s.site() + strings.ReplaceAll(pick, " ", "%20")
	srt, err := s.http.get(ctx, srtURL, detailURL, "")
	if err != nil {
		return Downloaded{}, err
	}
	name := pick
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return Downloaded{Name: name, Lang: pickLang, Body: srt}, nil
}

// langScore ranks languages for the "no explicit language" case.
func langScore(lang string) int {
	switch {
	case lang == "zh-cn" || lang == "zh-tw" || lang == "zh":
		return 0
	case strings.HasPrefix(lang, "en"):
		return 1
	case lang == "ja" || lang == "jp":
		return 2
	default:
		return 3
	}
}

// langMatch does a loose comparison ("chs" ≈ "zh", "cht" ≈ "zh-tw").
func langMatch(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	norm := func(s string) string {
		switch {
		case s == "chs" || s == "zh-hans":
			return "zh-cn"
		case s == "cht" || s == "zh-hant":
			return "zh-tw"
		case s == "jp":
			return "ja"
		}
		return s
	}
	return norm(a) == norm(b)
}

func parseIntSafe(s string) int {
	n := 0
	for _, c := range strings.TrimSpace(s) {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// ---------------------------------------------------------------------------
// avsubtitles.com — community subtitles; fully anonymous (auto session cookie)
// ---------------------------------------------------------------------------

type avsubtitlesSource struct {
	http   *fetcher
	cookie string // optional explicit PHPSESSID (e.g. a logged-in session)
	// base is overridable for offline tests; empty means the real site.
	base string
}

func (s *avsubtitlesSource) Name() string { return "avsubtitles" }

func (s *avsubtitlesSource) site() string {
	if s.base != "" {
		return s.base
	}
	return "https://www.avsubtitles.com"
}

var (
	avSubLinkRe = regexp.MustCompile(`href="(/movie\d+/[^"]+/subtitles/([a-z]{2})/(\d+))"`)
	avMovieRe   = regexp.MustCompile(`href="(/movie\d+/[^"]+)"`)
	avRevIDRe   = regexp.MustCompile(`name="revid"\s+value="(\d+)"`)
	avSubIDRe   = regexp.MustCompile(`name="subid"\s+value="(\d+)"`)
	avZipRe     = regexp.MustCompile(`Filename:</div>\s*<div[^>]*>\s*<span[^>]*>([^<]+\.zip)</span>`)
	// Real gate pages use "./download_sub.php?..." with raw "&"; accept the
	// "/"-prefixed and &amp;-encoded spellings too.
	avDownloadRe = regexp.MustCompile(`href="((?:\./|/)?download_sub\.php\?subid=\d+(?:&amp;|&)revid=\d+)"`)
)

// avMaxMoviePages bounds the two-stage search: the results page lists movie
// pages only, and each must be visited to discover its subtitle entries.
const (
	avMaxMoviePages = 3
	// scMaxDetailPages bounds how many subtitlecat result rows are opened to
	// discover their per-language .srt variants.
	scMaxDetailPages = 3
)

func (s *avsubtitlesSource) Search(ctx context.Context, code string) ([]Item, error) {
	code = NormalizeCode(code)
	base := s.site()
	body, err := s.http.get(ctx, base+"/search_results.php?search="+url.QueryEscape(code), base, "")
	if err != nil {
		return nil, err
	}
	html := string(body)

	items := s.collectItems(html)
	if len(items) == 0 {
		// Two-stage search: the results page links movie pages, not the
		// subtitles themselves — visit each (bounded) and collect its
		// ".../subtitles/{lang}/{id}" entries.
		seenPage := map[string]bool{}
		for _, m := range avMovieRe.FindAllStringSubmatch(html, -1) {
			path := m[1]
			if strings.Contains(path, "/subtitles/") || seenPage[path] {
				continue
			}
			seenPage[path] = true
			if len(seenPage) > avMaxMoviePages {
				break
			}
			pb, err := s.http.get(ctx, base+path, base+"/search_results.php", s.cookie)
			if err != nil {
				continue
			}
			items = append(items, s.collectItems(string(pb))...)
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("%w on avsubtitles: %s", ErrNotFound, code)
	}
	return items, nil
}

// collectItems extracts subtitle detail links from any avsubtitles page
// (search results sometimes inline them, movie pages always do).
func (s *avsubtitlesSource) collectItems(html string) []Item {
	base := s.site()
	var items []Item
	seen := map[string]bool{}
	for _, m := range avSubLinkRe.FindAllStringSubmatch(html, -1) {
		path, ln := m[1], m[2]
		if seen[path] {
			continue
		}
		seen[path] = true
		// Human title from the movie slug:
		// /movie59595/hez-773--ena-koume-2025/subtitles/en/140347 → "hez 773 ena koume 2025"
		slug := path
		if i := strings.Index(slug, "/subtitles/"); i >= 0 {
			slug = slug[:i]
		}
		slug = strings.TrimPrefix(slug, "/movie")
		if i := strings.Index(slug, "/"); i >= 0 {
			slug = slug[i+1:]
		}
		title := strings.Join(strings.Fields(strings.NewReplacer("-", " ").Replace(slug)), " ")
		items = append(items, Item{
			Source:    s.Name(),
			Title:     title,
			Lang:      ln,
			DetailURL: base + path,
			Ref:       path,
		})
	}
	return items
}

func (s *avsubtitlesSource) Download(ctx context.Context, ref, lang string) (Downloaded, error) {
	base := s.site()
	detailURL := base + ref

	// Step 1: subtitle detail page (also seeds the anonymous PHPSESSID jar).
	body, err := s.http.get(ctx, detailURL, base+"/", s.cookie)
	if err != nil {
		return Downloaded{}, err
	}
	html := string(body)
	subid := ""
	if m := avSubIDRe.FindStringSubmatch(html); m != nil {
		subid = m[1]
	}
	revid := ""
	if m := avRevIDRe.FindStringSubmatch(html); m != nil {
		revid = m[1]
	}
	if subid == "" || revid == "" {
		return Downloaded{}, fmt.Errorf("avsubtitles: no download form on %s", detailURL)
	}

	// Step 2: the download gate page links the real file endpoint.
	gate, err := s.http.get(ctx, fmt.Sprintf("%s/download_page.php?subid=%s&revid=%s", base, subid, revid), detailURL, s.cookie)
	if err != nil {
		return Downloaded{}, err
	}
	dlPath := fmt.Sprintf("/download_sub.php?subid=%s&revid=%s", subid, revid)
	if m := avDownloadRe.FindStringSubmatch(string(gate)); m != nil {
		p := strings.ReplaceAll(m[1], "&amp;", "&")
		p = strings.TrimPrefix(p, "./")
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		dlPath = p
	}

	// Step 3: the zip bundle (largest .srt inside wins).
	zipBytes, err := s.http.get(ctx, base+dlPath, detailURL, s.cookie)
	if err != nil {
		return Downloaded{}, err
	}
	srt, name := unpackZipSRT(zipBytes)
	if srt == nil {
		return Downloaded{}, fmt.Errorf("avsubtitles: no .srt inside bundle")
	}
	langFromRef := ""
	if parts := strings.Split(ref, "/subtitles/"); len(parts) == 2 {
		if l := strings.SplitN(parts[1], "/", 2); len(l) == 2 {
			langFromRef = l[0]
		}
	}
	if mm := avZipRe.FindStringSubmatch(html); mm != nil && name == "" {
		name = mm[1]
	}
	return Downloaded{Name: name, Lang: langFromRef, Body: srt}, nil
}

// unpackZipSRT extracts the largest .srt entry from a ZIP bundle (nil when
// the payload is not a ZIP or has no .srt inside).
func unpackZipSRT(data []byte) ([]byte, string) {
	if len(data) < 4 || !bytes.Equal(data[:2], []byte("PK")) {
		return nil, ""
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, ""
	}
	var best []byte
	var bestName string
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !strings.HasSuffix(strings.ToLower(f.Name), ".srt") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		if len(content) > len(best) {
			best, bestName = content, f.Name
		}
	}
	return best, bestName
}
