//go:build live

package av_test

// live 真实联网集成测试（build tag: live，外部测试包 av_test）
//
// 默认不参与编译，不影响 go build / go test ./...。验证的是"真实可达性"：
// 通过 browser 包（tls-client 浏览器指纹，绕过 Cloudflare）真实访问站点，
// 覆盖 连通性 / 搜索 / 最新 / 播放解析(m3u8) / 真实分片下载 全链路。
//
// 运行方式：
//
//	go test -tags live ./pkg/av -run TestLive -v          # 全部
//	go test -tags live ./pkg/av -run TestLiveMissAV -v    # 只测 missav
//	go test -tags live ./pkg/av -run TestLiveJable  -v    # 只测 jable
//
// 可选环境变量：
//
//	VL_PROXY=http://127.0.0.1:7890   无法直连站点时走代理
//	VL_CODE=SSIS-001                 指定番号（默认取"最新/搜索"列表第一条）
//	VL_PROFILE=chrome_120            浏览器指纹（默认 chrome_120）
//	VL_FULL=1                        开启整片下载（较慢，默认只下载头部若干分片）
//
// 注意：需要出口 IP 能访问目标站点（海外网络通常可直接跑通；国内需 VL_PROXY）。

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"videoviewer/pkg/av"
	"videoviewer/pkg/av/browser"
)

const uaChrome = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"

// defaultProfile 是实测能通过 Cloudflare 质询的浏览器指纹。
// 注：旧指纹（chrome_120~144）会从数据中心 IP 命中 "Just a moment" 质询；
// chrome_150 / firefox_135 可放行。可用 VL_PROFILE 覆盖。
const defaultProfile = "chrome_150"

