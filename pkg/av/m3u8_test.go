package av

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const masterFixture = `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1200000,RESOLUTION=1280x720
720p/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=5600000,RESOLUTION=1920x1080
1080p/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=854x480
480p/index.m3u8`

func TestParseMasterPlaylist(t *testing.T) {
	streams := ParseMasterPlaylist(masterFixture, "https://surrit.com/uuid/playlist.m3u8")
	if len(streams) != 3 {
		t.Fatalf("want 3 streams, got %d", len(streams))
	}
	// 应按带宽升序
	if streams[0].Bandwidth > streams[1].Bandwidth || streams[1].Bandwidth > streams[2].Bandwidth {
		t.Fatalf("not sorted asc: %+v", streams)
	}
	best, ok := highestBandwidth(streams)
	if !ok || best.QualityHeight != 1080 {
		t.Fatalf("best=%+v", best)
	}
	if best.URL != "https://surrit.com/uuid/1080p/index.m3u8" {
		t.Fatalf("bad resolved url: %q", best.URL)
	}
}

func TestParseMediaSegments(t *testing.T) {
	content := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:10
#EXTINF:10.0,
seg-0.ts
#EXTINF:10.0,
seg-1.ts
#EXT-X-ENDLIST`
	segs, enc := parseMediaSegments(content, "https://x/y/playlist.m3u8")
	if enc {
		t.Fatal("should not be encrypted")
	}
	if len(segs) != 2 {
		t.Fatalf("want 2 segs got %d", len(segs))
	}
	if segs[0].uri != "https://x/y/seg-0.ts" || segs[1].uri != "https://x/y/seg-1.ts" {
		t.Fatalf("bad uris: %+v", segs)
	}
}

func TestParseMediaSegmentsEncrypted(t *testing.T) {
	content := "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"k.key\"\nseg.ts\n"
	_, enc := parseMediaSegments(content, "https://x/y/")
	if !enc {
		t.Fatal("should be encrypted")
	}
}

// ---- DownloadStream：流式写盘与断点续传 ----

const (
	segSize  = 1024
	segCount = 6
)

