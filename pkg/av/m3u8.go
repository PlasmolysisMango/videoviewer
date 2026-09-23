package av

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
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

// DownloadStream 下载一路 HLS 流到 dst，并发拉取分片并按序号流式写入
// {dst}.part（内存仅暂存乱序完成的少量分片），全部完成后原子重命名为 dst。
// 中断/取消时保留 part 与断点元数据，对同一流重试时自动续传（跳过已写入分片）。
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
	total := len(segs)

	if dir := filepath.Dir(dst); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 0, 0, err
		}
	}
	partPath := dst + partSuffix
	metaPath := partPath + metaSuffix

	// 断点续传：元数据与当前下载身份一致且 part 不小于记录偏移时，从断点继续。
	meta, resumable := loadResume(metaPath, partPath, total, stream, opt)
	if !resumable {
		meta = &partMeta{
			Version:       resumeVersion,
			Source:        stream.Source,
			Variant:       opt.Variant,
			QualityHeight: stream.QualityHeight,
			Total:         total,
		}
	}
	f, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	if resumable {
		// 裁剪到记录的边界偏移：丢弃上次中断时可能写入的半截分片。
		if err := f.Truncate(meta.WrittenBytes); err != nil {
			return 0, 0, err
		}
	} else {
		// 不可续传：清空 part 与陈旧元数据，从零开始。
		if err := f.Truncate(0); err != nil {
			return 0, 0, err
		}
		_ = os.Remove(metaPath)
	}
	if _, err := f.Seek(meta.WrittenBytes, io.SeekStart); err != nil {
		return 0, 0, err
	}

	// 固定 worker 池 + 滑动窗口调度（见 streamDownloader）：只在窗口内
	// 分发任务，下一个待写入分片必然会被某个 worker 下载，不存在
	// 「窗口被占满而阻塞按序推进」的死锁。
	dlCtx, cancelDl := context.WithCancel(ctx)
	defer cancelDl()
	dl := newStreamDownloader(f, meta, metaPath, total, 2*opt.Concurrency, cancelDl, opt.Progress)
	if opt.Progress != nil {
		opt.Progress(meta.Done, total) // 续传起点立即上报，避免 UI 从零等待
	}

	// 失败或取消时中止全部在途下载，并唤醒等待窗口的 worker。
	watchDone := make(chan struct{})
	go func() {
		select {
		case <-dlCtx.Done():
			dl.abort(dlCtx.Err())
		case <-watchDone:
		}
	}()

	limiter := newRateLimiter(opt.SpeedLimitBytesPerSec)
	var wg sync.WaitGroup
	for w := 0; w < opt.Concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dl.work(dlCtx, hc, limiter, segs, stream.Referer)
		}()
	}
	wg.Wait()
	close(watchDone)

	done, written, derr := dl.state()
	if ctx.Err() != nil {
		dl.flush() // 保留断点进度，供重试续传
		return total, 0, ctx.Err()
	}
	if derr != nil {
		dl.flush() // 保留断点进度，供重试续传
		return total, 0, derr
	}
	if done < total {
		dl.flush()
		return total, 0, fmt.Errorf("download stalled at segment %d/%d", done, total)
	}
	if err := f.Sync(); err != nil {
		return total, 0, err
	}
	if err := f.Close(); err != nil {
		return total, 0, err
	}
	if err := os.Rename(partPath, dst); err != nil {
		return total, 0, err
	}
	_ = os.Remove(metaPath)
	return total, written, nil
}

const (
	// partSuffix / metaSuffix 定义断点续传的伴生文件：{dst}.part 是未完成
	// 的下载文件，{dst}.part.meta 记录已完成分片数与写入边界偏移。
	partSuffix = ".part"
	metaSuffix = ".meta"

	// resumeVersion 是断点元数据的版本号，不一致时放弃续传。
	resumeVersion = 1

	// 断点元数据落盘节流：每写满 metaFlushEvery 个分片或间隔
	// metaFlushInterval 落盘一次；崩溃时最多丢失该窗口内的进度（重试时重下）。
	metaFlushEvery    = 16
	metaFlushInterval = 2 * time.Second
)

