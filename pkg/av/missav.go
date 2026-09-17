package av

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/PuerkitoBio/goquery"
)

// missavUUIDRe 从页面中提取 surrit 播放列表的 uuid。
// 参考 NASSAV：形如 m3u8|<hex|-segs>|com|surrit|https|video，段序需反转。
var missavUUIDRe = regexp.MustCompile(`m3u8\|([a-f0-9|]+)\|com\|surrit\|https\|video`)

// missavFallbackDomains 是主域名被 Cloudflare 403 封锁时的备选镜像。
// 按可用性从高到低排列；missav.ai 已被封锁故不在列表中。
var missavFallbackDomains = []string{"missav.ws", "missav123.com"}

// MissAVSource 是 MissAV 数据源实现。
type MissAVSource struct {
	http   *HTTPClient
	domain string // 如 "missav.ws"
	// surritBase 是流媒体 CDN 基地址，测试可指向 stub。
	surritBase string
}

// NewMissAVSource 构造 MissAV 源，domain 为空时使用默认站点。
// missav.ai 已被 Cloudflare 403 封锁，默认使用 missav.ws（可用镜像）。
func NewMissAVSource(hc *HTTPClient, domain string) *MissAVSource {
	if domain == "" {
		domain = "missav.ws"
	}
	return &MissAVSource{http: hc, domain: domain, surritBase: "https://surrit.com"}
}

// Name 实现 Source。
func (s *MissAVSource) Name() string { return "missav" }

func (s *MissAVSource) base() string {
	if strings.HasPrefix(s.domain, "http://") || strings.HasPrefix(s.domain, "https://") {
		return strings.TrimRight(s.domain, "/")
	}
	return "https://" + s.domain
}

// padCodeDigits 将番号尾部数字段补零到三位，保留其余字符原样
// （LAFBD-41 → LAFBD-041）。MissAV 的 slug 数字段固定三位宽，搜索与详情页
// 都按补零后的形式索引；未补零 code 的直访仅靠 302 重定向兜底
// （/cn/lafbd-41 → /cn/lafbd-041），但搜索 /cn/search/LAFBD-41 搜不到任何卡片、
// 变体页 /cn/lafbd-41-uncensored-leak 404，导致 probe 失败、前端回退占位三变体
// 后无码/中字播放失败（实测 LAFBD-41 / SSIS-41 / IPX-88）。
func padCodeDigits(code string) string {
	c := strings.TrimSpace(code)
	i := strings.LastIndexFunc(c, func(r rune) bool { return r < '0' || r > '9' })
	if i < 0 || i == len(c)-1 {
		return c // 无尾部数字段（纯字母或以非数字结尾），无从补零
	}
	digits := c[i+1:]
	if n := len(digits); n < 3 {
		digits = strings.Repeat("0", 3-n) + digits
	}
	return c[:i+1] + digits
}

// missavSlug 是 MissAV 详情页/搜索索引使用的 slug 形式：小写 + 尾部数字补零
// （LAFBD-41 → lafbd-041）。卡片 href、详情页路径均为该形式。
func missavSlug(code string) string {
	return strings.ToLower(padCodeDigits(code))
}

// pageCandidates 返回番号详情页的候选 URL（不同字幕/镜像路径），逐个尝试。
// 借鉴 NASSAV missAVDownloader.getHTML 的多路径策略。
func (s *MissAVSource) pageCandidates(code string) []string {
	c := missavSlug(code)
	return []string{
		fmt.Sprintf("%s/cn/%s-chinese-subtitle", s.base(), c),
		fmt.Sprintf("%s/cn/%s-uncensored-leak", s.base(), c),
		fmt.Sprintf("%s/cn/%s", s.base(), c),
		fmt.Sprintf("%s/dm13/cn/%s", s.base(), c),
	}
}

// variantPage 表示同一番号的一个片源变体页面。
type variantPage struct {
	url  string
	kind string // "uncensored" / "cnsub" / "normal"
}