// newLiveHTTP 构造带浏览器指纹（绕 CF）的 HTTPClient。
func newLiveHTTP(t *testing.T) *av.HTTPClient {
	t.Helper()
	profile := envOr("VL_PROFILE", defaultProfile)
	r, err := browser.New(browser.Options{
		Profile:        profile,
		Proxy:          os.Getenv("VL_PROXY"),
		Timeout:        30 * time.Second,
		FollowRedirect: true, // 站点首页常 301 跳转到语言路径，需跟随
	})
	if err != nil {
		t.Fatalf("browser.New(%s): %v", profile, err)
	}
	hc, err := av.NewHTTPClient(av.Options{Requester: r, UserAgent: uaChrome, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	return hc
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// TestLiveHTTPFingerprint 基础连通性：以浏览器指纹访问站点首页，确认真实拿到页面而非 CF 挑战页。
func TestLiveHTTPFingerprint(t *testing.T) {
	hc := newLiveHTTP(t)
	ctx := t.Context()

	for _, host := range []string{"https://missav.ai/", "https://jable.tv/"} {
		t.Run(strings.TrimPrefix(strings.TrimSuffix(host, "/"), "https://"), func(t *testing.T) {
			var body []byte
			err := retryLive(6, func() error {
				b, e := hc.GetWithRetry(ctx, host, "", 2)
				body = b
				return e
			})
			if err != nil {
				if isCFChallenge(err) {
					t.Skipf("Cloudflare 托管质询持续(数据中心 IP 信誉所致)，无法靠指纹消除: %v", err)
				}
				t.Fatalf("GET %s: %v（可能需要 VL_PROXY）", host, err)
			}
			html := string(body)
			if len(html) < 2000 {
				t.Fatalf("响应过短(%d 字节)，疑似拦截页: %s", len(html), snip(html))
			}
			lower := strings.ToLower(html)
			for _, mark := range []string{"just a moment", "cf-chl", "attention required", "turnstile"} {
				if strings.Contains(lower, mark) {
					t.Fatalf("疑似被 Cloudflare 拦截(命中 %q): %s", mark, snip(html))
				}
			}
			if !strings.Contains(lower, "<html") {
				t.Fatalf("未拿到 HTML 页面: %s", snip(html))
			}
			t.Logf("OK %s -> %d bytes", host, len(html))
		})
	}
}

// TestLiveMissAVSearch 真实搜索：missav /search/{kw}，校验结果结构并回查详情页。
func TestLiveMissAVSearch(t *testing.T) {
	s := av.NewMissAVSource(newLiveHTTP(t), "")
	ctx := t.Context()

	var videos []av.Video
	if err := retryLive(6, func() error {
		var e error
		videos, e = s.Search(ctx, av.Query{Keyword: "SSIS", Limit: 12})
		return e
	}); err != nil {
		if isCFChallenge(err) {
			t.Skipf("Cloudflare 质询持续，跳过: %v", err)
		}
		t.Fatalf("Search: %v", err)
	}
	if len(videos) == 0 {
		t.Fatal("搜索结果为空（解析规则可能已过期）")
	}
	assertVideosSane(t, videos, "missav")

	target := pickCode(videos)
	if target == "" {
		t.Fatalf("%d 条结果中无真实番号（可能误抓导航项，需收紧 parseVideoCards）；首条=%+v", len(videos), videos[0])
	}
	var det *av.Video
	if err := retryLive(6, func() error {
		var e error
		det, e = s.Detail(ctx, target)
		return e
	}); err != nil {
		if isCFChallenge(err) {
			t.Skipf("Detail 遇 CF 质询，跳过: %v", err)
		}
		t.Fatalf("Detail(%s): %v", target, err)
	}
	if det.Code == "" || det.DetailURL == "" {
		t.Fatalf("Detail 字段缺失: %+v", det)
	}
	t.Logf("搜索 %d 条；例: %s %q", len(videos), det.Code, trunc(det.Title, 40))
}

// TestLiveMissAVRecombeeSearch 验证 CF 绕过的核心：搜索走 Recombee 后端 API。
// 故意使用标准库 HTTPClient（无浏览器指纹、无代理）——若仍能拿到真实番号，
// 即证明该路径完全不经过 missav 的 Cloudflare 质询。
func TestLiveMissAVRecombeeSearch(t *testing.T) {
	hc, err := av.NewHTTPClient(av.Options{Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	s := av.NewMissAVSource(hc, "")

	videos, err := s.Search(t.Context(), av.Query{Keyword: "SSIS", Limit: 12})
	if err != nil {
		t.Fatalf("Search(recombee): %v", err)
	}
	if len(videos) == 0 {
		t.Fatal("Recombee 搜索为空")
	}
	assertVideosSane(t, videos, "missav")
	if pickCode(videos) == "" {
		t.Fatalf("无真实番号（Recombee 返回异常）；首条=%+v", videos[0])
	}
	// 元数据应丰富（Recombee 专有）：至少一条带演员或时长
	hasMeta := false
	for _, v := range videos {
		if len(v.Actresses) > 0 || v.Duration > 0 {
			hasMeta = true
			break
		}
	}
	if !hasMeta {
		t.Errorf("Recombee 结果缺少演员/时长元数据，可能回退到了 HTML 解析")
	}
	for _, v := range videos[:min(3, len(videos))] {
		t.Logf("  %-12s dur=%-8s actresses=%v", v.Code, v.Duration, v.Actresses)
	}
}

// TestLiveJableLatest 真实最新列表：jable 首页。
func TestLiveJableLatest(t *testing.T) {
	s := av.NewJableSource(newLiveHTTP(t), "")
	ctx := t.Context()

	var videos []av.Video
	if err := retryLive(6, func() error {
		var e error
		videos, e = s.Latest(ctx, av.Query{Limit: 12})
		return e
	}); err != nil {
		if isCFChallenge(err) {
			t.Skipf("Cloudflare 质询持续，跳过: %v", err)
		}
		t.Fatalf("Latest: %v", err)
	}
	if len(videos) == 0 {
		t.Fatal("最新列表为空（解析规则可能已过期）")
	}
	assertVideosSane(t, videos, "jable")
	t.Logf("最新 %d 条；例: %s %q", len(videos), videos[0].Code, trunc(videos[0].Title, 40))
}

// seedCode 为播放/下载测试取一个干净的 AV 番号：先试 Latest，拿不到再回退 Search
// （missav 的 Search 走 Recombee，番号最干净；jable 走站内搜索）。
func seedCode(ctx context.Context, t *testing.T, s av.Source) string {
	t.Helper()
	if videos, err := latestSafe(ctx, s); err == nil {
		if c := pickCode(videos); c != "" {
			return c
		}
	}
	for _, kw := range []string{"SSIS", "MIDV", "FSDSS", "abc"} {
		var videos []av.Video
		err := retryLive(4, func() error {
			var e error
			videos, e = s.Search(ctx, av.Query{Keyword: kw, Limit: 12})
			return e
		})
		if err != nil {
			if isCFChallenge(err) {
				t.Skipf("取番号途中遇 CF 质询，跳过: %v", err)
			}
			continue
		}
		if c := pickCode(videos); c != "" {
			return c
		}
	}
	t.Skip("未能从 Latest/Search 获得干净的 AV 番号（站点被封锁或解析需更新）")
	return ""
}

// latestSafe 拉取 Latest，对 CF 质询重试；返回结果（可能为空）。
func latestSafe(ctx context.Context, s av.Source) ([]av.Video, error) {
	var videos []av.Video
	err := retryLive(6, func() error {
		var e error
		videos, e = s.Latest(ctx, av.Query{Limit: 12})
		return e
	})
	return videos, err
}

// TestLiveMissAVPlayDownload 真实播放+下载：详情页 → surrit m3u8 → 多码率 → 真实下载头部若干分片。
func TestLiveMissAVPlayDownload(t *testing.T) {
	hc := newLiveHTTP(t)
	livePlayAndDownload(t, hc, av.NewMissAVSource(hc, ""))
}

// TestLiveJablePlayDownload 同上，走 jable（hlsUrl）。
func TestLiveJablePlayDownload(t *testing.T) {
	hc := newLiveHTTP(t)
	livePlayAndDownload(t, hc, av.NewJableSource(hc, ""))
}

// livePlayAndDownload 通用链路校验：Resolve 出流 → 拉媒体播放列表 → 真实下载分片并合并落盘。
func livePlayAndDownload(t *testing.T, hc *av.HTTPClient, s av.Source) {
	t.Helper()
	ctx := t.Context()

	code := os.Getenv("VL_CODE")
	if code == "" {
		code = seedCode(ctx, t, s)
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	t.Logf("测试番号: %s (source=%s)", code, s.Name())

	// 1. 播放解析
	var streams []av.Stream
	if err := retryLive(6, func() error {
		var e error
		streams, e = s.Resolve(ctx, code)
		return e
	}); err != nil {
		if isCFChallenge(err) {
			t.Skipf("Resolve 遇 CF 质询，跳过: %v", err)
		}
		t.Fatalf("Resolve(%s): %v", code, err)
	}
	if len(streams) == 0 {
		t.Fatalf("Resolve 返回 0 路流")
	}
	sort.SliceStable(streams, func(i, j int) bool { return streams[i].Bandwidth > streams[j].Bandwidth })
	best := streams[0]
	for _, st := range streams {
		if !strings.HasPrefix(st.URL, "http") {
			t.Fatalf("流地址未归一为绝对地址: %+v", st)
		}
	}
	t.Logf("解析到 %d 路流，最优: %s", len(streams), best)

	// 2. 真实拉取媒体播放列表（变体流）
	media, err := hc.GetWithRetry(ctx, best.URL, best.Referer, 3)
	if err != nil {
		t.Fatalf("GET 媒体播放列表 %s: %v", best.URL, err)
	}
	content := string(media)
	if !strings.HasPrefix(content, "#EXTM3U") {
		t.Fatalf("不是 HLS 播放列表: %s", snip(content))
	}
	if strings.Contains(content, "#EXT-X-KEY") {
		t.Skip("该流使用了 EXT-X-KEY 加密，跳过分片下载断言")
	}
	segs := segmentURIs(best.URL, content)
	if len(segs) == 0 {
		t.Fatalf("媒体播放列表无分片: %s", snip(content))
	}

	// 3. 真实下载头部 N 个分片（验证可播放性，不整片拉取）
	n := 3
	if len(segs) < n {
		n = len(segs)
	}
	var merged []byte
	for _, uri := range segs[:n] {
		data, err := hc.GetWithRetry(ctx, uri, best.Referer, 3)
		if err != nil {
			t.Fatalf("下载分片 %s: %v", uri, err)
		}
		if len(data) == 0 {
			t.Fatalf("分片为空: %s", uri)
		}
		merged = append(merged, data...)
	}
	// MPEG-TS 分片以 0x47 同步字节开头；若返回 fMP4(mp4 box) 则放行
	if merged[0] != 0x47 && !looksLikeMP4Box(merged) {
		t.Fatalf("分片内容异常，首字节 0x%02x len=%d（既非 TS 也非 MP4 box）", merged[0], len(merged))
	}

	out := t.TempDir()
	path := filepath.Join(out, code+"-head.ts")
	if err := os.WriteFile(path, merged, 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	t.Logf("真实下载完成: %s (%d 字节, %d 个分片)", path, fi.Size(), n)

	// 4. 可选：整片下载（VL_FULL=1，走 DownloadStream 全流程）
	if os.Getenv("VL_FULL") == "1" {
		full := filepath.Join(out, code+"-full.ts")
		segsN, size, derr := av.DownloadStream(ctx, hc, best, full, av.DefaultDownloadOptions())
		if derr != nil {
			t.Fatalf("DownloadStream: %v", derr)
		}
		t.Logf("整片下载完成: %s (%d 分片, %d 字节)", full, segsN, size)
	}
}

// TestLiveClientDownload 端到端：Client.Download 全流程（多源调度 + 落盘）。需 VL_FULL=1 + VL_CODE。
func TestLiveClientDownload(t *testing.T) {
	if os.Getenv("VL_FULL") != "1" {
		t.Skip("整片下载较慢，设 VL_FULL=1 开启")
	}
	code := os.Getenv("VL_CODE")
	if code == "" {
		t.Skip("整片下载请用 VL_CODE 指定番号，避免随机拉到超大文件")
	}
	profile := envOr("VL_PROFILE", defaultProfile)
	r, err := browser.New(browser.Options{Profile: profile, Proxy: os.Getenv("VL_PROXY"), Timeout: 60 * time.Second, FollowRedirect: true})
	if err != nil {
		t.Fatal(err)
	}
	c, err := av.NewClient(av.ClientOptions{HTTP: av.Options{Requester: r, UserAgent: uaChrome, Timeout: 60 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Download(context.Background(), code, t.TempDir(), av.DownloadOptions{Concurrency: 8})
	if err != nil {
		t.Fatalf("Client.Download(%s): %v", code, err)
	}
	fi, serr := os.Stat(res.FilePath)
	if serr != nil {
		t.Fatal(serr)
	}
	if fi.Size() == 0 {
		t.Fatal("产物为 0 字节")
	}
	t.Logf("下载成功: %s source=%s segments=%d size=%d remuxed=%v",
		res.FilePath, res.Source, res.Segments, res.Size, res.Remuxed)
}

// ---- 本地辅助（不依赖 av 包内部符号）----

// segmentURIs 从媒体播放列表提取分片绝对地址（保持顺序）。
func segmentURIs(playlistURL, content string) []string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, refURI(playlistURL, line))
	}
	return out
}

// refURI 用 net/url 解析相对地址（等价 av.resolveURI 的公开实现）。
func refURI(base, ref string) string {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}

func assertVideosSane(t *testing.T, videos []av.Video, source string) {
	t.Helper()
	for i, v := range videos {
		if v.Code == "" {
			t.Fatalf("[%s] 第 %d 条无番号: %+v", source, i, v)
		}
		if !strings.HasPrefix(v.DetailURL, "http") {
			t.Fatalf("[%s] %s 详情 URL 非法: %q", source, v.Code, v.DetailURL)
		}
		if v.Source != source {
			t.Fatalf("[%s] Source 标记错误: %q", source, v.Source)
		}
	}
}

// avCodeRe 匹配"真实 AV 番号"（形如 ABC-123 / SSIS-001 / STUV-007），用于过滤首页/搜索页里的导航项（如 DM278、C1）。
var avCodeRe = regexp.MustCompile(`^[A-Z]{2,}-\d{2,}[A-Z]?$`)

// pickCode 返回第一个符合真实番号格式的条目；没有则返回空串。
func pickCode(videos []av.Video) string {
	for _, v := range videos {
		if avCodeRe.MatchString(strings.ToUpper(v.Code)) {
			return strings.ToUpper(v.Code)
		}
	}
	return ""
}

// firstOrNil 安全返回首条（用于错误日志），空列表返回零值。
func firstOrNil(videos []av.Video) av.Video {
	if len(videos) == 0 {
		return av.Video{}
	}
	return videos[0]
}

// isCFChallenge 判断错误是否为 Cloudflare 间歇性质询页（托管质询无法靠指纹消除，需重试捕捉放行窗口）。
func isCFChallenge(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, m := range []string{"just a moment", "attention required", "cf-chl", "turnstile", "http 403"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// retryLive 执行 fn，仅对 CF 质询错误退避重试（其余错误立即返回）。attempts 为总尝试次数。
func retryLive(attempts int, fn func() error) error {
	var err error
	delay := 1200 * time.Millisecond
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		if !isCFChallenge(err) {
			return err
		}
		time.Sleep(delay)
		delay *= 2
	}
	return err
}

func looksLikeMP4Box(b []byte) bool {
	if len(b) < 12 {
		return false
	}
	return string(b[4:8]) == "ftyp" || string(b[4:8]) == "moof" || string(b[4:8]) == "styp"
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

func snip(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > 160 {
		return s[:160] + "..."
	}
	return s
}
