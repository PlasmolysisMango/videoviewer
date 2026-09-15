package av

import (
	"fmt"
	"strings"
	"time"
)

// Video 表示一条视频资源的元数据。
// 融合了 missav-bot 的 Video 实体与 NASSAV 的 AVDownloadInfo。
type Video struct {
	// Code 番号（车牌号），如 "SSIS-001"，统一大写规范化后存储。
	Code string `json:"code"`
	// Title 标题（不含番号部分）。
	Title string `json:"title"`
	// DetailURL 详情页地址。
	DetailURL string `json:"detail_url"`
	// CoverURL 封面图地址。
	CoverURL string `json:"cover_url"`
	// PreviewURL 预览视频（预告片）地址，可能为空。
	PreviewURL string `json:"preview_url"`
	// Actresses 演员列表。
	Actresses []string `json:"actresses,omitempty"`
	// Tags 标签/分类列表。
	Tags []string `json:"tags,omitempty"`
	// Duration 时长，未知时为零值。
	Duration time.Duration `json:"duration"`
	// ReleaseDate 发行日期，未知时为 nil。
	ReleaseDate *time.Time `json:"release_date,omitempty"`
	// Source 数据来源标识（missav / jable / hohoj ...）。
	Source string `json:"source"`
	// M3U8 解析后的可播放主播放列表地址，仅 Detail/Resolve 场景填充。
	M3U8 string `json:"m3u8,omitempty"`
}

// String 便于日志输出。
func (v Video) String() string {
	dur := ""
	if v.Duration > 0 {
		dur = v.Duration.Round(time.Second).String()
	}
	return fmt.Sprintf("[%s] %s (source=%s, dur=%s)", v.Code, v.Title, v.Source, durEmpty(dur))
}

func durEmpty(s string) string {
	if s == "" {
		return "未知"
	}
	return s
}

// Stream 表示一路可播放的媒体流（HLS）。
type Stream struct {
	// URL m3u8 播放列表地址（已解析为绝对地址）。
	URL string `json:"url"`
	// Bandwidth 带宽（bps），来自 EXT-X-STREAM-INF。
	Bandwidth int `json:"bandwidth"`
	// Resolution 分辨率，如 "1920x1080"。
	Resolution string `json:"resolution"`
	// QualityHeight 从 Resolution 解析出的高度，便于按清晰度排序/筛选（0 表示未知）。
	QualityHeight int `json:"quality_height"`
	// Referer 下载该流时需要携带的 Referer 头（部分站点校验）。
	Referer string `json:"referer"`
	// Source 来源标识。
	Source string `json:"source"`
	// Uncensored 该流来自无码（uncensored-leak）变体页。
	Uncensored bool `json:"uncensored,omitempty"`
	// CNSub 该流来自中文字幕变体页。
	CNSub bool `json:"cnsub,omitempty"`
}

// String 输出人类可读的清晰度描述。
func (s Stream) String() string {
	q := s.Resolution
	if q == "" {
		q = "未知"
	}
	return fmt.Sprintf("%s (%dbps, %s)", s.URL, s.Bandwidth, q)
}

// Query 描述一次列表检索的条件。
type Query struct {
	// Keyword 关键词，用于 Search。
	Keyword string
	// Actress 演员名，用于按演员检索。
	Actress string
	// Tag 标签，用于按标签检索（部分数据源支持）。
	Tag string
	// Source 指定数据源；为空表示按 Client 优先级遍历全部数据源。
	Source string
	// Page 页码，从 1 开始；<=0 视为 1。
	Page int
	// Limit 期望返回的最大条数；<=0 表示不限制（由数据源默认）。
	Limit int
}

// normalized 填充默认值。
func (q Query) normalized() Query {
	if q.Page <= 0 {
		q.Page = 1
	}
	return q
}

// DownloadOptions 控制下载行为。
type DownloadOptions struct {
	// MaxQualityHeight 允许的最高清晰度高度；0 表示不限（选最高）。
	// 例如 720 表示优先选择不高于 720p 的流。
	MaxQualityHeight int
	// MinQualityHeight 允许的最低清晰度高度；0 表示不限。
	MinQualityHeight int
	// Source 指定数据源（如 "missav"）；空表示按 Client 优先级自动选择。
	Source string
	// Concurrency 分片下载并发数，默认 8。
	Concurrency int
	// RemuxToMP4 是否在下载 .ts 后调用 ffmpeg 转封装为 .mp4（需系统安装 ffmpeg）。
	RemuxToMP4 bool
	// FFmpegPath ffmpeg 可执行文件路径，默认 "ffmpeg"。
	FFmpegPath string
	// KeepTS 转 mp4 后是否保留原始 .ts 文件。
	KeepTS bool
	// Progress 进度回调，可为 nil。done/total 为已下载/总分片数。
	Progress func(done, total int)
}

// DefaultDownloadOptions 返回一组合理的默认下载选项。
func DefaultDownloadOptions() DownloadOptions {
	return DownloadOptions{
		Concurrency: 8,
		FFmpegPath:  "ffmpeg",
	}
}

func (o DownloadOptions) withDefaults() DownloadOptions {
	if o.Concurrency <= 0 {
		o.Concurrency = 8
	}
	if o.FFmpegPath == "" {
		o.FFmpegPath = "ffmpeg"
	}
	return o
}

// DownloadResult 描述一次下载的结果。
type DownloadResult struct {
	Code      string        `json:"code"`
	Source    string        `json:"source"`
	StreamURL string        `json:"stream_url"`
	FilePath  string        `json:"file_path"` // 最终产物路径（mp4 或 ts）
	TempPath  string        `json:"temp_path"` // 原始 .ts 路径（若保留）
	Segments  int           `json:"segments"`  // 下载的分片数
	Size      int64         `json:"size"`      // 最终文件字节数
	Duration  time.Duration `json:"duration"`  // 下载耗时
	Remuxed   bool          `json:"remuxed"`   // 是否已转封装为 mp4
}

// pickBestStream 从流列表中按选项挑选最合适的一路。
// 策略：先按高度过滤 [MinQualityHeight, MaxQualityHeight]，再按带宽降序，取第一。
func pickBestStream(streams []Stream, o DownloadOptions) (Stream, bool) {
	var candidates []Stream
	for _, s := range streams {
		if s.URL == "" {
			continue
		}
		if o.MaxQualityHeight > 0 && s.QualityHeight > o.MaxQualityHeight && s.QualityHeight != 0 {
			continue
		}
		if o.MinQualityHeight > 0 && s.QualityHeight != 0 && s.QualityHeight < o.MinQualityHeight {
			continue
		}
		candidates = append(candidates, s)
	}
	if len(candidates) == 0 {
		// 过滤后为空则回退到全量，避免过度过滤导致无流可用。
		candidates = streams
	}
	best, ok := highestBandwidth(candidates)
	return best, ok
}

func highestBandwidth(streams []Stream) (Stream, bool) {
	var best Stream
	found := false
	for _, s := range streams {
		if s.URL == "" {
			continue
		}
		if !found || s.Bandwidth > best.Bandwidth {
			best = s
			found = true
		}
	}
	return best, found
}

// normalizeCode 将番号规范化：去空格、转大写。
func normalizeCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}
