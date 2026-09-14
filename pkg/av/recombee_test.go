package av

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestRecombeeSignature 用固定向量校验 HMAC-SHA1 签名（与 openssl 结果对齐）。
func TestRecombeeSignature(t *testing.T) {
	unsigned := "/missav-default/search/users/anon_deadbeef/items/?frontend_timestamp=1700000000"
	got := recombeeSignature(unsigned)
	want := "d353157900301781fd950e7f9e6e009652dc1f1b"
	if got != want {
		t.Fatalf("signature=%q want %q", got, want)
	}
	// signed path 应包含时间戳与 frontend_sign
	sp := recombeeSignedPath("/search/users/anon_deadbeef/items/", 1700000000)
	if !strings.HasPrefix(sp, unsigned[:len(unsigned)-len("1700000000")]) || !strings.HasSuffix(sp, want) {
		t.Fatalf("signedPath malformed: %q", sp)
	}
}

// TestSearchMissAVRecombeeOffline 用 httptest 伪造 Recombee 响应，校验结构化映射。
func TestSearchMissAVRecombeeOffline(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "recommId":"xyz",
		  "recomms":[
		    {"id":"ssis-024-uncensored-leak","values":{"title":"SSIS-024 标题甲","duration":11676,"released_at":1616112000,"actresses":["はやのうた"],"genres":["巨乳"],"labels":["S1 NO.1 STYLE"],"markers":["S1番号ワンスタイル"],"is_uncensored_leak":true}},
		    {"id":"midv-001","values":{"title":"MIDV-001","title_en":"Midv One","duration":5400,"actresses":["abc"]}}
		  ]}`))
	}))
	defer srv.Close()

	// 注入离线端点。
	oldHost, oldScheme := recombeeHost, recombeeScheme
	recombeeHost = strings.TrimPrefix(srv.URL, "http://")
	recombeeScheme = "http"
	t.Cleanup(func() { recombeeHost, recombeeScheme = oldHost, oldScheme })

	hc, err := NewHTTPClient(Options{})
	if err != nil {
		t.Fatal(err)
	}
	videos, err := searchMissAVRecombee(context.Background(), hc, "https://missav.ai", "SSIS", 10)
	if err != nil {
		t.Fatalf("searchMissAVRecombee: %v", err)
	}
	if len(videos) != 2 {
		t.Fatalf("want 2 videos, got %d: %+v", len(videos), videos)
	}

	// 请求体校验
	if gotBody["searchQuery"] != "SSIS" {
		t.Errorf("payload searchQuery=%v want SSIS", gotBody["searchQuery"])
	}
	if !strings.Contains(gotPath, "frontend_sign=") {
		t.Errorf("signed path missing frontend_sign: %q", gotPath)
	}

	// 映射校验
	v0 := videos[0]
	if v0.Code != "SSIS-024" {
		t.Errorf("code=%q want SSIS-024", v0.Code)
	}
	if v0.Source != "missav" {
		t.Errorf("source=%q", v0.Source)
	}
	if !strings.Contains(v0.DetailURL, "missav.ai/cn/ssis-024-uncensored-leak") {
		t.Errorf("detailURL=%q", v0.DetailURL)
	}
	if v0.Duration != 11676*time.Second {
		t.Errorf("duration=%v", v0.Duration)
	}
	if len(v0.Actresses) != 1 || v0.Actresses[0] != "はやのうた" {
		t.Errorf("actresses=%v", v0.Actresses)
	}
	if v0.ReleaseDate == nil || v0.ReleaseDate.Year() != 2021 {
		t.Errorf("releaseDate=%v", v0.ReleaseDate)
	}
	// 番号应从标题/ id 剔除
	if strings.Contains(v0.Title, "SSIS-024") {
		t.Errorf("title 仍含番号: %q", v0.Title)
	}
	// 第二个：从 title_en 兜底
	if videos[1].Code != "MIDV-001" {
		t.Errorf("code1=%q want MIDV-001", videos[1].Code)
	}
}

// TestIsNavHref 校验导航/分类链接被正确剔除（修复 DM278/C1 污染）。
func TestIsNavHref(t *testing.T) {
	cases := map[string]bool{ // href -> 期望 isNavHref
		"/videos/SSIS-001/":             false,
		"https://missav.ai/cn/ssis-024": false,
		"/dm539/cn/new?id=1":            false, // 末段 new 无数字 -> 其实是 nav；下面单列
		"/dm278/chinese-subtitle":       true,
		"/dm170/weekly-hot":             true,
		"/cn/fpre-017-chinese-subtitle": false,
		"/search/SSIS":                  true,
		"/cn/today":                     true,
	}
	// "new" 无数字，应为 nav —— 修正预期
	cases["/dm539/cn/new?id=1"] = true
	for href, want := range cases {
		if got := isNavHref(href); got != want {
			t.Errorf("isNavHref(%q)=%v want %v", href, got, want)
		}
	}
}
