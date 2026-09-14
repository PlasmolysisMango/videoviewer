package av

import (
	"testing"
	"time"
)

func TestExtractCode(t *testing.T) {
	cases := map[string]string{
		"SSIS-001":                      "SSIS-001",
		"https://missav.ai/cn/fpre-017": "FPRE-017",
		"[JUX-252] 标题":                  "JUX-252",
		"MIDV00123":                     "MIDV00123",
		"STCVS-007-C":                   "STCVS-007",
		"没有番号":                          "",
	}
	for in, want := range cases {
		if got := ExtractCode(in); got != want {
			t.Errorf("ExtractCode(%q)=%q want %q", in, got, want)
		}
	}
}

func TestExtractMissAVUUID(t *testing.T) {
	// 段序需反转：d5ba|27bf|... → 27bf-...-d5ba（此处示例三段）
	html := `... data="m3u8|aa11|bb22|cc33|com|surrit|https|video" ...`
	got, ok := extractMissAVUUID(html)
	if !ok {
		t.Fatal("expected uuid")
	}
	want := "cc33-bb22-aa11"
	if got != want {
		t.Fatalf("uuid=%q want %q", got, want)
	}
}

func TestParseDurationMinutes(t *testing.T) {
	if d := parseDurationMinutes("时长 120分钟"); d != 120*time.Minute {
		t.Errorf("120分钟 -> %v", d)
	}
	if d := parseDurationMinutes("2:13:45"); d != 2*time.Hour+13*time.Minute+45*time.Second {
		t.Errorf("2:13:45 -> %v", d)
	}
	if d := parseDurationMinutes("1小时30分钟"); d != 90*time.Minute {
		t.Errorf("1小时30分钟 -> %v", d)
	}
}

func TestStripCodeFromTitle(t *testing.T) {
	got := stripCodeFromTitle("SSIS-001 标题内容", "SSIS-001")
	if got != "标题内容" {
		t.Errorf("got %q", got)
	}
}

func TestResolveURI(t *testing.T) {
	base := "https://surrit.com/uuid/playlist.m3u8"
	if got := resolveURI(base, "seg-1.ts"); got != "https://surrit.com/uuid/seg-1.ts" {
		t.Errorf("relative: %q", got)
	}
	if got := resolveURI(base, "/abs/x.ts"); got != "https://surrit.com/abs/x.ts" {
		t.Errorf("abs path: %q", got)
	}
	if got := resolveURI(base, "https://cdn/y.ts"); got != "https://cdn/y.ts" {
		t.Errorf("absolute: %q", got)
	}
}

func TestPickBestStream(t *testing.T) {
	streams := []Stream{
		{URL: "a", Bandwidth: 1000, QualityHeight: 480},
		{URL: "b", Bandwidth: 5000, QualityHeight: 1080},
		{URL: "c", Bandwidth: 3000, QualityHeight: 720},
	}
	// 默认取最高带宽
	best, ok := pickBestStream(streams, DownloadOptions{})
	if !ok || best.URL != "b" {
		t.Fatalf("default best=%v ok=%v", best.URL, ok)
	}
	// 限制最高 720p
	best, _ = pickBestStream(streams, DownloadOptions{MaxQualityHeight: 720})
	if best.URL != "c" {
		t.Fatalf("720p cap best=%v want c", best.URL)
	}
	// 最低 1080p
	best, _ = pickBestStream(streams, DownloadOptions{MinQualityHeight: 1080})
	if best.URL != "b" {
		t.Fatalf("min1080 best=%v want b", best.URL)
	}
}
