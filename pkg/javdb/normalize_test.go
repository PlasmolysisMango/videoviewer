package javdb

import (
	"math"
	"strconv"
	"testing"
	"time"
)

func TestNormalizeCode(t *testing.T) {
	cases := map[string]string{
		"SSIS-001":    "SSIS-001",
		" ssis-001 ":  "SSIS-001",
		"ＳＳＩＳ-００１":    "SSIS-001",
		"fc2-ppv-123": "FC2-PPV-123",
		"":            "",
	}
	for in, want := range cases {
		if got := NormalizeCode(in); got != want {
			t.Errorf("NormalizeCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsPlausibleVideoCode(t *testing.T) {
	yes := []string{"SSIS-001", "ABC123", "FC2-PPV-1234567", "062216-179", "062216_001", "Wifey.2026.05.30", "n0656", "ｍｉｄａ-783"}
	no := []string{"", "AB", "12345", "標題一", "SSIS 001", "a", "巨乳-1", "?page=2"}
	for _, s := range yes {
		if !IsPlausibleVideoCode(s) {
			t.Errorf("IsPlausibleVideoCode(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if IsPlausibleVideoCode(s) {
			t.Errorf("IsPlausibleVideoCode(%q) = true, want false", s)
		}
	}
}

func TestClassifyVideoCodeFamily(t *testing.T) {
	cases := map[string]string{
		"SSIS-001":         "classic_hyphenated",
		"FC2-PPV-1234567":  "multi_hyphen",
		"062216-179":       "numeric_date_hyphen",
		"062216_001":       "numeric_date_underscore",
		"Wifey.2026.05.30": "western_studio_date",
		"n0656":            "hyphenless_studio",
		"12345":            "",
		"標題":               "",
	}
	for in, want := range cases {
		if got := ClassifyVideoCodeFamily(in); got != want {
			t.Errorf("ClassifyVideoCodeFamily(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitRateAndComments(t *testing.T) {
	rate, comments := SplitRateAndComments("4.45分, 由386人評價")
	if rate != "4.45" || comments != "386" {
		t.Fatalf("zh: %q / %q", rate, comments)
	}
	rate, comments = SplitRateAndComments("4.2, by 101 users")
	if rate != "4.2" || comments != "101" {
		t.Fatalf("en: %q / %q", rate, comments)
	}
	rate, comments = SplitRateAndComments("暫無評分")
	if rate != "" || comments != "" {
		t.Fatalf("empty: %q / %q", rate, comments)
	}
}

func TestNumericParsers(t *testing.T) {
	if v := ParseFloat("4.45分"); v != 4.45 {
		t.Fatalf("ParseFloat: %v", v)
	}
	if v := ParseFloat("-"); v != 0 {
		t.Fatalf("ParseFloat dash: %v", v)
	}
	if v := ParseFloat(""); v != 0 {
		t.Fatalf("ParseFloat empty: %v", v)
	}
	if v := ParseInt("由386人評價"); v != 386 {
		t.Fatalf("ParseInt: %v", v)
	}
	if v := ParseInt("abc"); v != 0 {
		t.Fatalf("ParseInt empty: %v", v)
	}
	mib := int64(1024 * 1024)
	gib := 1024 * mib
	tib := 1024 * gib
	sizes := map[string]int64{
		"1.24GB":        int64(1.24 * float64(gib)),
		"700 MB":        700 * mib,
		"2.5 TB":        int64(2.5 * float64(tib)),
		"512KB":         512 * 1024,
		"1.24GB, 3 個文件": int64(1.24 * float64(gib)),
		"無":             0,
	}
	for in, want := range sizes {
		if got := ParseSizeBytes(in); got != want {
			t.Errorf("ParseSizeBytes(%q) = %d, want %d", in, got, want)
		}
	}
	if v := ParseFileCount("3 個文件"); v != 3 {
		t.Fatalf("ParseFileCount zh: %d", v)
	}
	if v := ParseFileCount("12 files"); v != 12 {
		t.Fatalf("ParseFileCount en: %d", v)
	}
	if v := ParseFileCount("1.24GB"); v != 0 {
		t.Fatalf("ParseFileCount negative: %d", v)
	}
}

func TestInferResolution(t *testing.T) {
	cases := []struct {
		name string
		tags []string
		want int
	}{
		{"x", []string{"4K"}, 3840},
		{"x", []string{"8K"}, 7680},
		{"x", []string{"2K"}, 2560},
		{"x", []string{"高清"}, 1080},
		{"SSIS-001 1080p", nil, 1080},
		{"SSIS-001 720p", nil, 720},
		{"SSIS-001", nil, 0},
	}
	for _, c := range cases {
		if got := InferResolution(c.name, c.tags); got != c.want {
			t.Errorf("InferResolution(%q, %v) = %d, want %d", c.name, c.tags, got, c.want)
		}
	}
}

func TestClassifyMagnetName(t *testing.T) {
	cases := []struct {
		name string
		tags []string
		want string
	}{
		{"SSIS-001 [CNSUB]", nil, "subtitle"},
		{"SSIS-001 中文字幕", nil, "subtitle"},
		{"SSIS-001", []string{"含中字"}, "subtitle"},
		{"UC無碼破解 中文字幕", nil, "hacked_subtitle"},
		{"UC無碼破解", nil, "hacked_no_subtitle"},
		{"SSIS-001", nil, "no_subtitle"},
	}
	for _, c := range cases {
		if got := classifyMagnetName(c.name, c.tags); got != c.want {
			t.Errorf("classifyMagnetName(%q, %v) = %q, want %q", c.name, c.tags, got, c.want)
		}
	}
}

func TestFixImageURL(t *testing.T) {
	cases := map[string]string{
		"":              "",
		"/images/a.jpg": "/images/a.jpg",
		"https://www.javdb.com/rhe951l4q/x/a.jpg": "https://c0.jdbstatic.com/x/a.jpg",
		"http://mirror/rhe951l4q/b.jpg":           "https://c0.jdbstatic.com/b.jpg",
		"/rhe951l4q/no-scheme.jpg":                "/rhe951l4q/no-scheme.jpg",
	}
	for in, want := range cases {
		if got := FixImageURL(in); got != want {
			t.Errorf("FixImageURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHrefHelpers(t *testing.T) {
	hrefs := map[string]string{
		"/v/abc":                    "/v/abc",
		"v/abc":                     "/v/abc",
		"https://javdb.com/v/1?x=1": "/v/1?x=1",
		"http://javdb.com":          "",
		"magnet:?xt=urn:btih:ab":    "magnet:?xt=urn:btih:ab",
	}
	for in, want := range hrefs {
		if got := NormalizeHref(in); got != want {
			t.Errorf("NormalizeHref(%q) = %q, want %q", in, got, want)
		}
	}
	ids := map[string]string{
		"/actors/21Jp":       "21Jp",
		"https://x/v/yxY7kW": "yxY7kW",
		"/v/abc/":            "abc",
		"":                   "",
	}
	for in, want := range ids {
		if got := HrefID(in); got != want {
			t.Errorf("HrefID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPeriodAndYear(t *testing.T) {
	if got := ParsePeriod("/rankings/movies?p=daily&t=censored"); got != PeriodDaily {
		t.Fatalf("period from path: %q", got)
	}
	if got := ParsePeriod("p=weekly&x=1"); got != PeriodWeekly {
		t.Fatalf("period from query: %q", got)
	}
	if got := ParsePeriod("period=monthly"); got != "" {
		t.Fatalf("period negative: %q", got)
	}
	if got := ParseYear("/rankings/top?t=y2025"); got != 2025 {
		t.Fatalf("year: %d", got)
	}
	if got := ParseYear("t=all"); got != 0 {
		t.Fatalf("year negative: %d", got)
	}
	if !IsReleaseDate("2026-09-15") || IsReleaseDate("2026/09/15") {
		t.Fatal("IsReleaseDate")
	}
	if got := formatMillis(1700000000000); got != "2023-11-14" {
		t.Fatalf("formatMillis: %q", got)
	}
	if got := formatMillis(0); got != "" {
		t.Fatalf("formatMillis zero: %q", got)
	}
}

func TestSignature(t *testing.T) {
	at := time.Unix(1700000000, 0)
	sig := Signature(at)
	ts, clientID, digest := SplitSignature(sig)
	if ts != "1700000000" || clientID != signatureClientID {
		t.Fatalf("signature shape: %q", sig)
	}
	// Independent recomputation of md5("{ts}{salt}").
	want := md5Hex("1700000000" + signatureSalt)
	if digest != want {
		t.Fatalf("digest %q, want %q", digest, want)
	}
	if len(digest) != 32 {
		t.Fatalf("digest length: %d", len(digest))
	}
}

func TestSignatureCacheReusesAndRefreshes(t *testing.T) {
	now := time.Unix(1700000000, 0)
	c := &signatureCache{now: func() time.Time { return now }}
	first := c.Get()
	if c.Get() != first {
		t.Fatal("signature should be cached inside the TTL window")
	}
	now = now.Add(signatureTTL) // past the early-refresh margin
	if refreshed := c.Get(); refreshed == first {
		t.Fatal("signature should be re-minted after the TTL")
	} else if ts, _, _ := SplitSignature(refreshed); ts != strconv.FormatInt(now.Unix(), 10) {
		t.Fatalf("refreshed timestamp: %q", ts)
	}
}

func TestSplitSignaturePartial(t *testing.T) {
	if ts, cid, digest := SplitSignature("1.2"); ts != "1" || cid != "2" || digest != "" {
		t.Fatalf("two parts: %q %q %q", ts, cid, digest)
	}
	if ts, cid, digest := SplitSignature("plain"); ts != "plain" || cid != "" || digest != "" {
		t.Fatalf("one part: %q %q %q", ts, cid, digest)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:                               "",
		512:                             "512.00 B",
		1024 * 1024:                     "1.00 MB",
		int64(2.5 * 1024 * 1024 * 1024): "2.50 GB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestRawAndTextDateHelpers(t *testing.T) {
	if got := rawToString([]byte(`"abc"`)); got != "abc" {
		t.Fatalf("rawToString string: %q", got)
	}
	if got := rawToString([]byte(`4.02`)); got != "4.02" {
		t.Fatalf("rawToString number: %q", got)
	}
	if got := rawToString([]byte(`null`)); got != "" {
		t.Fatalf("rawToString null: %q", got)
	}
	if got := textDate([]byte(`"2026-07-16"`)); got != "2026-07-16" {
		t.Fatalf("textDate iso: %q", got)
	}
	if got := textDate([]byte(`1700000000`)); got != "2023-11-14" {
		t.Fatalf("textDate epoch seconds: %q", got)
	}
	if got := textDate([]byte(`1700000000000`)); got != "2023-11-14" {
		t.Fatalf("textDate epoch millis: %q", got)
	}
	if got := firstNonEmpty("", "  ", "x"); got != "x" {
		t.Fatalf("firstNonEmpty: %q", got)
	}
}

func TestParseActorNames(t *testing.T) {
	names := parseActorNames([]byte(`["楓花戀","路人"]`))
	if len(names) != 2 || names[0].Name != "楓花戀" {
		t.Fatalf("string array: %+v", names)
	}
	objs := parseActorNames([]byte(`[{"id":"21Jp","name":"楓花戀","gender":0}]`))
	if len(objs) != 1 || objs[0].ID != "21Jp" || objs[0].Gender != "female" {
		t.Fatalf("object array: %+v", objs)
	}
	if got := parseActorNames([]byte(`null`)); got != nil {
		t.Fatalf("null: %+v", got)
	}
	if got := apiGender([]byte(`1`)); got != "male" {
		t.Fatalf("gender male: %q", got)
	}
	if got := apiGender(nil); got != "" {
		t.Fatalf("gender unknown: %q", got)
	}
}

func TestEnsureLeadingAndCollapse(t *testing.T) {
	if ensureLeading("v/1") != "/v/1" || ensureLeading("/v/1") != "/v/1" {
		t.Fatal("ensureLeading")
	}
	if math.Abs(ParseFloat("3.5")-3.5) > 1e-9 {
		t.Fatal("ParseFloat plain")
	}
}
