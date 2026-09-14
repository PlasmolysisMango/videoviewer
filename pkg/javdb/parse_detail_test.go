package javdb

import "testing"

func TestParseDetail(t *testing.T) {
	d := parseDetail(mustDoc(t, fixtureDetailHTML), "yxY7kW", "web")

	if d.ID != "yxY7kW" || d.Href != "/v/yxY7kW" || d.Source != "web" {
		t.Fatalf("identity: %+v", d.Movie)
	}
	if d.Title != "SSIS-001 標題一" {
		t.Fatalf("title: %q", d.Title)
	}
	if d.Code != "SSIS-001" {
		t.Fatalf("code: %q", d.Code)
	}
	if d.CodePrefix == nil || d.CodePrefix.Name != "SSIS" || d.CodePrefix.Kind != "video_code" {
		t.Fatalf("code prefix: %+v", d.CodePrefix)
	}
	if d.ReleaseDate != "2026-09-15" || d.Duration != 120 {
		t.Fatalf("date/duration: %q %d", d.ReleaseDate, d.Duration)
	}
	if d.Maker == nil || d.Maker.Name != "片商A" || d.Maker.ID != "zKW" || d.Maker.Kind != "maker" {
		t.Fatalf("maker: %+v", d.Maker)
	}
	if d.Publisher == nil || d.Publisher.ID != "aB3" {
		t.Fatalf("publisher: %+v", d.Publisher)
	}
	if d.Series == nil || d.Series.ID != "xY9" {
		t.Fatalf("series: %+v", d.Series)
	}
	if len(d.Directors) != 1 || d.Directors[0].ID != "d1" {
		t.Fatalf("directors: %v", d.Directors)
	}
	if d.Score != 4.45 || d.Ratings != 386 {
		t.Fatalf("rating: %+v", d.Movie)
	}

	if len(d.Genres) != 2 || d.Genres[0].Name != "巨乳" || d.Genres[0].Kind != "tag" {
		t.Fatalf("genres: %+v", d.Genres)
	}
	if len(d.Tags) != 2 || d.Tags[1] != "高中職生" {
		t.Fatalf("tags: %v", d.Tags)
	}
	if len(d.ActorCredits) != 2 {
		t.Fatalf("actor credits: %+v", d.ActorCredits)
	}
	lead := d.LeadActor()
	if lead == nil || lead.ID != "21Jp" || lead.Name != "楓花戀" {
		t.Fatalf("lead actor: %+v", lead)
	}
	if lead.Gender != "female" {
		t.Fatalf("actor gender: %q", lead.Gender)
	}
	if len(d.Actors) != 2 || d.Actors[1] != "第二人" {
		t.Fatalf("actor names: %v", d.Actors)
	}

	if d.PosterURL != "https://c0.jdbstatic.com/images/yxY7kW_poster.jpg" {
		t.Fatalf("poster: %q", d.PosterURL)
	}
	if len(d.PreviewImages) != 2 || d.PreviewImages[0] != "https://c0.jdbstatic.com/preview/1_thumb.jpg" {
		t.Fatalf("preview images: %v", d.PreviewImages)
	}
	if len(d.FanartURLs) != 2 {
		t.Fatalf("fanart: %v", d.FanartURLs)
	}
	// A blob: preview source is not a usable URL and must be dropped.
	if d.PreviewVideo != "" {
		t.Fatalf("preview video: %q", d.PreviewVideo)
	}
	if d.ReviewsCount != 25 || d.WantCount != 1234 || d.WatchedCount != 567 {
		t.Fatalf("counters: reviews=%d want=%d watched=%d", d.ReviewsCount, d.WantCount, d.WatchedCount)
	}
}

