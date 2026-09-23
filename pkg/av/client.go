package av

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Client 聚合多个 Source，按优先级对外提供搜索、播放与下载能力。
type Client struct {
	http    *HTTPClient
	sources []Source          // 按优先级排序，靠前者优先
	byName  map[string]Source // 名称索引
}

// ClientOptions 配置 Client。
type ClientOptions struct {
	// HTTP 传给内部 HTTPClient；零值时使用默认。
	HTTP Options
	// Sources 指定要注册的数据源构造列表。为空时使用默认（missav → jable → hohoj）。
	// 顺序即优先级。
	Sources []Source
	// MissAVDomain / JableDomain / HohoJDomain 覆盖默认站点域名（仅在 Sources 为空时使用）。
	MissAVDomain string
	JableDomain  string
	HohoJDomain  string
}

// NewClient 构造 Client。默认注册 missav、jable、hohoj 三个数据源。
func NewClient(opt ClientOptions) (*Client, error) {
	hc, err := NewHTTPClient(opt.HTTP)
	if err != nil {
		return nil, err
	}
	c := &Client{http: hc, byName: map[string]Source{}}

	sources := opt.Sources
	if len(sources) == 0 {
		sources = []Source{
			NewMissAVSource(hc, opt.MissAVDomain),
			NewJableSource(hc, opt.JableDomain),
			NewHohoJSource(hc, opt.HohoJDomain),
		}
	}
	for _, s := range sources {
		c.Register(s)
	}
	return c, nil
}

// Register 注册一个数据源（追加到优先级末尾）。同名将被跳过。
func (c *Client) Register(s Source) {
	if s == nil {
		return
	}
	if _, ok := c.byName[s.Name()]; ok {
		return
	}
	c.byName[s.Name()] = s
	c.sources = append(c.sources, s)
}

// Sources 返回已注册数据源名称（按优先级）。
func (c *Client) Sources() []string {
	out := make([]string, 0, len(c.sources))
	for _, s := range c.sources {
		out = append(out, s.Name())
	}
	return out
}

// SetCFCookie 动态注入 Cloudflare cf_clearance Cookie，后续请求自动携带。
func (c *Client) SetCFCookie(host string, cred CFCredential) {
	c.http.SetCFCredential(host, cred)
}

// Source 按名称获取数据源。
func (c *Client) Source(name string) (Source, error) {
	s, ok := c.byName[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrSourceNotFound, name)
	}
	return s, nil
}

func (c *Client) ordered(name string) []Source {
	if name != "" {
		if s, ok := c.byName[name]; ok {
			return []Source{s}
		}
	}
	return c.sources
}

// Search 检索视频。q.Source 为空时按优先级遍历所有支持搜索的数据源并合并去重。
func (c *Client) Search(ctx context.Context, q Query) ([]Video, error) {
	if len(c.sources) == 0 {
		return nil, ErrNoSource
	}
	var merged []Video
	var lastErr error
	for _, s := range c.ordered(q.Source) {
		vs, err := s.Search(ctx, q)
		if err != nil {
			lastErr = err
			continue
		}
		merged = appendDedup(merged, vs)
		if q.Limit > 0 && len(merged) >= q.Limit {
			return applyLimit(merged, q.Limit), nil
		}
	}
	if len(merged) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return applyLimit(merged, q.Limit), nil
}

// Latest 返回最新视频，逻辑同 Search。
func (c *Client) Latest(ctx context.Context, q Query) ([]Video, error) {
	if len(c.sources) == 0 {
		return nil, ErrNoSource
	}
	var merged []Video
	var lastErr error
	for _, s := range c.ordered(q.Source) {
		vs, err := s.Latest(ctx, q)
		if err != nil {
			lastErr = err
			continue
		}
		merged = appendDedup(merged, vs)
		if q.Limit > 0 && len(merged) >= q.Limit {
			return applyLimit(merged, q.Limit), nil
		}
	}
	if len(merged) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return applyLimit(merged, q.Limit), nil
}

// Detail 按番号获取元数据，按优先级返回首个成功结果。
func (c *Client) Detail(ctx context.Context, code string, source string) (*Video, error) {
	if len(c.sources) == 0 {
		return nil, ErrNoSource
	}
	var lastErr error
	for _, s := range c.ordered(source) {
		v, err := s.Detail(ctx, code)
		if err != nil {
			log.Printf("[av] source=%s detail %s: %v", s.Name(), code, err)
			lastErr = err
			continue
		}
		return v, nil
	}
	if lastErr == nil {
		lastErr = ErrNotFound
	}
	return nil, lastErr
}

// Resolve 按番号解析所有可播放流，按优先级返回首个非空结果。
func (c *Client) Resolve(ctx context.Context, code string, source string) ([]Stream, error) {
	if len(c.sources) == 0 {
		return nil, ErrNoSource
	}
	var lastErr error
	for _, s := range c.ordered(source) {
		streams, err := s.Resolve(ctx, code)
		if err != nil || len(streams) == 0 {
			if err != nil {
				log.Printf("[av] source=%s resolve %s: %v", s.Name(), code, err)
				lastErr = err
			}
			continue
		}
		return streams, nil
	}
	if lastErr == nil {
		lastErr = ErrNoStream
	}
	return nil, lastErr
}

// Prober 是可选接口，数据源实现它即可支持轻量变体探测。
type Prober interface {
	Probe(ctx context.Context, code string) (*ProbeResult, error)
}

// VariantResolver 是可选接口，数据源实现它即可支持按变体惰性加载。
type VariantResolver interface {
	ResolveVariant(ctx context.Context, code, variant string) ([]Stream, error)
}