// variantCandidates 返回同一番号的全部片源变体页：
// 无码流出版（-uncensored-leak）、中文字幕版（-chinese-subtitle）、普通版与镜像。
func (s *MissAVSource) variantCandidates(code string) []variantPage {
	c := missavSlug(code)
	return []variantPage{
		{url: fmt.Sprintf("%s/cn/%s-uncensored-leak", s.base(), c), kind: "uncensored"},
		{url: fmt.Sprintf("%s/cn/%s-chinese-subtitle", s.base(), c), kind: "cnsub"},
		{url: fmt.Sprintf("%s/cn/%s", s.base(), c), kind: "normal"},
		{url: fmt.Sprintf("%s/dm13/cn/%s", s.base(), c), kind: "normal"},
	}
}

// fetchPage 拉取单个详情页并校验页面有效性（og:title 或 uuid 存在）。
// 当主域名返回 403 时自动尝试 fallback 域名。
func (s *MissAVSource) fetchPage(ctx context.Context, u string) (string, error) {
	body, err := s.http.GetWithRetry(ctx, u, s.base()+"/", 2)
	if err != nil {
		var he *HTTPError
		if errors.As(err, &he) && he.Status == http.StatusForbidden {
			// 主域名被 CF 封锁，尝试 fallback 域名
			for _, fb := range missavFallbackDomains {
				fbURL := strings.Replace(u, s.domain, fb, 1)
				if fbURL == u {
					continue // 已经是 fallback 域名
				}
				fbBody, fbErr := s.http.GetWithRetry(ctx, fbURL, "https://"+fb+"/", 2)
				if fbErr == nil {
					fbHTML := string(fbBody)
					if missavUUIDRe.MatchString(fbHTML) || ogTitleRe.MatchString(fbHTML) {
						return fbHTML, nil
					}
				}
			}
		}
		return "", err
	}
	html := string(body)
	if !missavUUIDRe.MatchString(html) && !ogTitleRe.MatchString(html) {
		return "", fmt.Errorf("%w: %s", ErrNotFound, u)
	}
	return html, nil
}