// partMeta 是断点续传的元数据（{dst}.part.meta）：记录下载身份信息
// （来源/变体/清晰度/总分片数）与已按序写入的分片数、边界字节偏移。
type partMeta struct {
	Version       int       `json:"version"`
	Source        string    `json:"source"`
	Variant       string    `json:"variant"`
	QualityHeight int       `json:"quality_height"`
	Total         int       `json:"total_segments"`
	Done          int       `json:"done_segments"`
	WrittenBytes  int64     `json:"written_bytes"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// loadResume 载入断点状态。元数据与当前下载身份不一致（换清晰度/变体、
// 分片数变化、版本升级）或 part 文件小于记录偏移时，返回不可续传。
func loadResume(metaPath, partPath string, total int, stream Stream, opt DownloadOptions) (*partMeta, bool) {
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, false
	}
	var m partMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false
	}
	if m.Version != resumeVersion || m.Total != total ||
		m.Source != stream.Source || m.Variant != opt.Variant ||
		m.QualityHeight != stream.QualityHeight {
		return nil, false
	}
	if m.Done <= 0 || m.Done >= total || m.WrittenBytes <= 0 {
		return nil, false
	}
	fi, err := os.Stat(partPath)
	if err != nil || fi.Size() < m.WrittenBytes {
		return nil, false
	}
	return &m, true
}

// savePartMeta 原子落盘断点元数据（临时文件 + rename）。
func savePartMeta(path string, m *partMeta) error {
	m.UpdatedAt = time.Now()
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// streamDownloader 以固定 worker 池做「滑动窗口」分片下载，并把下载完成
// 的分片按序号、流式追加写入 part 文件。
// 窗口以「在途 + 待写入」分片数为界（内存占用常数级）：只在窗口内分发
// 任务——下一个待写入的分片始终在窗口内，必然会被某个 worker 下载，
// 因此不会出现「窗口被占满而与按序推进互相等待」的死锁。
// 写入推进按节流频率落盘断点元数据，供中断后续传。
type streamDownloader struct {
	mu   sync.Mutex
	cond *sync.Cond
	f    *os.File
	meta *partMeta

	metaPath string
	total    int            // 分片总数
	window   int            // 窗口上限：在途 + 待写入分片数
	issued   int            // 已分发的下一个分片序号（窗口右边界）
	inflight int            // 在途（下载中）分片数
	pending  map[int][]byte // 已下载待写入的乱序分片
	next     int            // 下一个待写入的分片序号
	written  int64          // 已写入字节数（含续传前缀）

	lastFlush time.Time
	nextFlush int
	err       error
	cancel    context.CancelFunc // 分片失败时中止其余在途下载
	progress  func(done, total int)
}

func newStreamDownloader(f *os.File, meta *partMeta, metaPath string, total, window int, cancel context.CancelFunc, progress func(done, total int)) *streamDownloader {
	if window < 2 {
		window = 2
	}
	d := &streamDownloader{
		f:         f,
		meta:      meta,
		metaPath:  metaPath,
		total:     total,
		window:    window,
		issued:    meta.Done,
		pending:   make(map[int][]byte),
		next:      meta.Done,
		written:   meta.WrittenBytes,
		lastFlush: time.Now(),
		nextFlush: meta.Done + metaFlushEvery,
		cancel:    cancel,
		progress:  progress,
	}
	d.cond = sync.NewCond(&d.mu)
	return d
}

// acquire 为 worker 分配下一个待下载的分片：窗口（在途 + 待写入）
// 有空位时按序分发；全部分发完或失败/取消时返回 false。
func (d *streamDownloader) acquire() (int, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for {
		if d.err != nil || d.next >= d.total {
			return 0, false
		}
		if d.issued < d.total && d.inflight+len(d.pending) < d.window {
			idx := d.issued
			d.issued++
			d.inflight++
			return idx, true
		}
		if d.issued >= d.total && d.inflight == 0 {
			// 不应发生：无在途下载也没有可写分片，但仍有未写分片。
			d.err = fmt.Errorf("download stalled at segment %d/%d", d.next, d.total)
			d.cond.Broadcast()
			return 0, false
		}
		d.cond.Wait() // 窗口满或等最后一个在途分片完成
	}
}

// work 是单个下载 worker 的主循环：取任务 → 限速 → 下载 → 写回。
func (d *streamDownloader) work(ctx context.Context, hc *HTTPClient, limiter *rateLimiter, segs []segmentRef, referer string) {
	for {
		idx, ok := d.acquire()
		if !ok {
			return
		}
		limiter.wait(ctx) // 限速：进度领先于限速曲线时先补齐等待
		data, err := hc.GetWithRetry(ctx, segs[idx].uri, referer, 4)
		if err == nil && len(data) == 0 {
			err = fmt.Errorf("%w: empty segment at %d", ErrNoStream, idx)
		}
		if err == nil {
			limiter.add(int64(len(data)))
			d.complete(idx, data)
			continue
		}
		if ctx.Err() != nil {
			d.drop() // 已被取消/中止：归还额度，不登记错误
		} else {
			d.fail(idx, err)
		}
		return
	}
}

// complete 登记一个已下载分片：入待写队列并推进按序写盘。
func (d *streamDownloader) complete(idx int, data []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.inflight--
	d.pending[idx] = data
	d.drainLocked()
	d.cond.Broadcast()
}

// fail 处理分片下载失败：记录首个错误、中止其余在途下载并唤醒 worker。
func (d *streamDownloader) fail(idx int, err error) {
	d.mu.Lock()
	d.inflight--
	if d.err == nil {
		d.err = fmt.Errorf("segment %d: %w", idx, err)
	}
	cancel := d.cancel
	d.cond.Broadcast()
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// drop 归还一个被中止分片的额度（取消场景，不登记错误）。
func (d *streamDownloader) drop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.inflight--
	d.cond.Broadcast()
}

// drainLocked 按序写出 pending 中已连续就绪的分片。调用方需持有 d.mu。
func (d *streamDownloader) drainLocked() {
	for d.err == nil {
		data, ok := d.pending[d.next]
		if !ok {
			return
		}
		delete(d.pending, d.next)
		n, err := d.f.Write(data)
		d.written += int64(n)
		if err != nil {
			d.err = err
			d.cond.Broadcast()
			return
		}
		d.next++
		d.meta.Done = d.next
		d.meta.WrittenBytes = d.written
		if d.progress != nil {
			d.progress(d.next, d.meta.Total)
		}
		if d.next >= d.nextFlush || time.Since(d.lastFlush) >= metaFlushInterval {
			d.flushLocked()
		}
	}
}

// flushLocked 落盘断点元数据；落盘失败视为致命（进度无法可靠恢复）。
func (d *streamDownloader) flushLocked() {
	if err := savePartMeta(d.metaPath, d.meta); err != nil {
		if d.err == nil {
			d.err = fmt.Errorf("save resume meta: %w", err)
		}
		d.cond.Broadcast()
		return
	}
	d.lastFlush = time.Now()
	d.nextFlush = d.next + metaFlushEvery
}

// flush 强制落盘当前进度（退出路径调用，固定中断点供后续续传）。
func (d *streamDownloader) flush() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.flushLocked()
}

// abort 由外部取消/中止触发：唤醒全部等待者；无既有错误时记录原因。
func (d *streamDownloader) abort(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err == nil {
		d.err = err
	}
	d.cond.Broadcast()
}

// state 返回写入进度与错误（全部下载结束、无并发后读取）。
func (d *streamDownloader) state() (done int, written int64, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.next, d.written, d.err
}

// rateLimiter 为并发分片下载提供整体速率上限（窗口平均法）：所有 worker
// 共享同一实例，以「累计字节 ÷ 限速值 = 理论耗时」与实际耗时的差值决定
// 等待时长，使总下载速率趋近设定值；limit<=0 时全程零开销。
type rateLimiter struct {
	limit int64 // 字节/秒，<=0 表示不限制
	mu    sync.Mutex
	bytes int64
	start time.Time
}

func newRateLimiter(limit int64) *rateLimiter { return &rateLimiter{limit: limit} }

// wait 在发起下一次分片请求前调用：进度领先限速曲线时睡眠补齐差值。
// 循环复查直到累计速率回落到限速以内（单次最多睡 1 秒以保持对 ctx 取消
// 的响应）；只在截断后放行会让并发 worker 合计速率超出设定值。
func (r *rateLimiter) wait(ctx context.Context) {
	if r == nil || r.limit <= 0 {
		return
	}
	for {
		r.mu.Lock()
		if r.start.IsZero() {
			r.mu.Unlock()
			return
		}
		target := time.Duration(r.bytes * int64(time.Second) / r.limit)
		elapsed := time.Since(r.start)
		r.mu.Unlock()

		d := target - elapsed
		if d <= 0 {
			return
		}
		if d > time.Second {
			d = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
		}
	}
}

// add 在分片下载完成后记录已下载字节数。
func (r *rateLimiter) add(n int64) {
	if r == nil || r.limit <= 0 || n <= 0 {
		return
	}
	r.mu.Lock()
	if r.start.IsZero() {
		r.start = time.Now()
	}
	r.bytes += n
	r.mu.Unlock()
}
