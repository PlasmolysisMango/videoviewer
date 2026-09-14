package av

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSite 基于 httptest 模拟一个提供 Jable 风格页面的站点，用于端到端测试
// Search → Resolve → DownloadStream 的完整链路，不依赖外网。
func fakeSite(t *testing.T) *httptest.Server {
	t.Helper()

	// 3 个分片
	segData := func(i int) []byte { return []byte(fmt.Sprintf("SEGMENT-%d-DATA", i)) }

	var mux http.ServeMux
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		// 搜索列表页
		case strings.HasPrefix(r.URL.Path, "/search/videos"):
			w.Write([]byte(`<html><body>
				<div class="group"><a href="/videos/SSIS-001/"><img data-src="/c/1.jpg" title="SSIS-001 影片甲"></a></div>
				<div class="group"><a href="/videos/SSIS-002/"><img data-src="/c/2.jpg" title="SSIS-002 影片乙"></a></div>
			</body></html>`))
		// 详情页：内联 hlsUrl 指向本站媒体播放列表
		case strings.HasPrefix(r.URL.Path, "/videos/"):
			w.Write([]byte(fmt.Sprintf(
				`<html><head><meta property="og:title" content="SSIS-001 影片甲"><meta property="og:image" content="%s/c/1.jpg"></head>
				<script>var hlsUrl = '%s/master.m3u8';</script></html>`,
				baseOf(r), baseOf(r))))
		// master playlist：两条码率流
		case r.URL.Path == "/master.m3u8":
			w.Write([]byte(fmt.Sprintf(`#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=1280x720
%s/720.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=5000000,RESOLUTION=1920x1080
%s/1080.m3u8`, baseOf(r), baseOf(r))))
		case r.URL.Path == "/720.m3u8":
			w.Write([]byte(mediaPlaylist(baseOf(r), 2)))
		case r.URL.Path == "/1080.m3u8":
			w.Write([]byte(mediaPlaylist(baseOf(r), 3)))
		// 分片
		case strings.HasPrefix(r.URL.Path, "/seg"):
			idx := 0
			fmt.Sscanf(r.URL.Path, "/seg%d.ts", &idx)
			_, _ = w.Write(segData(idx))
		default:
			http.NotFound(w, r)
		}
	})
	return httptest.NewServer(&mux)
}

func mediaPlaylist(base string, n int) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	for i := 0; i < n; i++ {
		b.WriteString(fmt.Sprintf("#EXTINF:10.0,\n%s/seg%d.ts\n", base, i))
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

func baseOf(r *http.Request) string {
	scheme := "http"
	return fmt.Sprintf("%s://%s", scheme, r.Host)
}

func TestEndToEndSearchResolveDownload(t *testing.T) {
	srv := fakeSite(t)
	defer srv.Close()

	hc, err := NewHTTPClient(Options{})
	if err != nil {
		t.Fatal(err)
	}
	// 把 fake site 的 host 作为 jable domain 注入
	host := strings.TrimPrefix(srv.URL, "http://")
	c, err := NewClient(ClientOptions{Sources: []Source{NewJableSource(hc, "http://"+host)}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// 1. Search
	videos, err := c.Search(ctx, Query{Keyword: "SSIS", Limit: 10, Source: "jable"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(videos) != 2 || videos[0].Code != "SSIS-001" {
		t.Fatalf("bad search result: %+v", videos)
	}
	if videos[0].CoverURL == "" || !strings.Contains(videos[0].Title, "影片甲") {
		t.Fatalf("card parse failed: %+v", videos[0])
	}

	// 2. Detail
	v, err := c.Detail(ctx, "SSIS-001", "jable")
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if v.Code != "SSIS-001" || v.CoverURL == "" {
		t.Fatalf("bad detail: %+v", v)
	}

	// 3. Play (resolve best)
	stream, err := c.Play(ctx, "SSIS-001", DownloadOptions{Source: "jable"})
	if err != nil {
		t.Fatalf("play: %v", err)
	}
	if !strings.Contains(stream.URL, "1080.m3u8") {
		t.Fatalf("expected 1080p best, got %q", stream.URL)
	}

	// 4. Download
	out := t.TempDir()
	res, err := c.Download(ctx, "SSIS-001", out, DownloadOptions{Source: "jable", Concurrency: 3})
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if res.FilePath != filepath.Join(out, "SSIS-001.ts") {
		t.Fatalf("bad filepath: %s", res.FilePath)
	}
	if res.Segments != 3 {
		t.Fatalf("want 3 segments got %d", res.Segments)
	}
	data, err := os.ReadFile(res.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	want := concatSegs([]int{0, 1, 2})
	if !bytes.Equal(data, want) {
		t.Fatalf("downloaded bytes mismatch:\n got %q\nwant %q", data, want)
	}
}

func concatSegs(idxs []int) []byte {
	var b bytes.Buffer
	for _, i := range idxs {
		b.WriteString(fmt.Sprintf("SEGMENT-%d-DATA", i))
	}
	return b.Bytes()
}
