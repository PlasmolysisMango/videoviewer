package javdb

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMovieUnmarshalJSONLooseScalars(t *testing.T) {
	// The upstream API is inconsistent about numbers vs strings and answers
	// null for missing scores, so the decoder must take every shape.
	cases := []struct {
		name        string
		body        string
		wantScore   float64
		wantDur     int
		wantRelease string
	}{
		{"numbers", `{"id":"a","score":4.45,"duration":125,"release_date":"2024-05-21"}`, 4.45, 125, "2024-05-21"},
		{"strings", `{"id":"a","score":"4.02","duration":"120","release_date":"2024-05-21"}`, 4.02, 120, "2024-05-21"},
		{"nulls", `{"id":"a","score":null,"duration":null,"release_date":null}`, 0, 0, ""},
		{"dash placeholder", `{"id":"a","score":"-","duration":"","release_date":""}`, 0, 0, ""},
		{"decimal string duration", `{"id":"a","duration":"125.0"}`, 0, 125, ""},
		{"missing keys", `{"id":"a"}`, 0, 0, ""},
	}
	for _, tc := range cases {
		var m Movie
		if err := json.Unmarshal([]byte(tc.body), &m); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if m.Score != tc.wantScore || m.Duration != tc.wantDur || m.ReleaseDate != tc.wantRelease {
			t.Errorf("%s: got score=%v duration=%v release=%q", tc.name, m.Score, m.Duration, m.ReleaseDate)
		}
	}
}

func TestMovieUnmarshalJSONKeepsOtherFields(t *testing.T) {
	var m Movie
	requireNoErr(t, json.Unmarshal([]byte(`{
	  "id":"yxY7kW","number":"SSIS-001","title":"標題一","origin_title":"標題一 完整版",
	  "cover_url":"https://c0.jdbstatic.com/c.jpg","tags":["巨乳"],"actors":["楓花戀"],
	  "magnets_count":6,"has_cnsub":true,"can_play":true,"ranking":3}`), &m))
	if m.ID != "yxY7kW" || m.Code != "SSIS-001" || m.Title != "標題一" || m.OriginTitle == "" {
		t.Fatalf("plain fields lost: %+v", m)
	}
	if m.MagnetsCount != 6 || !m.HasCNSub || !m.CanPlay || m.Ranking != 3 {
		t.Fatalf("flags lost: %+v", m)
	}
	if len(m.Tags) != 1 || len(m.Actors) != 1 {
		t.Fatalf("slices lost: %+v", m)
	}
	// Round-tripping must not invent or drop data the JSON tags describe.
	out, err := json.Marshal(m)
	requireNoErr(t, err)
	for _, key := range []string{`"id"`, `"number"`, `"title"`, `"cover_url"`, `"tags"`, `"ranking"`} {
		if !strings.Contains(string(out), key) {
			t.Errorf("marshalled movie misses %s: %s", key, out)
		}
	}
	// Source is transport metadata and must never reach a serialiser.
	if strings.Contains(string(out), `"Source"`) || strings.Contains(string(out), `"source"`) {
		t.Errorf("movie leaked internal fields: %s", out)
	}
}

func TestMovieURL(t *testing.T) {
	cases := []struct {
		movie Movie
		site  string
		want  string
	}{
		{Movie{Href: "/v/yxY7kW"}, "https://javdb.com", "https://javdb.com/v/yxY7kW"},
		{Movie{Href: "v/yxY7kW"}, "https://javdb.com/", "https://javdb.com/v/yxY7kW"},
		{Movie{Href: "https://mirr.or/v/x"}, "https://javdb.com", "https://mirr.or/v/x"},
		{Movie{ID: "yxY7kW"}, "https://javdb.com", "https://javdb.com/v/yxY7kW"},
		{Movie{Code: "SSIS-001"}, "https://javdb.com", ""},
	}
	for _, tc := range cases {
		if got := tc.movie.URL(tc.site); got != tc.want {
			t.Errorf("URL(%+v, %q) = %q, want %q", tc.movie, tc.site, got, tc.want)
		}
	}
}

