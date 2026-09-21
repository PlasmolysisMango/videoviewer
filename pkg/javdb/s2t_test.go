package javdb

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestToTraditional(t *testing.T) {
	cases := [][2]string{
		{"清原美优", "清原美優"},
		{"苍井空", "蒼井空"},
		{"桃園憐奈", "桃園憐奈"},   // already traditional
		{"清原みゆう", "清原みゆう"}, // kana untouched
		{"FC2女优", "FC2女優"}, // latin untouched
		{"", ""},
	}
	for _, c := range cases {
		if got := toTraditional(c[0]); got != c[1] {
			t.Errorf("toTraditional(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

// Extension-B variant glyphs (e.g. 𫔭 for 開/开) have no glyphs in
// Android/Flutter system fonts and render as tofu; ToSimplified must
// normalize them to standard simplified characters.
func TestToSimplifiedExtVariants(t *testing.T) {
	cases := [][2]string{
		{"\U0002B52D心", "开心"}, // 𫔭 → 开
		{"张\U0002B52D", "张开"},
		{"\U00020BB7日", "吉日"}, // 𠮷 → 吉
		{"很高興\U0002B52D心", "很高兴开心"},
	}
	for _, c := range cases {
		if got := ToSimplified(c[0]); got != c[1] {
			t.Errorf("ToSimplified(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

// The web keyword search (/search?f=actor) must hit actors by alias, and the
// client must retry a simplified keyword with its traditional rewrite.
func TestClientSearchActorsWebKeywordAndSimplifiedFallback(t *testing.T) {
	actorCard := `<div id="actors" class="actors">` +
		`<div class="box actor-box">` +
		`<a href="/actors/O2qxB" title="清原美優, 清原みゆう">` +
		`<figure class="image"><img class="avatar" src="/avatars/o2/O2qxB.jpg" /></figure>` +
		`<strong>清原美優</strong></a></div></div>`
	empty := `<div id="actors" class="actors"></div>`
	st := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" && r.URL.Query().Get("f") == "actor" {
			switch r.URL.Query().Get("q") {
			case "清原美優", "清原みゆう":
				writeHTML(w, actorCard)
			default:
				writeHTML(w, empty)
			}
			return
		}
		// directory fallback: no matches anywhere
		writeHTML(w, empty)
	})
	c := newWebClient(t, st)

	// Simplified input misses; the traditional rewrite must find the actor.
	actors, err := c.SearchActors(context.Background(), Query{Keyword: "清原美优"})
	requireNoErr(t, err)
	if len(actors) != 1 || actors[0].ID != "O2qxB" {
		t.Fatalf("actors: %+v", actors)
	}
	if !strings.Contains(actors[0].OtherName, "清原みゆう") {
		t.Fatalf("alias not parsed: %+v", actors[0])
	}
	if st.count("/search") < 2 {
		t.Fatalf("expected a rewritten retry, /search hits: %d", st.count("/search"))
	}

	// Kana input hits directly (single round).
	actors2, err := c.SearchActors(context.Background(), Query{Keyword: "清原みゆう"})
	requireNoErr(t, err)
	if len(actors2) != 1 || actors2[0].ID != "O2qxB" {
		t.Fatalf("actors2: %+v", actors2)
	}
}