func TestParseDetailMagnets(t *testing.T) {
	d := parseDetail(mustDoc(t, fixtureDetailHTML), "yxY7kW", "web")
	if !d.MagnetsFetched {
		t.Fatal("magnets block not detected")
	}
	if len(d.Magnets) != 2 {
		t.Fatalf("got %d magnets, want 2 (ad row must be skipped): %+v", len(d.Magnets), d.Magnets)
	}
	if d.MagnetsCount != 2 || !d.HasCNSub {
		t.Fatalf("counts: %+v", d.Movie)
	}

	sub := d.Magnets[0]
	if sub.Name != "SSIS-001 中文字幕 [4K]" {
		t.Fatalf("name: %q", sub.Name)
	}
	if sub.Hash != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Fatalf("hash: %q", sub.Hash)
	}
	if sub.SizeText != "2.50GB" || sub.SizeBytes != int64(2.5*1024*1024*1024) {
		t.Fatalf("size: %q / %d", sub.SizeText, sub.SizeBytes)
	}
	if sub.Files != 3 || !sub.CNSub || sub.CreatedAt != "2026-09-16" {
		t.Fatalf("meta: %+v", sub)
	}
	if got := sub.Category(); got != "subtitle" {
		t.Fatalf("category: %q", got)
	}
	if sub.MagnetURL() != "magnet:?xt=urn:btih:abcdef0123456789abcdef0123456789abcdef01" {
		t.Fatalf("magnet url: %q", sub.MagnetURL())
	}

	plain := d.Magnets[1]
	if plain.Category() != "no_subtitle" || plain.CNSub {
		t.Fatalf("second magnet: %+v", plain)
	}
	if !plain.HD || plain.SizeBytes == 0 || plain.CreatedAt != "2026-09-10" {
		t.Fatalf("second magnet meta: %+v", plain)
	}
	if got := InferResolution(plain.Name, plain.Tags); got != 1080 {
		// The 高清 badge outranks the 720p text in the torrent name.
		t.Fatalf("resolution: %d", got)
	}
	if got := InferResolution(plain.Name, nil); got != 720 {
		t.Fatalf("resolution from name: %d", got)
	}
}

func TestParseDetailEnglishLabels(t *testing.T) {
	const html = `<div class="video-meta-panel">
	  <div class="panel-block"><strong>ID:</strong> <span class="value">ABC-001</span></div>
	  <div class="panel-block"><strong>Released Date:</strong> <span class="value">2026-01-02</span></div>
	  <div class="panel-block"><strong>Duration:</strong> <span class="value">95</span></div>
	  <div class="panel-block"><strong>Rating:</strong> <span class="value">3.1, by 42 users</span></div>
	  <div class="panel-block"><strong>Actor(s):</strong> <span class="value"><a href="/actors/a1">Someone</a></span></div>
	</div>`
	d := parseDetail(mustDoc(t, html), "abc", "web")
	if d.Code != "ABC-001" || d.ReleaseDate != "2026-01-02" || d.Duration != 95 {
		t.Fatalf("english labels: %+v", d.Movie)
	}
	if d.Score != 3.1 || d.Ratings != 42 {
		t.Fatalf("english rating: %+v", d.Movie)
	}
	if len(d.ActorCredits) != 1 || d.ActorCredits[0].Gender != "" {
		t.Fatalf("actors without gender marker: %+v", d.ActorCredits)
	}
}

func TestParseDetailWithoutMagnets(t *testing.T) {
	d := parseDetail(mustDoc(t, `<div class="movie-list"></div>`), "zz", "web")
	if d == nil || d.ID != "zz" {
		t.Fatalf("empty page: %+v", d)
	}
	if d.MagnetsFetched || len(d.Magnets) != 0 {
		t.Fatalf("magnet state: %v %+v", d.MagnetsFetched, d.Magnets)
	}
	if d.LeadActor() != nil {
		t.Fatal("no actors billed")
	}
}

func TestHashFromMagnet(t *testing.T) {
	cases := map[string]string{
		"magnet:?xt=urn:btih:DEADBEEF&dn=x": "deadbeef",
		"magnet:?xt=urn:btih:deadbeef#frag": "deadbeef",
		"magnet:?xt=urn:btih:abc":           "abc",
		"https://example.com/torrent":       "",
	}
	for uri, want := range cases {
		if got := hashFromMagnet(uri); got != want {
			t.Errorf("hashFromMagnet(%q) = %q, want %q", uri, got, want)
		}
	}
}

func TestFindSize(t *testing.T) {
	if got := findSize("2.50GB, 3 個文件"); got != "2.50GB" {
		t.Fatalf("findSize: %q", got)
	}
	if got := findSize("700 MB 1 files"); got != "700 MB" {
		t.Fatalf("findSize spaced: %q", got)
	}
	if got := findSize("無大小資訊"); got != "" {
		t.Fatalf("findSize negative: %q", got)
	}
}