func TestMagnetHelpers(t *testing.T) {
	sub := Magnet{Name: "SSIS-001 [CNSUB].mp4", Hash: "deadbeef", SizeBytes: 1 << 30}
	if sub.MagnetURL() != "magnet:?xt=urn:btih:deadbeef" {
		t.Fatalf("magnet uri: %q", sub.MagnetURL())
	}
	if sub.Category() != "subtitle" {
		t.Fatalf("category: %q", sub.Category())
	}

	// Tags count as much as the file name for the subtitle classification.
	if got := (Magnet{Name: "SSIS-001.mp4", Tags: []string{"中字"}}).Category(); got != "subtitle" {
		t.Fatalf("category from tags: %q", got)
	}
	hacked := Magnet{Name: "SSIS-001 UC無碼破解.mp4", Tags: []string{"中字"}}
	if hacked.Category() != "hacked_subtitle" {
		t.Fatalf("category: %q", hacked.Category())
	}
	if (Magnet{Name: "SSIS-001 UC無碼破解.mp4"}).Category() != "hacked_no_subtitle" {
		t.Fatal("plain leak misclassified")
	}
	if (Magnet{Name: "SSIS-001.mp4"}).Category() != "no_subtitle" {
		t.Fatal("plain torrent misclassified")
	}

	// Without a hash (login-gated rows) the PikPak link is the only handle.
	remote := Magnet{PikPakURL: "https://pikp.ak/x"}
	if remote.MagnetURL() != "https://pikp.ak/x" {
		t.Fatalf("fallback uri: %q", remote.MagnetURL())
	}
	if (Magnet{}).MagnetURL() != "" {
		t.Fatal("empty magnet must not fabricate a uri")
	}
}

func TestSortMagnetsPrefersSubtitlesThenSize(t *testing.T) {
	list := []Magnet{
		{Name: "SSIS-001.mp4", Hash: "a", SizeBytes: 9 << 30},
		{Name: "SSIS-001 UC無碼破解.mp4", Hash: "b", SizeBytes: 5 << 30},
		{Name: "SSIS-001 [CNSUB].mp4", Hash: "c", SizeBytes: 1 << 30},
		{Name: "SSIS-001 [CNSUB].mp4", Hash: "d", SizeBytes: 3 << 30, HD: true},
	}
	sortMagnets(list)
	got := make([]string, 0, len(list))
	for _, m := range list {
		got = append(got, m.Hash)
	}
	want := []string{"d", "c", "b", "a"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order: %v, want %v", got, want)
	}
}

func TestSortMagnetsTiesBreakOnSize(t *testing.T) {
	// Same category (no subtitle markers at all): the largest file wins.
	list := []Magnet{
		{Name: "SSIS-001.mp4", Hash: "small", SizeBytes: 1 << 30},
		{Name: "SSIS-001.mp4", Hash: "big", SizeBytes: 9 << 30},
	}
	sortMagnets(list)
	if list[0].Hash != "big" {
		t.Fatalf("equal categories must sort by size: %+v", list)
	}
}

func TestDetailLeadActor(t *testing.T) {
	var d *Detail
	if d.LeadActor() != nil {
		t.Fatal("nil detail must not panic")
	}
	if (&Detail{}).LeadActor() != nil {
		t.Fatal("empty billing must return nil")
	}
	full := &Detail{ActorCredits: []Actor{{ID: "21Jp", Name: "楓花戀"}, {ID: "z9", Name: "乙"}}}
	if lead := full.LeadActor(); lead == nil || lead.ID != "21Jp" {
		t.Fatalf("lead: %+v", lead)
	}
	// The returned pointer must not alias the slice (callers mutate copies).
	full.LeadActor().Name = "改名"
	if full.ActorCredits[0].Name != "楓花戀" {
		t.Fatal("LeadActor leaked a pointer into the detail")
	}
}

func TestPageDefaults(t *testing.T) {
	if got := (Page{}).pageOrDefault(1); got != 1 {
		t.Fatalf("page default: %d", got)
	}
	if got := (Page{Page: -3}).limitOrDefault(25); got != 25 {
		t.Fatalf("limit default: %d", got)
	}
	if got := (Page{Page: 4, Limit: 10}).pageOrDefault(1); got != 4 {
		t.Fatalf("explicit page: %d", got)
	}
}