// newSegmentServer 启动一个提供 segCount 个固定大小分片的媒体列表服务，
// 统计每个分片的请求次数（hits[i]）供断言续传是否跳过已完成分片；
// fail 中的分片固定返回 404（不可重试错误）用于构造失败场景。
func newSegmentServer(t *testing.T, fail map[int]bool) (*httptest.Server, *[]int32) {
	t.Helper()
	h := make([]int32, segCount)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/media.m3u8":
			var b strings.Builder
			b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:10\n")
			for i := 0; i < segCount; i++ {
				fmt.Fprintf(&b, "#EXTINF:10.0,\nseg-%d.ts\n", i)
			}
			b.WriteString("#EXT-X-ENDLIST\n")
			_, _ = io.WriteString(w, b.String())
		case strings.HasPrefix(r.URL.Path, "/seg-"):
			n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/seg-"), ".ts"))
			if err != nil || n < 0 || n >= segCount {
				http.NotFound(w, r)
				return
			}
			atomic.AddInt32(&h[n], 1)
			if fail[n] {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(bytes.Repeat([]byte{byte(0x40 + n)}, segSize))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &h
}

// assertSegmentFile 校验合并文件：大小正确，且第 i 档 segSize 字节全为 0x40+i。
func assertSegmentFile(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if len(raw) != segCount*segSize {
		t.Fatalf("size = %d, want %d", len(raw), segCount*segSize)
	}
	for i := 0; i < segCount; i++ {
		for j, b := range raw[i*segSize : (i+1)*segSize] {
			if b != byte(0x40+i) {
				t.Fatalf("segment %d corrupted at byte %d: 0x%02x", i, j, b)
			}
		}
	}
}

// TestDownloadStreamStreamsToDisk 完整下载：按序流式写盘、进度上报、
// 完成后 part/meta 清理且产物内容正确。
func TestDownloadStreamStreamsToDisk(t *testing.T) {
	srv, hits := newSegmentServer(t, nil)
	hc, err := NewHTTPClient(Options{})
	if err != nil {
		t.Fatal(err)
	}
	var progresses [][2]int
	dst := filepath.Join(t.TempDir(), "v.ts")
	n, size, err := DownloadStream(t.Context(), hc, Stream{URL: srv.URL + "/media.m3u8"}, dst, DownloadOptions{
		Progress: func(done, total int) { progresses = append(progresses, [2]int{done, total}) },
	})
	if err != nil {
		t.Fatalf("DownloadStream: %v", err)
	}
	if n != segCount || size != segCount*segSize {
		t.Fatalf("n=%d size=%d, want %d/%d", n, size, segCount, segCount*segSize)
	}
	assertSegmentFile(t, dst)
	for i, c := range *hits {
		if c != 1 {
			t.Fatalf("segment %d requested %d times, want 1", i, c)
		}
	}
	if len(progresses) == 0 {
		t.Fatal("no progress reported")
	}
	if progresses[0] != [2]int{0, segCount} {
		t.Fatalf("first progress = %v, want {0 %d}", progresses[0], segCount)
	}
	if last := progresses[len(progresses)-1]; last != [2]int{segCount, segCount} {
		t.Fatalf("last progress = %v, want {%d %d}", last, segCount, segCount)
	}
	for _, p := range []string{dst + ".part", dst + ".part.meta"} {
		if _, serr := os.Stat(p); !os.IsNotExist(serr) {
			t.Fatalf("%s should be cleaned after success (stat err=%v)", p, serr)
		}
	}
}

// TestDownloadStreamResumeFromPart 断点续传：预置「前 3 个分片已写入」的
// part 与元数据，下载应跳过已写入分片、只补齐剩余分片并产出完整文件。
func TestDownloadStreamResumeFromPart(t *testing.T) {
	srv, hits := newSegmentServer(t, nil)
	hc, err := NewHTTPClient(Options{})
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "v.ts")
	var part []byte
	for i := 0; i < 3; i++ {
		part = append(part, bytes.Repeat([]byte{byte(0x40 + i)}, segSize)...)
	}
	if err := os.WriteFile(dst+".part", part, 0o644); err != nil {
		t.Fatal(err)
	}
	m := partMeta{Version: resumeVersion, Total: segCount, Done: 3, WrittenBytes: int64(len(part))}
	if err := savePartMeta(dst+".part.meta", &m); err != nil {
		t.Fatal(err)
	}

	var first [2]int
	n, size, err := DownloadStream(t.Context(), hc, Stream{URL: srv.URL + "/media.m3u8"}, dst, DownloadOptions{
		Progress: func(done, total int) {
			if first == [2]int{} {
				first = [2]int{done, total}
			}
		},
	})
	if err != nil {
		t.Fatalf("DownloadStream: %v", err)
	}
	if n != segCount || size != segCount*segSize {
		t.Fatalf("n=%d size=%d, want %d/%d", n, size, segCount, segCount*segSize)
	}
	assertSegmentFile(t, dst)
	for i, c := range *hits {
		want := int32(1)
		if i < 3 {
			want = 0
		}
		if c != want {
			t.Fatalf("segment %d requested %d times, want %d (resume must skip written segments)", i, c, want)
		}
	}
	if first != [2]int{3, segCount} {
		t.Fatalf("first progress = %v, want {3 %d}", first, segCount)
	}
}

// TestDownloadStreamResumeRejectedOnMismatch 断点身份不符（总分片数变化）时
// 必须丢弃旧断点、从头下载，产物仍完整正确。
func TestDownloadStreamResumeRejectedOnMismatch(t *testing.T) {
	srv, hits := newSegmentServer(t, nil)
	hc, err := NewHTTPClient(Options{})
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "v.ts")
	stale := bytes.Repeat([]byte{0x55}, 3*segSize)
	if err := os.WriteFile(dst+".part", stale, 0o644); err != nil {
		t.Fatal(err)
	}
	m := partMeta{Version: resumeVersion, Total: segCount - 1, Done: 3, WrittenBytes: int64(len(stale))}
	if err := savePartMeta(dst+".part.meta", &m); err != nil {
		t.Fatal(err)
	}
	if _, _, err := DownloadStream(t.Context(), hc, Stream{URL: srv.URL + "/media.m3u8"}, dst, DownloadOptions{}); err != nil {
		t.Fatalf("DownloadStream: %v", err)
	}
	assertSegmentFile(t, dst)
	for i, c := range *hits {
		if c != 1 {
			t.Fatalf("segment %d requested %d times, want 1 (stale resume must be discarded)", i, c)
		}
	}
}

