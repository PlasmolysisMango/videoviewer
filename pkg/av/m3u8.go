package av

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

var (
	masterStreamRe = regexp.MustCompile(`(?s)#EXT-X-STREAM-INF:([^\n]*)\n([^\n#]+)`)
	bandwidthRe    = regexp.MustCompile(`BANDWIDTH=(\d+)`)
	resolutionRe   = regexp.MustCompile(`RESOLUTION=(\d+x\d+)`)
	keyRe          = regexp.MustCompile(`#EXT-X-KEY:`)
)

// ParseMasterPlaylist 解析 HLS 主播放列表，返回其中的多路码率流。
// baseURL 用于把相对 URI 归一为绝对地址。若内容不是 master（无 STREAM-INF），返回 nil。
func ParseMasterPlaylist(content, baseURL string) []Stream {
	matches := masterStreamRe.FindAllStringSubmatch(content, -1)
	var streams []Stream
	for _, m := range matches {
		attrs := m[1]
		uri := strings.TrimSpace(m[2])
		if uri == "" {
			continue
		}
		s := Stream{URL: resolveURI(baseURL, uri)}
		if bm := bandwidthRe.FindStringSubmatch(attrs); bm != nil {
			s.Bandwidth, _ = strconv.Atoi(bm[1])
		}
		if rm := resolutionRe.FindStringSubmatch(attrs); rm != nil {
			s.Resolution = rm[1]
			if parts := strings.SplitN(rm[1], "x", 2); len(parts) == 2 {
				s.QualityHeight, _ = strconv.Atoi(parts[1])
			}
		}
		streams = append(streams, s)
	}
	if len(streams) == 0 {
		return nil
	}
	sortStreams(streams)
	return streams
}

func sortStreams(s []Stream) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].Bandwidth < s[j-1].Bandwidth; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// resolveURI 依据 playlist 地址解析媒体分片/子流的相对 URI。
func resolveURI(playlistURL, ref string) string {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	if strings.HasPrefix(ref, "/") {
		return schemeHost(playlistURL) + ref
	}
	base := playlistURL
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[:i+1]
	}
	return base + ref
}

// FetchStreams 拉取一个 playlist 并返回其可播放流列表。
// 若传入的是媒体播放列表（无 STREAM-INF），返回单条流（URL 即自身）。
// 若传入的是主播放列表，返回多路码率流，并为每路附带 Referer。
func FetchStreams(ctx context.Context, hc *HTTPClient, playlistURL, referer string) ([]Stream, error) {
	body, err := hc.GetWithRetry(ctx, playlistURL, referer, 3)
	if err != nil {
		return nil, err
	}
	content := string(body)
	if streams := ParseMasterPlaylist(content, playlistURL); streams != nil {
		for i := range streams {
			streams[i].Referer = referer
		}
		return streams, nil
	}
	// 非 master：作为单一媒体流
	return []Stream{{URL: playlistURL, Referer: referer, Resolution: "media"}}, nil
}

// segmentRef 表示一个媒体分片。
type segmentRef struct {
	index int
	uri   string
}

// parseMediaSegments 从媒体播放列表内容中提取分片 URI 列表（保持顺序）。
// 返回分片列表与是否检测到 EXT-X-KEY 加密。
func parseMediaSegments(content, baseURL string) (segs []segmentRef, encrypted bool) {
	var idx int
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if keyRe.MatchString(line) {
				encrypted = true
			}
			continue
		}
		segs = append(segs, segmentRef{index: idx, uri: resolveURI(baseURL, line)})
		idx++
	}
	return segs, encrypted
}

// DownloadStream 下载一路 HLS 流到 dst，并发拉取分片并按序合并为 .ts。
// 返回下载分片数与合并后的字节数。若检测到 EXT-X-KEY 加密，返回 ErrEncryptedStream。
func DownloadStream(ctx context.Context, hc *HTTPClient, stream Stream, dst string, opt DownloadOptions) (int, int64, error) {
	opt = opt.withDefaults()

	media, err := hc.GetWithRetry(ctx, stream.URL, stream.Referer, 3)
	if err != nil {
		return 0, 0, fmt.Errorf("fetch media playlist: %w", err)
	}
	// 可能拿到的仍是 master：挑最高码率再下一层
	if sub := ParseMasterPlaylist(string(media), stream.URL); sub != nil {
		best, ok := pickBestStream(sub, opt)
		if !ok {
			return 0, 0, ErrNoStream
		}
		best.Referer = stream.Referer
		return DownloadStream(ctx, hc, best, dst, opt)
	}

	segs, encrypted := parseMediaSegments(string(media), stream.URL)
	if encrypted {
		return 0, 0, ErrEncryptedStream
	}
	if len(segs) == 0 {
		return 0, 0, ErrNoStream
	}

	blobs := make([][]byte, len(segs))
	var (
		wg     sync.WaitGroup
		sem    = make(chan struct{}, opt.Concurrency)
		failed int64
		done   int64
		first  atomic.Value
	)
	total := len(segs)
	for _, s := range segs {
		wg.Add(1)
		go func(s segmentRef) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			data, err := hc.GetWithRetry(ctx, s.uri, stream.Referer, 4)
			if err != nil {
				atomic.AddInt64(&failed, 1)
				if first.Load() == nil {
					first.Store(fmt.Errorf("segment %d: %w", s.index, err))
				}
				return
			}
			blobs[s.index] = data
			if opt.Progress != nil {
				opt.Progress(int(atomic.AddInt64(&done, 1)), total)
			}
		}(s)
	}
	wg.Wait()

	if failed > 0 {
		if e, ok := first.Load().(error); ok && e != nil {
			return total, 0, e
		}
		return total, 0, fmt.Errorf("%d segments failed", failed)
	}

	if dir := filepath.Dir(dst); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return total, 0, err
		}
	}
	f, err := os.Create(dst)
	if err != nil {
		return total, 0, err
	}
	defer f.Close()
	var written int64
	for i, b := range blobs {
		if len(b) == 0 {
			return i, written, fmt.Errorf("%w: empty segment at %d", ErrNoStream, i)
		}
		n, err := f.Write(b)
		written += int64(n)
		if err != nil {
			return i, written, err
		}
	}
	return total, written, f.Sync()
}
