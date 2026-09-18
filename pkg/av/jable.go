package av

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// jableHlsRe 提取 Jable 详情页内联的 hlsUrl。参考 NASSAV jableDownloder。
var jableHlsRe = regexp.MustCompile(`var\s+hlsUrl\s*=\s*'(https?://[^']+)'`)

// JableSource 是 Jable 数据源实现。
type JableSource struct {
	http   *HTTPClient
	domain string
}

// NewJableSource 构造 Jable 源，domain 为空时默认 "jable.tv"。
func NewJableSource(hc *HTTPClient, domain string) *JableSource {
	if domain == "" {
		domain = "jable.tv"
	}
	return &JableSource{http: hc, domain: domain}
}

// jableSlug 与 MissAV 同规则：URL slug 用番号尾部数字补零到三位的小写形式
// （SSIS-41 → ssis-041），未补零会 404。
func jableSlug(code string) string {
	return strings.ToLower(padCodeDigits(code))
}

// Name 实现 Source。
func (s *JableSource) Name() string { return "jable" }

func (s *JableSource) base() string {
	if strings.HasPrefix(s.domain, "http://") || strings.HasPrefix(s.domain, "https://") {
		return strings.TrimRight(s.domain, "/")
	}
	return "https://" + s.domain
}

// Search 实现 Source：/search/videos?search_query=
func (s *JableSource) Search(ctx context.Context, q Query) ([]Video, error) {
	q = q.normalized()
	if strings.TrimSpace(q.Keyword) == "" {
		return nil, fmt.Errorf("jable search: empty keyword")
	}
	u := fmt.Sprintf("%s/search/videos?search_query=%s", s.base(), url.QueryEscape(q.Keyword))
	if q.Page > 1 {
		u = fmt.Sprintf("%s&page=%d", u, q.Page)
	}
	return s.list(ctx, u, q)
}

// Latest 实现 Source：首页视频列表。
func (s *JableSource) Latest(ctx context.Context, q Query) ([]Video, error) {
	q = q.normalized()
	u := s.base() + "/"
	if q.Page > 1 {
		u = fmt.Sprintf("%s?page=%d", u, q.Page)
	}
	return s.list(ctx, u, q)
}

func (s *JableSource) list(ctx context.Context, u string, q Query) ([]Video, error) {
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

// Detail 实现 Source：/videos/{slug}/
func (s *JableSource) Detail(ctx context.Context, code string) (*Video, error) {
	code = normalizeCode(code)
	pageURL := fmt.Sprintf("%s/videos/%s/", s.base(), jableSlug(code))
	body, err := s.http.GetWithRetry(ctx, pageURL, s.base()+"/", 2)
	if err != nil {
		return nil, err
	}
	html := string(body)
	v := &Video{Code: code, Source: s.Name(), DetailURL: pageURL}
	if t := ogTitle(html); t != "" {
		if c := ExtractCode(t); c != "" {
			v.Code = c
		}
		v.Title = stripCodeFromTitle(t, ExtractCode(t))
	}
	v.CoverURL = ogImage(html)
	doc, derr := goquery.NewDocumentFromReader(strings.NewReader(html))
	if derr == nil {
		v.Actresses = collectTexts(doc, "a[href*='/models/'], .video-info a[href*='model']")
		v.Tags = collectTexts(doc, "a[href*='/categories/']")
	}
	return v, nil
}

// Resolve 实现 Source：提取 hlsUrl → 流。
func (s *JableSource) Resolve(ctx context.Context, code string) ([]Stream, error) {
	code = normalizeCode(code)
	pageURL := fmt.Sprintf("%s/videos/%s/", s.base(), jableSlug(code))
	body, err := s.http.GetWithRetry(ctx, pageURL, s.base()+"/", 2)
	if err != nil {
		return nil, err
	}
	m := jableHlsRe.FindStringSubmatch(string(body))
	if m == nil {
		return nil, ErrNoStream
	}
	referer := s.base()
	streams, err := FetchStreams(ctx, s.http, m[1], referer)
	if err != nil {
		return nil, err
	}
	for i := range streams {
		streams[i].Source = s.Name()
	}
	return streams, nil
}

// Probe 实现 Prober：详情页可访问且内嵌 hlsUrl 即视为有“原片”（Jable 单版本）。
func (s *JableSource) Probe(ctx context.Context, code string) (*ProbeResult, error) {
	code = normalizeCode(code)
	pageURL := fmt.Sprintf("%s/videos/%s/", s.base(), jableSlug(code))
	body, err := s.http.GetWithRetry(ctx, pageURL, s.base()+"/", 2)
	if err != nil {
		return nil, err
	}
	if jableHlsRe.FindStringSubmatch(string(body)) == nil {
		return nil, fmt.Errorf("%w: jable variants for %s", ErrNotFound, code)
	}
	return &ProbeResult{
		Code: code, Source: s.Name(),
		Variants: []VariantInfo{{Kind: "normal", Label: "原片", Available: true}},
	}, nil
}

// ResolveVariant 实现 VariantResolver：单版本，仅支持 normal。
func (s *JableSource) ResolveVariant(ctx context.Context, code, variant string) ([]Stream, error) {
	if variant != "" && variant != "normal" {
		return nil, fmt.Errorf("%w: jable variant %s", ErrNotFound, variant)
	}
	return s.Resolve(ctx, code)
}

var _ Source = (*JableSource)(nil)
var _ Prober = (*JableSource)(nil)
var _ VariantResolver = (*JableSource)(nil)