func TestQueryOptionBuilders(t *testing.T) {
	q := newQuery("ssis", []QueryOption{
		WithPage(3, 50), WithCategory(CategoryUncensored), WithSort(SortMostMagnet),
		WithFilter(FilterPlayable), WithSubtitle(), WithRecent(), WithYear(2024), WithScope(ScopeActor),
	})
	if q.Page.Page != 3 || q.Page.Limit != 50 {
		t.Fatalf("WithPage wrote the wrong field: %+v", q.Page)
	}
	if q.Category != CategoryUncensored || q.Sort != SortMostMagnet || q.Year != 2024 {
		t.Fatalf("options not applied: %+v", q)
	}
	// WithSubtitle is a hint kept beside the explicit filter.
	if !q.WithSubtitle || q.Filter != FilterPlayable {
		t.Fatalf("filter options: %+v", q)
	}
	if q.Scope != ScopeActor {
		t.Fatalf("scope: %q", q.Scope)
	}
	if q.Scope.String() != string(ScopeActor) {
		t.Fatalf("scope string: %q", q.Scope.String())
	}
	if (SearchScope("")).String() != string(ScopeMovie) {
		t.Fatal("an empty scope must stringify to movies")
	}
}

func TestSearchKeySeparatesQueries(t *testing.T) {
	base := Query{Keyword: "ssis"}
	same := Query{Keyword: " SSIS "}
	if searchKey(base) != searchKey(same) {
		t.Fatalf("keyword case/space must not split cache keys: %q vs %q", searchKey(base), searchKey(same))
	}
	seen := map[string]string{searchKey(base): "base"}
	for name, q := range map[string]Query{
		"page":     {Keyword: "ssis", Page: Page{Page: 2}},
		"limit":    {Keyword: "ssis", Page: Page{Limit: 50}},
		"category": {Keyword: "ssis", Category: CategoryWestern},
		"sort":     {Keyword: "ssis", Sort: SortOldest},
		"filter":   {Keyword: "ssis", Filter: FilterWithSub},
		"subtitle": {Keyword: "ssis", WithSubtitle: true},
		"recent":   {Keyword: "ssis", FromRecent: true},
		"year":     {Keyword: "ssis", Year: 2023},
		"month":    {Keyword: "ssis", Year: 2023, Month: 5},
		"scope":    {Keyword: "ssis", Scope: ScopeActor},
		"tags":     {Keyword: "ssis", TagIDs: map[string]string{"4": "15"}},
	} {
		key := searchKey(q)
		if key == searchKey(base) {
			t.Errorf("%s shares the cache key with the base query", name)
		}
		if other, ok := seen[key]; ok {
			t.Errorf("cache key collision between %q and %q", name, other)
		}
		seen[key] = name
	}
	// Map iteration order must not change the key.
	a := searchKey(Query{Keyword: "x", TagIDs: map[string]string{"4": "15", "1": "3"}})
	b := searchKey(Query{Keyword: "x", TagIDs: map[string]string{"1": "3", "4": "15"}})
	if a != b {
		t.Fatalf("tag map ordering leaks into the cache key: %q vs %q", a, b)
	}
}

func TestTop250Slices(t *testing.T) {
	if got := Top250OfYear(2025); got.Type != "year" || got.Value != "2025" {
		t.Fatalf("year slice: %+v", got)
	}
	for name, slice := range map[string]Top250Slice{
		"censored":   Top250Censored,
		"uncensored": Top250Uncensored,
		"western":    Top250Western,
		"fc2":        Top250FC2,
	} {
		if slice.Type != "video_type" || slice.Value == "" {
			t.Errorf("%s slice: %+v", name, slice)
		}
	}
	if Top250All.Type != "all" || Top250All.Value != "" {
		t.Fatalf("all slice: %+v", Top250All)
	}
}

func TestMustJSONIsIndentedAndLossless(t *testing.T) {
	out := mustJSON(Ranking{Kind: RankingTop250, Page: 2, Movies: []Movie{{ID: "a"}}})
	if !strings.Contains(out, "\n  \"kind\": \"top250\"") {
		t.Fatalf("indentation: %q", out)
	}
	var back Ranking
	requireNoErr(t, json.Unmarshal([]byte(out), &back))
	if back.Kind != RankingTop250 || back.Page != 2 || back.Movies[0].ID != "a" {
		t.Fatalf("round trip: %+v", back)
	}
	// A value that cannot be marshalled must degrade to text, not panic.
	if got := mustJSON(make(chan int)); got == "" {
		t.Fatal("mustJSON swallowed the failure")
	}
}
