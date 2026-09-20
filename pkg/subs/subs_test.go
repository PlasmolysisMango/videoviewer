package subs

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestNormalizeCode(t *testing.T) {
	cases := map[string]string{
		"ssis-414":  "SSIS-414",
		" SSIS 414": "SSIS-414",
		"SSIS--414": "SSIS-414",
		" SSIS-414": "SSIS-414",
	}
	for in, want := range cases {
		if got := NormalizeCode(in); got != want {
			t.Errorf("NormalizeCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToUTF8(t *testing.T) {
	// UTF-8 passes through unchanged.
	in := []byte("1\n00:00:01,000 --> 00:00:02,000\n中文字幕\n")
	if got := ToUTF8(in); !bytes.Equal(got, in) {
		t.Errorf("UTF-8 payload altered: %q", got)
	}
	// BOM is stripped.
	if got := ToUTF8(append([]byte{0xEF, 0xBB, 0xBF}, in...)); bytes.HasPrefix(got, []byte{0xEF}) {
		t.Errorf("UTF-8 BOM not stripped")
	}
	// GBK is transcoded (language hint picks GB18030 first).
	gbk, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("简体中文字幕测试"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ToUTF8WithLang(gbk, "zh"); string(got) != "简体中文字幕测试" {
		t.Errorf("GBK transcode failed: %q", got)
	}
}

func TestRankLangs(t *testing.T) {
	items := []Item{
		{Lang: "ja", Downloads: 5},
		{Lang: "en"},
		{Lang: "zh-TW"},
		{Lang: "fr"},
		{Lang: "zh-CN", Downloads: 9},
	}
	rankLangs(items)
	if items[0].Lang != "zh-CN" || items[1].Lang != "zh-TW" || items[2].Lang != "en" {
		t.Errorf("unexpected order: %v %v %v", items[0].Lang, items[1].Lang, items[2].Lang)
	}
}

func TestSubtitlecatSearchAndDownload(t *testing.T) {
	// Real search pages use site-relative hrefs (no leading slash).
	searchHTML := `<table>
	<tr><td><a href="subs/1671/SSIS-414%20jp.html">SSIS-414 jp (translated from Japanese)</a></td><td>&nbsp;</td><td>Size 131 KB</td><td>Downloads 2 downloads</td></tr>
	<tr><td>unrelated row without link</td></tr>
	</table>`
	// Real detail pages use "/subs/..." hrefs with unencoded spaces.
	detailHTML := `<div>some page</div>
	<a href="/subs/1671/SSIS-414 jp-en.srt">english</a>
	<a href="/subs/1671/SSIS-414 jp-zh-TW.srt">chinese</a>`
	srtContent := "1\n00:00:00,001 --> 00:00:04,680\n今晚是柯南\n"

	var gotSRT bool
	mux := http.NewServeMux()
	mux.HandleFunc("/index.php", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "search=SSIS-414") {
			t.Errorf("search query = %q", r.URL.RawQuery)
		}
		w.Write([]byte(searchHTML))
	})
	mux.HandleFunc("/subs/1671/SSIS-414%20jp.html", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(detailHTML))
	})
	mux.HandleFunc("/subs/1671/", func(w http.ResponseWriter, r *http.Request) {
		gotSRT = true
		if r.Referer() == "" {
			t.Errorf("srt download without referer")
		}
		w.Write([]byte(srtContent))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src := &subtitlecatSource{http: newFetcher("", 0), base: srv.URL}
	items, err := src.Search(context.Background(), "ssis-414")
	if err != nil {
		t.Fatal(err)
	}
	// The detail hub is split into one item per discovered language.
	if len(items) != 2 {
		t.Fatalf("items = %d (%v), want 2", len(items), items)
	}
	for _, it := range items {
		if it.Size != "131 KB" || it.Downloads != 2 {
			t.Errorf("meta = %q %d", it.Size, it.Downloads)
		}
		if it.Ref != "/subs/1671/SSIS-414%20jp.html" {
			t.Errorf("ref = %q (site-relative href must normalize)", it.Ref)
		}
	}
	var zhRef string
	for _, it := range items {
		if it.Lang == "zh-tw" {
			zhRef = it.Ref
		}
	}
	if zhRef == "" {
		t.Fatalf("zh-tw item missing: %v", items)
	}

	// No explicit lang: the zh-TW variant must win over en.
	d, err := src.Download(context.Background(), zhRef, "")
	if err != nil {
		t.Fatal(err)
	}
	if !gotSRT || d.Lang != "zh-tw" || !strings.Contains(string(d.Body), "今晚是柯南") {
		t.Errorf("download = lang %q, gotSRT %v, body %q", d.Lang, gotSRT, d.Body[:30])
	}
}

func TestAVSubtitlesDownloadFlow(t *testing.T) {
	// Real search results link movie pages; the movie page links its subtitles.
	searchHTML := `<html><body>
	<a href="/movie59595/hez-773--ena-koume-2025"><img src="/img/x.jpg"></a>
	<a href="/movie59595/hez-773--ena-koume-2025">[HEZ-773] - Ena Koume</a>
	<a href="/movie59595/hez-773--ena-koume-2025">Details</a>
	<a href="https://theporndude.com/22354/avsubtitles-review">review</a>
	</body></html>`
	movieHTML := `<html><body>
	<a href="/movie59595/hez-773--ena-koume-2025/subtitles/ja/140346">Subtitles details</a>
	<a href="/movie59595/hez-773--ena-koume-2025/subtitles/en/140347">Subtitles details</a>
	</body></html>`
	subHTML := `<div class="subtitles_content_label">Filename:</div>
	<div class="subtitles_content_value"><span class="text-mono">hez_773-20260918134848.zip</span> (38.7 KB)</div>
	<form method="get" target="_blank" action="/download_page.php">
	  <input type="hidden" name="subid" value="140347" />
	  <input type="hidden" name="revid" value="20260918134848" />
	</form>`
	gateHTML := `<a href="./download_sub.php?subid=140347&revid=20260918134848">Download</a>`

	var sawSessionCookie bool
	mux := http.NewServeMux()
	mux.HandleFunc("/search_results.php", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(searchHTML))
	})
	mux.HandleFunc("/movie59595/hez-773--ena-koume-2025", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(movieHTML))
	})
	mux.HandleFunc("/movie59595/hez-773--ena-koume-2025/subtitles/en/140347", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "PHPSESSID", Value: "anon123", Path: "/"})
		w.Write([]byte(subHTML))
	})
	mux.HandleFunc("/download_page.php", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(gateHTML))
	})
	mux.HandleFunc("/download_sub.php", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("PHPSESSID"); err != nil || c.Value != "anon123" {
			t.Errorf("download_sub without session cookie: %v", err)
		} else {
			sawSessionCookie = true
		}
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		f, _ := zw.Create("HEZ-773 test.en.srt")
		f.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\nSix months have passed\n"))
		zw.Close()
		w.Header().Set("Content-Type", "application/zip")
		w.Write(buf.Bytes())
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src := &avsubtitlesSource{http: newFetcher("", 0), base: srv.URL}
	items, err := src.Search(context.Background(), "HEZ-773")
	if err != nil {
		t.Fatal(err)
	}
	// The en/140347 entry must be present and resolvable.
	ref := ""
	for _, it := range items {
		if it.Lang == "en" && strings.HasSuffix(it.Ref, "140347") {
			ref = it.Ref
		}
	}
	if ref == "" {
		t.Fatalf("en subtitle not found in %d items", len(items))
	}
	d, err := src.Download(context.Background(), ref, "")
	if err != nil {
		t.Fatal(err)
	}
	if !sawSessionCookie {
		t.Errorf("session cookie never reached download_sub.php")
	}
	if d.Lang != "en" || !strings.Contains(string(d.Body), "Six months have passed") {
		t.Errorf("download = lang %q body %q", d.Lang, d.Body)
	}
}

func TestUnpackZipSRTRejectsNonZip(t *testing.T) {
	if b, _ := unpackZipSRT([]byte("<html>not a zip</html>")); b != nil {
		t.Errorf("non-zip payload returned %q", b)
	}
}