// fetchDetailPage 依次尝试候选 URL，返回首个成功的 HTML。
// 若全部失败但其中存在 Cloudflare 拦截（403），如实上抛该错误，以便上层区分"被质询"与"番号不存在"。
func (s *MissAVSource) fetchDetailPage(ctx context.Context, code string) (string, string, error) {
	var lastBlocked error
	for _, u := range s.pageCandidates(code) {
		html, err := s.fetchPage(ctx, u)
		if err != nil {
			var he *HTTPError
			if errors.As(err, &he) && he.Status == http.StatusForbidden {
				lastBlocked = err // 记住 CF 拦截
			}
			continue
		}
		return html, u, nil
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

// Probe 轻量探测番号的可用变体（仅抓取 HTML，不拉取播放列表）。
// 用于前端在详情页快速展示变体按钮，用户点击后才按需调用 ResolveVariant。
// 实现用一次搜索请求替代旧的逐个候选页探测：搜索结果列表天然聚合同一番号
// 的各变体页面链接（-uncensored-leak / -chinese-subtitle / 原片 / 镜像），
// 从卡片 href 归纳变体即可，请求数从 4 降为 1，显著降低触发 Cloudflare
// 频率限制的风险；主域名 403 时由 fetchPage 自动回退备用域名。
// 注意必须用中文站搜索 /cn/search/{code}：英文站 /search/ 的结果不收录
// 中文字幕版卡片，会漏掉中字变体（实测 START-624：cn 站返回三卡，英文站只有两张）。
func (s *MissAVSource) Probe(ctx context.Context, code string) (*ProbeResult, error) {
	code = normalizeCode(code)
	// 搜索词补零但保留大小写（MissAV 搜索不区分大小写，索引按补零 slug
	// 分词，未补零的 LAFBD-41 搜不到任何卡片）；卡片 href 匹配用小写 slug。
	u := fmt.Sprintf("%s/cn/search/%s", s.base(), url.PathEscape(padCodeDigits(code)))
	html, err := s.fetchPage(ctx, u)
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}

	variants := make([]VariantInfo, 0, 3)
	seenKind := map[string]bool{}
	doc.Find("a[href]").Each(func(_ int, sel *goquery.Selection) {
		href, _ := sel.Attr("href")
		if href == "" || isNavHref(href) {
			return
		}
		kind, ok := variantFromSlug(lastPathSegment(href), missavSlug(code))
		if !ok || seenKind[kind] {
			return // 同一变体去重（普通页与镜像页等价）
		}
		seenKind[kind] = true
		variants = append(variants, VariantInfo{
			Kind:      kind,
			Label:     variantLabel(kind),
			Available: true,
		})
	})

	if len(variants) == 0 {
		return nil, fmt.Errorf("%w: no variants found for %s", ErrNotFound, code)
	}

	// 按优先级排序：无码 > 中字 > 原片
	sortVariants(variants)

	return &ProbeResult{
		Code:     code,
		Source:   s.Name(),
		Variants: variants,
	}, nil
}

// variantFromSlug 判断详情页链接末段是否为 code 的某个变体页并返回变体类型：
// 末段等于 code（忽略大小写）为原片，或为 code 加已知后缀
// （-uncensored-leak 无码流出版、-chinese-subtitle 中文字幕版）；其余（分片、
// 无关番号等）不算变体。code 由调用方先经 missavSlug 补零（搜索结果卡片
// 的 slug 均为补零形式，如 lafbd-041）。
func variantFromSlug(slug, code string) (string, bool) {
	if code == "" {
		return "", false
	}
	seg := strings.ToLower(slug)
	c := strings.ToLower(code)
	if seg == c {
		return "normal", true
	}
	for suffix, kind := range map[string]string{
		"-uncensored-leak":  "uncensored",
		"-chinese-subtitle": "cnsub",
	} {
		if strings.TrimSuffix(seg, suffix) == c {
			return kind, true
		}
	}
	return "", false
}

// variantLabel 返回变体的展示名。
func variantLabel(kind string) string {
	switch kind {
	case "uncensored":
		return "无码"
	case "cnsub":
		return "中字"
	default:
		return "原片"
	}
}

// ResolveVariant 仅解析指定变体的播放流（按需加载）。
// variant 为空时回退到 Resolve 全量解析。
func (s *MissAVSource) ResolveVariant(ctx context.Context, code, variant string) ([]Stream, error) {
	code = normalizeCode(code)
	candidates := s.variantCandidates(code)

	// 筛选目标变体
	var target *variantPage
	for i := range candidates {
		c := &candidates[i]
		if variantMatchesKind(c.kind, variant) {
			target = c
			break
		}
	}
	if target == nil {
		// variant 不匹配任何候选，回退全量解析
		return s.Resolve(ctx, code)
	}

	html, err := s.fetchPage(ctx, target.url)
	if err != nil {
		return nil, fmt.Errorf("fetch variant page: %w", err)
	}
	uuid, ok := extractMissAVUUID(html)
	if !ok {
		return nil, fmt.Errorf("%w: no UUID in page", ErrNotFound)
	}

	playlist := fmt.Sprintf("%s/%s/playlist.m3u8", s.surritBase, uuid)
	streams, err := FetchStreams(ctx, s.http, playlist, s.base()+"/")
	if err != nil {
		return nil, fmt.Errorf("fetch playlist: %w", err)
	}

	// 打变体标记
	tags := missavPageTags(html)
	unc := target.kind == "uncensored" || tagsMatchAny(tags, missavUncensoredTagHints)
	cn := target.kind == "cnsub" || tagsMatchAny(tags, missavCNSubTagHints)
	for i := range streams {
		streams[i].Source = s.Name()
		streams[i].Uncensored = unc
		streams[i].CNSub = cn
	}

	return streams, nil
}

// variantMatchesKind 判断候选变体是否匹配目标 variant。
func variantMatchesKind(candidateKind, targetVariant string) bool {
	switch targetVariant {
	case "uncensored":
		return candidateKind == "uncensored"
	case "cnsub":
		return candidateKind == "cnsub"
	case "normal", "":
		return candidateKind == "normal"
	}
	return false
}

// sortVariants 按优先级排序变体：无码 > 中字 > 原片。
func sortVariants(variants []VariantInfo) {
	priority := map[string]int{"uncensored": 0, "cnsub": 1, "normal": 2}
	for i := 0; i < len(variants)-1; i++ {
		for j := i + 1; j < len(variants); j++ {
			if priority[variants[i].Kind] > priority[variants[j].Kind] {
				variants[i], variants[j] = variants[j], variants[i]
			}
		}
	}
}

// Resolve 实现 Source：解析番号的全部可用片源（并行遍历无码/中字/普通/镜像变体页），
// 汇总每页的多清晰度流并打上变体标记（同一 uuid 的镜像页去重）。
func (s *MissAVSource) Resolve(ctx context.Context, code string) ([]Stream, error) {
	code = normalizeCode(code)
	candidates := s.variantCandidates(code)

	var (
		mu          sync.Mutex
		wg          sync.WaitGroup
		all         []Stream
		seenUUID    = map[string]bool{}
		fetchedAny  bool
		lastBlocked error
	)
	for _, cand := range candidates {
		wg.Add(1)
		go func(cand variantPage) {
			defer wg.Done()
			html, err := s.fetchPage(ctx, cand.url)
			if err != nil {
				var he *HTTPError
				if errors.As(err, &he) && he.Status == http.StatusForbidden {
					mu.Lock()
					lastBlocked = err
					mu.Unlock()
				}
				return
			}
			uuid, ok := extractMissAVUUID(html)
			if !ok {
				return
			}
			playlist := fmt.Sprintf("%s/%s/playlist.m3u8", s.surritBase, uuid)
			streams, err := FetchStreams(ctx, s.http, playlist, s.base()+"/")
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			fetchedAny = true
			if seenUUID[uuid] {
				return // 镜像页与普通页内容相同时去重
			}
			seenUUID[uuid] = true
			// 变体标记：页面标签优先判定；标签缺失时回退按 URL 后缀判定。
			tags := missavPageTags(html)
			unc := cand.kind == "uncensored" || tagsMatchAny(tags, missavUncensoredTagHints)
			cn := cand.kind == "cnsub" || tagsMatchAny(tags, missavCNSubTagHints)
			for i := range streams {
				streams[i].Source = s.Name()
				streams[i].Uncensored = unc
				streams[i].CNSub = cn
			}
			all = append(all, streams...)
		}(cand)
	}
	wg.Wait()

	if !fetchedAny {
		if lastBlocked != nil {
			return nil, lastBlocked
		}
		return nil, fmt.Errorf("%w: %s", ErrNotFound, code)
	}
	if len(all) == 0 {
		return nil, ErrNoStream
	}
	return all, nil
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

// 片源变体的页面标签判据：MissAV 详情页给无码内容打「无码流出」等标签、
// 中字内容打「中文字幕」；比 URL 后缀更可靠（普通页也可能本就是中字/无码视频）。
var (
	missavUncensoredTagHints = []string{"无码", "無碼"}
	missavCNSubTagHints      = []string{"中文字幕", "中字"}
)

// tagsMatchAny 报告 tags 中任一项包含 hints 中任一关键字（简繁通配）。
func tagsMatchAny(tags, hints []string) bool {
	for _, t := range tags {
		for _, h := range hints {
			if strings.Contains(t, h) {
				return true
			}
		}
	}
	return false
}

// missavPageTags 提取详情页标签文本集合（选择器与 Detail 一致）。
func missavPageTags(html string) []string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil
	}
	return collectTexts(doc, "a[href*='tag'], a[href*='genre'], .tag")
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
