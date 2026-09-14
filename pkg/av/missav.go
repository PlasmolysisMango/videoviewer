package av

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// missavUUIDRe 从页面中提取 surrit 播放列表的 uuid。
// 参考 NASSAV：形如 m3u8|<hex|-segs>|com|surrit|https|video，段序需反转。
var missavUUIDRe = regexp.MustCompile(`m3u8\|([a-f0-9|]+)\|com\|surrit\|https\|video`)

// MissAVSource 是 MissAV 数据源实现。
type MissAVSource struct {
	http   *HTTPClient
	domain string // 如 "missav.ai"
}

// NewMissAVSource 构造 MissAV 源，domain 为空时使用默认站点。
func NewMissAVSource(hc *HTTPClient, domain string) *MissAVSource {
	if domain == "" {
		domain = "missav.ai"
	}
	return &MissAVSource{http: hc, domain: domain}
}

// Name 实现 Source。
func (s *MissAVSource) Name() string { return "missav" }

func (s *MissAVSource) base() string {
	if strings.HasPrefix(s.domain, "http://") || strings.HasPrefix(s.domain, "https://") {
		return strings.TrimRight(s.domain, "/")
	}
	return "https://" + s.domain
}

// pageCandidates 返回番号详情页的候选 URL（不同字幕/镜像路径），逐个尝试。
// 借鉴 NASSAV missAVDownloader.getHTML 的多路径策略。
func (s *MissAVSource) pageCandidates(code string) []string {
	c := strings.ToLower(code)
	return []string{
		fmt.Sprintf("%s/cn/%s-chinese-subtitle", s.base(), c),
		fmt.Sprintf("%s/cn/%s-uncensored-leak", s.base(), c),
		fmt.Sprintf("%s/cn/%s", s.base(), c),
		fmt.Sprintf("%s/dm13/cn/%s", s.base(), c),
	}
}

// fetchDetailPage 依次尝试候选 URL，返回首个成功的 HTML。
// 若全部失败但其中存在 Cloudflare 拦截（403），如实上抛该错误，以便上层区分"被质询"与"番号不存在"。
func (s *MissAVSource) fetchDetailPage(ctx context.Context, code string) (string, string, error) {
	var lastBlocked error
	for _, u := range s.pageCandidates(code) {
		body, err := s.http.GetWithRetry(ctx, u, s.base()+"/", 2)
		if err != nil {
			var he *HTTPError
			if errors.As(err, &he) && he.Status == http.StatusForbidden {
				lastBlocked = err // 记住 CF 拦截
			}
			continue
		}
		html := string(body)
		// 命中详情页标志（og:title 或 uuid）才认为是有效页面
		if missavUUIDRe.MatchString(html) || ogTitleRe.MatchString(html) {
			return html, u, nil
		}
	}
	if lastBlocked != nil {
		return "", "", lastBlocked
	}
	return "", "", fmt.Errorf("%w: %s", ErrNotFound, code)
}

// Search 实现 Source。
// 优先走 Recombee 后端 API（不经 Cloudflare，抗封锁且返回结构化元数据）；
// 失败或翻页时回退到 HTML 搜索页 /search/{keyword}。
func (s *MissAVSource) Search(ctx context.Context, q Query) ([]Video, error) {
	q = q.normalized()
	kw := strings.TrimSpace(q.Keyword)
	if kw == "" {
		return nil, fmt.Errorf("missav search: empty keyword")
	}
	if q.Page <= 1 {
		if vids, err := searchMissAVRecombee(ctx, s.http, s.base(), kw, q.Limit); err == nil && len(vids) > 0 {
			return applyLimit(vids, q.Limit), nil
		}
	}
	u := fmt.Sprintf("%s/search/%s", s.base(), url.PathEscape(kw))
	if q.Page > 1 {
		u = fmt.Sprintf("%s?page=%d", u, q.Page)
	}
	return s.list(ctx, u, q)
}

// Latest 实现 Source：/new
func (s *MissAVSource) Latest(ctx context.Context, q Query) ([]Video, error) {
	q = q.normalized()
	u := s.base() + "/new"
	if q.Page > 1 {
		u = fmt.Sprintf("%s?page=%d", u, q.Page)
	}
	return s.list(ctx, u, q)
}

// SearchByActress 按演员检索（额外能力，非接口必需）。
func (s *MissAVSource) SearchByActress(ctx context.Context, q Query) ([]Video, error) {
	q = q.normalized()
	u := fmt.Sprintf("%s/actresses/%s", s.base(), url.PathEscape(q.Actress))
	if q.Page > 1 {
		u = fmt.Sprintf("%s?page=%d", u, q.Page)
	}
	return s.list(ctx, u, q)
}

func (s *MissAVSource) list(ctx context.Context, u string, q Query) ([]Video, error) {
	s.http.Warmup(ctx, s.base()+"/new", 2)
	body, err := s.http.GetWithRetry(ctx, u, s.base()+"/", 3)
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	videos := parseVideoCards(doc, s.base(), s.Name())
	return applyLimit(videos, q.Limit), nil
}

// Detail 实现 Source：解析 og 标签与演员/标签。
func (s *MissAVSource) Detail(ctx context.Context, code string) (*Video, error) {
	code = normalizeCode(code)
	html, pageURL, err := s.fetchDetailPage(ctx, code)
	if err != nil {
		return nil, err
	}
	v := &Video{Code: code, Source: s.Name(), DetailURL: pageURL}
	if t := ogTitle(html); t != "" {
		if c := ExtractCode(t); c != "" {
			v.Code = c
		}
		v.Title = stripCodeFromTitle(t, ExtractCode(t))
	}
	v.CoverURL = ogImage(html)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err == nil {
		v.Actresses = collectTexts(doc, "a[href*='actress'], a[href*='actor'], .actress")
		v.Tags = collectTexts(doc, "a[href*='tag'], a[href*='genre'], .tag")
	}
	return v, nil
}

// Resolve 实现 Source：提取 uuid → surrit playlist → 多码率流。
func (s *MissAVSource) Resolve(ctx context.Context, code string) ([]Stream, error) {
	code = normalizeCode(code)
	html, _, err := s.fetchDetailPage(ctx, code)
	if err != nil {
		return nil, err
	}
	uuid, ok := extractMissAVUUID(html)
	if !ok {
		return nil, ErrNoStream
	}
	playlist := fmt.Sprintf("https://surrit.com/%s/playlist.m3u8", uuid)
	referer := s.base()
	streams, err := FetchStreams(ctx, s.http, playlist, referer)
	if err != nil {
		return nil, err
	}
	for i := range streams {
		streams[i].Source = s.Name()
	}
	return streams, nil
}

// extractMissAVUUID 从 HTML 提取并还原 surrit uuid（段序反转，'|'→'-'）。
func extractMissAVUUID(html string) (string, bool) {
	m := missavUUIDRe.FindStringSubmatch(html)
	if m == nil {
		return "", false
	}
	parts := strings.Split(m[1], "|")
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "-"), true
}

// collectTexts 提取匹配选择器元素去重后的文本集合。
func collectTexts(doc *goquery.Document, selector string) []string {
	seen := map[string]bool{}
	var out []string
	doc.Find(selector).Each(func(_ int, e *goquery.Selection) {
		t := strings.TrimSpace(e.Text())
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	})
	return out
}

var _ Source = (*MissAVSource)(nil)