// TestDownloadStreamKeepsResumeOnFailure 分片下载失败（404 不可重试）时：
// 不产出最终文件，但 part 与元数据保留且「part 大小 == 元数据边界」，
// 保证重试可继续。
func TestDownloadStreamKeepsResumeOnFailure(t *testing.T) {
	srv, hits := newSegmentServer(t, map[int]bool{5: true})
	hc, err := NewHTTPClient(Options{})
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "v.ts")
	_, _, err = DownloadStream(t.Context(), hc, Stream{URL: srv.URL + "/media.m3u8"}, dst, DownloadOptions{})
	if err == nil {
		t.Fatal("want segment failure, got nil")
	}
	if _, serr := os.Stat(dst); !os.IsNotExist(serr) {
		t.Fatalf("final file should not exist: %v", serr)
	}
	if got := atomic.LoadInt32(&(*hits)[5]); got != 1 {
		t.Fatalf("failing segment requested %d times, want 1 (404 is not retryable)", got)
	}
	raw, merr := os.ReadFile(dst + ".part.meta")
	if merr != nil {
		t.Fatalf("resume meta should be kept: %v", merr)
	}
	var m partMeta
	if uerr := json.Unmarshal(raw, &m); uerr != nil {
		t.Fatalf("bad resume meta: %v", uerr)
	}
	if m.Version != resumeVersion || m.Total != segCount || m.Done < 0 || m.Done >= segCount {
		t.Fatalf("bad resume meta: %+v", m)
	}
	fi, ferr := os.Stat(dst + ".part")
	if ferr != nil {
		t.Fatalf("part should be kept: %v", ferr)
	}
	if fi.Size() != m.WrittenBytes {
		t.Fatalf("part size %d != meta written_bytes %d", fi.Size(), m.WrittenBytes)
	}
}

// TestDownloadStreamCancelKeepsResume 取消下载：不产出最终文件，但保留
// part 与元数据，供之后重试续传。
func TestDownloadStreamCancelKeepsResume(t *testing.T) {
	// 分片响应前挂起（并随客户端断开立即退出），确保取消发生在下载中途。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/media.m3u8" {
			_, _ = io.WriteString(w, "#EXTM3U\n#EXTINF:10.0,\nseg-0.ts\n#EXT-X-ENDLIST\n")
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(800 * time.Millisecond):
		}
		_, _ = w.Write(bytes.Repeat([]byte{0x47}, segSize))
	}))
	defer srv.Close()
	hc, err := NewHTTPClient(Options{})
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "v.ts")
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	if _, _, err := DownloadStream(ctx, hc, Stream{URL: srv.URL + "/media.m3u8"}, dst, DownloadOptions{}); err == nil {
		t.Fatal("want context error, got nil")
	}
	if _, serr := os.Stat(dst); !os.IsNotExist(serr) {
		t.Fatalf("final file should not exist: %v", serr)
	}
	for _, p := range []string{dst + ".part", dst + ".part.meta"} {
		if _, serr := os.Stat(p); serr != nil {
			t.Fatalf("%s should be kept after cancel: %v", p, serr)
		}
	}
}

// TestRateLimiterWaitEnforcesPace 限速等待必须循环补齐：进度落后限速曲线
// 超过单次等待上限时不能提前放行，否则并发的分片下载会整体超速。
func TestRateLimiterWaitEnforcesPace(t *testing.T) {
	lim := newRateLimiter(128 * 1024) // 128KB/s
	lim.add(160 * 1024)               // 已用 160KB：理论上还需 1.25s 才能放行下一批
	start := time.Now()
	lim.wait(t.Context())
	elapsed := time.Since(start)
	if elapsed < 1150*time.Millisecond {
		t.Fatalf("wait returned after %v, want >= ~1.25s (pacing must loop, not cap at 1s)", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("wait took %v, want <= ~1.25s", elapsed)
	}
}