// Probe 轻量探测番号的可用变体（仅抓取 HTML，不拉取播放列表）。
// source 为空时按优先级逐源尝试：某源无此片/探测失败则换下一源；
// 指定 source 时只探测该源。
func (c *Client) Probe(ctx context.Context, code string, source string) (*ProbeResult, error) {
	var lastErr error
	for _, s := range c.ordered(source) {
		p, ok := s.(Prober)
		if !ok {
			continue
		}
		result, err := p.Probe(ctx, code)
		if err != nil {
			log.Printf("[av] source=%s probe %s: %v", s.Name(), code, err)
			lastErr = err
			continue
		}
		return result, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%w: no source supports probe", ErrNotImplemented)
	}
	return nil, lastErr
}

// ResolveVariant 按番号+变体解析播放流（按需加载）。
// variant 为 "uncensored" / "cnsub" / "normal" 之一；空字符串表示默认变体。
// source 为空时逐源级联：失败或无流则换下一源；指定 source 时只试该源。
func (c *Client) ResolveVariant(ctx context.Context, code, variant, source string) ([]Stream, error) {
	var lastErr error
	tried := false
	for _, s := range c.ordered(source) {
		vr, ok := s.(VariantResolver)
		if !ok {
			continue
		}
		tried = true
		streams, err := vr.ResolveVariant(ctx, code, variant)
		if err != nil {
			log.Printf("[av] source=%s resolve variant=%s %s: %v", s.Name(), variant, code, err)
			lastErr = err
			continue
		}
		if len(streams) == 0 {
			lastErr = ErrNoStream
			continue
		}
		return streams, nil
	}
	if !tried {
		// 无任何源支持按变体解析：回退全量解析（其本身已级联）
		return c.Resolve(ctx, code, source)
	}
	if lastErr == nil {
		lastErr = ErrNoStream
	}
	return nil, lastErr
}

// Play 解析并返回最适合播放的一路流（默认最高清晰度）。
func (c *Client) Play(ctx context.Context, code string, opt DownloadOptions) (*Stream, error) {
	streams, err := c.Resolve(ctx, code, opt.Source)
	if err != nil {
		return nil, err
	}
	best, ok := pickBestStream(streams, opt)
	if !ok {
		return nil, ErrNoStream
	}
	return &best, nil
}

// Download 解析流并下载合并为 .ts，按需转封装为 .mp4。
// dstDir 为输出目录，最终文件名为 {code}.mp4 或 {code}.ts。
func (c *Client) Download(ctx context.Context, code string, dstDir string, opt DownloadOptions) (*DownloadResult, error) {
	code = normalizeCode(code)
	opt = opt.withDefaults()

	// 指定变体时只解析该变体（与播放路径的惰性加载一致），否则全量解析。
	var (
		streams []Stream
		err     error
	)
	if opt.Variant != "" {
		streams, err = c.ResolveVariant(ctx, code, opt.Variant, opt.Source)
	} else {
		streams, err = c.Resolve(ctx, code, opt.Source)
	}
	if err != nil {
		return nil, err
	}
	stream, ok := pickBestStream(streams, opt)
	if !ok {
		return nil, ErrNoStream
	}

	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, err
	}
	tsPath := filepath.Join(dstDir, code+".ts")

	start := time.Now()
	segs, size, err := DownloadStream(ctx, c.http, stream, tsPath, opt)
	if err != nil {
		return nil, err
	}

	res := &DownloadResult{
		Code:      code,
		Source:    stream.Source,
		StreamURL: stream.URL,
		FilePath:  tsPath,
		TempPath:  tsPath,
		Segments:  segs,
		Size:      size,
		Duration:  time.Since(start),
	}

	if opt.RemuxToMP4 {
		mp4Path := filepath.Join(dstDir, code+".mp4")
		if err := remuxToMP4(ctx, opt.FFmpegPath, tsPath, mp4Path); err == nil {
			res.FilePath = mp4Path
			res.Remuxed = true
			if fi, serr := os.Stat(mp4Path); serr == nil {
				res.Size = fi.Size()
			}
			if !opt.KeepTS {
				_ = os.Remove(tsPath)
				res.TempPath = ""
			}
		} else {
			// 转封装失败不致命：保留 .ts 并把错误附带说明。
			res.Remuxed = false
			return res, fmt.Errorf("下载成功但转 mp4 失败(保留.ts): %w", err)
		}
	}
	return res, nil
}

// remuxToMP4 调用 ffmpeg 将 .ts 无损转封装为 .mp4（-c copy）。
func remuxToMP4(ctx context.Context, ffmpegPath, tsPath, mp4Path string) error {
	if _, err := exec.LookPath(ffmpegPath); err != nil {
		return fmt.Errorf("ffmpeg 未找到(%s): %w", ffmpegPath, err)
	}
	//nolint:gosec // 参数固定，路径来自受控拼接
	cmd := exec.CommandContext(ctx, ffmpegPath, "-y", "-i", tsPath, "-c", "copy", "-bsf:a", "aac_adtstoasc", "-f", "mp4", mp4Path)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// appendDedup 追加并按 Code 去重（保留先插入者优先级）。
func appendDedup(dst, src []Video) []Video {
	seen := map[string]bool{}
	for _, v := range dst {
		seen[v.Code] = true
	}
	for _, v := range src {
		if v.Code == "" || seen[v.Code] {
			continue
		}
		seen[v.Code] = true
		dst = append(dst, v)
	}
	return dst
}

// IsNotFound 便捷判定错误是否表示未找到资源。
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, ErrNoStream)
}
