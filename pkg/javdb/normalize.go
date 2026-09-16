package javdb

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Ported from the reference implementation (JAVDB_AutoSpider javdb/parsing/*)
// so both backends produce identical values for the same upstream data.

var (
	scoreZHRe    = regexp.MustCompile(`(\d+(?:\.\d+)?)分`)
	scoreENRe    = regexp.MustCompile(`(\d+(?:\.\d+)?),\s*by\b`)
	commentsZHRe = regexp.MustCompile(`由(\d+)人評價`)
	commentsENRe = regexp.MustCompile(`by\s+(\d+)\s+users?`)
	sizeRe       = regexp.MustCompile(`(?i)([\d.,]+)\s*(TB|GB|MB|KB)`)
	filesRe      = regexp.MustCompile(`(\d+)\s*(?:個文件|files?)`)
	yearRe       = regexp.MustCompile(`(?:^|/|\?)t=y(\d{4})`)
	periodRe     = regexp.MustCompile(`(?:^|/|\?)p=(daily|weekly|monthly)`)
	trailDateRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

	// Video-code families, evaluated in order (mirrors _VIDEO_CODE_FAMILY_PATTERNS).
	codeFamilies = []struct {
		Name string
		Re   *regexp.Regexp
	}{
		{"western_studio_date", regexp.MustCompile(`^[A-Za-z0-9]*[A-Za-z][A-Za-z0-9]*\.(?:\d{4}|\d{2})\.\d{2}\.\d{2}$`)},
		{"multi_hyphen", regexp.MustCompile(`^[A-Za-z0-9]+(?:-[A-Za-z0-9]+){2,}$`)},
		{"numeric_date_hyphen", regexp.MustCompile(`^\d{6}-\d+$`)},
		{"numeric_date_underscore", regexp.MustCompile(`^\d{6}_\d+$`)},
		{"classic_hyphenated", regexp.MustCompile(`^[A-Za-z]+-\d+[A-Za-z0-9]*$`)},
		{"hyphenless_studio", regexp.MustCompile(`^[A-Za-z]+\d+$`)},
	}
)

// NormalizeCode folds full-width characters to ASCII, trims and upper-cases a
// video code so "ssis-001", "ＳＳＩＳ-００１" and " SSIS-001 " share one key.
func NormalizeCode(code string) string {
	if code == "" {
		return ""
	}
	return strings.ToUpper(foldWidth(strings.TrimSpace(code)))
}

// IsPlausibleVideoCode reports whether a token looks like a real video code.
// It accepts classic "ABC-123", multi-hyphen "FC2-PPV-1234567", numeric
// date-style uncensored codes ("062216-179", "062216_001"), dotted western
// studio-date tokens ("Wifey.2026.05.30") and hyphen-less codes ("n0656").
func IsPlausibleVideoCode(raw string) bool {
	s := foldWidth(strings.TrimSpace(raw))
	if len(s) < 2 {
		return false
	}
	if codeFamilies[0].Re.MatchString(s) {
		return true
	}
	hasDigit, hasAlpha := false, false
	for _, r := range s {
		switch {
		case r == '-' || r == '_':
			continue
		case r >= '0' && r <= '9':
			hasDigit = true
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			hasAlpha = true
		default:
			// Any other character (CJK, whitespace, punctuation) disqualifies it.
			return false
		}
	}
	return hasDigit && (hasAlpha || strings.ContainsAny(s, "-_"))
}

// ClassifyVideoCodeFamily labels a code by its shape, or "" when unknown.
// It is descriptive only: many valid codes match no family.
func ClassifyVideoCodeFamily(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) < 2 {
		return ""
	}
	for _, f := range codeFamilies {
		if f.Re.MatchString(s) {
			return f.Name
		}
	}
	return ""
}

// SplitRateAndComments extracts ("4.45", "386") from "4.45分, 由386人評價"
// or "4.2, by 101 users". Missing parts come back as "".
func SplitRateAndComments(scoreText string) (rate, comments string) {
	if scoreText == "" {
		return "", ""
	}
	if m := scoreZHRe.FindStringSubmatch(scoreText); m != nil {
		rate = m[1]
	} else if m := scoreENRe.FindStringSubmatch(scoreText); m != nil {
		rate = m[1]
	}
	if m := commentsZHRe.FindStringSubmatch(scoreText); m != nil {
		comments = m[1]
	} else if m := commentsENRe.FindStringSubmatch(scoreText); m != nil {
		comments = m[1]
	}
	return rate, comments
}

// ParseFloat is a lenient float parser for scraped text ("4.45分" -> 4.45).
func ParseFloat(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '+' {
			b.WriteRune(r)
		} else if b.Len() > 0 && (r == '分' || unicode.IsSpace(r)) {
			break
		}
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(b.String()), 64)
	if err != nil {
		return 0
	}
	return v
}

// ParseInt is a lenient int parser ("由386人評價" -> 386).
func ParseInt(s string) int {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return 0
	}
	v, _ := strconv.Atoi(b.String())
	return v
}

// ParseSizeBytes converts a scraped size ("1.24GB", "700 MB") to bytes.
func ParseSizeBytes(s string) int64 {
	m := sizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	n, err := strconv.ParseFloat(strings.TrimSuffix(m[1], ","), 64)
	if err != nil {
		return 0
	}
	var unit int64
	switch strings.ToUpper(m[2]) {
	case "KB":
		unit = 1024
	case "MB":
		unit = 1024 * 1024
	case "GB":
		unit = 1024 * 1024 * 1024
	case "TB":
		unit = 1024 * 1024 * 1024 * 1024
	}
	return int64(n * float64(unit))
}

// ParseFileCount extracts the "N 個文件" / "N files" counter from magnet meta text.
func ParseFileCount(s string) int {
	m := filesRe.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	v, _ := strconv.Atoi(m[1])
	return v
}

// InferResolution guesses the video height (720/1080/2560/3840/7680) from the
// torrent name and tags; 0 when unknown.
func InferResolution(name string, tags []string) int {
	tagText := strings.Join(tags, " ")
	switch {
	case strings.Contains(tagText, "8K"):
		return 7680
	case strings.Contains(tagText, "4K"):
		return 3840
	case strings.Contains(tagText, "2K"):
		return 2560
	case strings.Contains(tagText, "高清"):
		return 1080
	}
	low := strings.ToLower(name)
	switch {
	case strings.Contains(low, "8k"):
		return 7680
	case strings.Contains(low, "4k"):
		return 3840
	case strings.Contains(low, "2k"):
		return 2560
	case strings.Contains(low, "1080"):
		return 1080
	case strings.Contains(low, "720"):
		return 720
	}
	return 0
}

// Subtitle markers used by JavDB torrent naming.
var subtitleMarkers = []string{"[CNSUB]", "中文字幕", "中字", "有字", "csub", "chinese subs", "chinese_sub"}

// HackedMarkers flag an uncensored-leak ("破解") torrent, priority-ordered.
var hackedMarkers = []string{"UC无码破解", "UC無碼破解", "U无码破解", "U無碼破解", "UC", "U无码", "U無碼"}

// classifyMagnetName buckets a torrent into subtitle / hacked_subtitle /
// hacked_no_subtitle / no_subtitle, mirroring the reference pipeline labels.
func classifyMagnetName(name string, tags []string) string {
	hay := strings.ToLower(name + " " + strings.Join(tags, " "))
	hasSub := false
	for _, m := range subtitleMarkers {
		if strings.Contains(hay, strings.ToLower(m)) {
			hasSub = true
			break
		}
	}
	for _, t := range tags {
		if strings.Contains(t, "中字") {
			hasSub = true
		}
	}
	isHacked := false
	for _, m := range hackedMarkers {
		if strings.Contains(name, m) {
			isHacked = true
			break
		}
	}
	switch {
	case hasSub && isHacked:
		return "hacked_subtitle"
	case hasSub:
		return "subtitle"
	case isHacked:
		return "hacked_no_subtitle"
	default:
		return "no_subtitle"
	}
}

// FixImageURL rewrites JavDB's rotating image proxy path to the stable CDN
// host, matching the upstream extension's normalisation
// (https://<any>/rhe951l4q/... -> https://c0.jdbstatic.com/...).
func FixImageURL(u string) string {
	if u == "" {
		return ""
	}
	i := strings.Index(u, "/rhe951l4q/")
	if i < 0 {
		return u
	}
	j := strings.Index(u[:i], "://")
	if j < 0 {
		return u
	}
	return "https://c0.jdbstatic.com" + u[i+len("/rhe951l4q"):]
}

// posterFromCover derives the vertical (2:3) poster URL from a wide cover
// still. The app API serves /covers/ and /small_covers/ (both 16:9, and the
// small variant 403s without a Referer), while the same hash is also hosted
// under /thumbs/ as the vertical card art the web listing shows
// (covers/9d/9DGB5X.jpg -> thumbs/9d/9DGB5X.jpg).
func posterFromCover(u string) string {
	if u == "" {
		return ""
	}
	for _, dir := range []string{"/covers/", "/small_covers/"} {
		if i := strings.LastIndex(u, dir); i >= 0 {
			return u[:i] + "/thumbs/" + u[i+len(dir):]
		}
	}
	return ""
}

// NormalizeHref turns an href into a site-relative path ("/actors/21Jp").
// Non-site URLs (magnet:, https://external) are returned unchanged / stripped.
func NormalizeHref(href string) string {
	h := strings.TrimSpace(href)
	if h == "" {
		return ""
	}
	if strings.HasPrefix(h, "magnet:") {
		return h
	}
	for _, scheme := range []string{"https://", "http://"} {
		if strings.HasPrefix(h, scheme) {
			rest := h[len(scheme):]
			if i := strings.Index(rest, "/"); i >= 0 {
				return rest[i:]
			}
			return ""
		}
	}
	return ensureLeading(h)
}

// HrefID extracts the trailing identifier of a site path ("/actors/21Jp" -> "21Jp").
func HrefID(href string) string {
	p := NormalizeHref(href)
	if p == "" {
		return ""
	}
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	p = strings.TrimSuffix(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// ParsePeriod finds a ranking period ("p=weekly") in a URL or page fragment.
func ParsePeriod(s string) Period {
	if m := periodRe.FindStringSubmatch(s); m != nil {
		return Period(m[1])
	}
	return ""
}

// ParseYear finds a TOP250 year marker ("t=y2025") in a URL or page fragment.
func ParseYear(s string) int {
	if m := yearRe.FindStringSubmatch(s); m != nil {
		v, _ := strconv.Atoi(m[1])
		return v
	}
	return 0
}

// IsReleaseDate reports whether s is a plain "YYYY-MM-DD" date.
func IsReleaseDate(s string) bool { return trailDateRe.MatchString(strings.TrimSpace(s)) }

// formatMillis renders epoch milliseconds as a UTC "YYYY-MM-DD" date.
func formatMillis(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02")
}

var fullWidthDigits = map[rune]rune{}

func init() {
	// Full-width ASCII (FF01-FF5E) -> half-width.
	for r := rune(0xFF01); r <= 0xFF5E; r++ {
		fullWidthDigits[r] = r - 0xFEE0
	}
	fullWidthDigits[0x3000] = ' '
}

func foldWidth(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r >= 0x3000 && r <= 0xFF5E }) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if v, ok := fullWidthDigits[r]; ok {
			return v
		}
		return r
	}, s)
}
