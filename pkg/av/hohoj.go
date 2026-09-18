package av

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

var (
	hohojVideoSrcRe = regexp.MustCompile(`var\s+videoSrc\s*=\s*"([^"]+)"`)
	// 搜索结果卡片：/video?id=N 与紧随其后的 img alt（含番号标题），
	// 限窗避免跨越相邻卡片。
	hohojCardRe = regexp.MustCompile(`(?s)href="/video\?id=(\d+)".{0,400}?alt="([^"]*)"`)
)

// HohoJSource 是 HohoJ 数据源实现。
// 该站点主要通过搜索番号 → 内嵌页 → videoSrc 获取直连 m3u8；列表类能力有限。
type HohoJSource struct {
	http   *HTTPClient
	domain string
}

// NewHohoJSource 构造 HohoJ 源，domain 为空时默认 "hohoj.tv"。
func NewHohoJSource(hc *HTTPClient, domain string) *HohoJSource {
	if domain == "" {
		domain = "hohoj.tv"
	}
	return &HohoJSource{http: hc, domain: domain}
}

// Name 实现 Source。
func (s *HohoJSource) Name() string { return "hohoj" }

func (s *HohoJSource) base() string { return "https://" + s.domain }

// Search 不被该数据源支持。
func (s *HohoJSource) Search(context.Context, Query) ([]Video, error) {
	return nil, fmt.Errorf("%w: hohoj search", ErrNotImplemented)
}

// Latest 不被该数据源支持。
func (s *HohoJSource) Latest(context.Context, Query) ([]Video, error) {
	return nil, fmt.Errorf("%w: hohoj latest", ErrNotImplemented)
}

// findID 搜索番号并返回精确匹配卡片的视频 id。
// hohoj 搜索索引是未补零形式（实测搜 ssis-041 返回空页，搜 ssis-41
// 命中 SSIS-411~419 等同前缀系列），因此先搜未补零小写番号，
// 无精确命中再试补零形式；搜索是模糊匹配（混入同前缀其它番号），
// 必须解析卡片 alt 中的番号精确比对（sameCode 忽略尾部前导零），
// 否则会解析到别的影片（实测 SSIS-41 曾命中首条 SSIS-414）。
func (s *HohoJSource) findID(ctx context.Context, code string) (string, error) {
	code = normalizeCode(code)
	padded := strings.ToLower(padCodeDigits(code))
	queries := []string{strings.ToLower(code)}
	if padded != queries[0] {
		queries = append(queries, padded)
	}
	var lastErr error
	for _, q := range queries {
		searchURL := fmt.Sprintf("%s/search?text=%s", s.base(), q)
		body, err := s.http.GetWithRetry(ctx, searchURL, s.base()+"/", 2)
		if err != nil {
			return "", err
		}
		for _, m := range hohojCardRe.FindAllStringSubmatch(string(body), -1) {
			if c := ExtractCode(m[2]); c != "" && sameCode(c, code) {
				return m[1], nil
			}
		}
		lastErr = fmt.Errorf("%w: hohoj id for %s", ErrNotFound, code)
	}
	return "", lastErr
}

// Detail 返回基于番号的基础元数据（该站点列表页信息有限）。
func (s *HohoJSource) Detail(ctx context.Context, code string) (*Video, error) {
	code = normalizeCode(code)
	id, err := s.findID(ctx, code)
	if err != nil {
		return nil, err
	}
	return &Video{
		Code:      code,
		Source:    s.Name(),
		DetailURL: fmt.Sprintf("%s/video?id=%s", s.base(), id),
	}, nil
}

// Resolve 实现 Source：search → embed → videoSrc。参考 NASSAV hohoJDownloader。
func (s *HohoJSource) Resolve(ctx context.Context, code string) ([]Stream, error) {
	code = normalizeCode(code)
	id, err := s.findID(ctx, code)
	if err != nil {
		return nil, err
	}
	embedURL := fmt.Sprintf("%s/embed?id=%s", s.base(), id)
	referer := fmt.Sprintf("%s/video?id=%s", s.base(), id)
	body, err := s.http.GetWithRetry(ctx, embedURL, referer, 2)
	if err != nil {
		return nil, err
	}
	m := hohojVideoSrcRe.FindStringSubmatch(string(body))
	if m == nil {
		return nil, ErrNoStream
	}
	streams, err := FetchStreams(ctx, s.http, m[1], referer)
	if err != nil {
		return nil, err
	}
	for i := range streams {
		streams[i].Source = s.Name()
	}
	return streams, nil
}

// Probe 实现 Prober：搜索命中精确番号即视为有“原片”（该站单版本）。
func (s *HohoJSource) Probe(ctx context.Context, code string) (*ProbeResult, error) {
	code = normalizeCode(code)
	if _, err := s.findID(ctx, code); err != nil {
		return nil, err
	}
	return &ProbeResult{
		Code: code, Source: s.Name(),
		Variants: []VariantInfo{{Kind: "normal", Label: "原片", Available: true}},
	}, nil
}

// ResolveVariant 实现 VariantResolver：单版本，仅支持 normal。
func (s *HohoJSource) ResolveVariant(ctx context.Context, code, variant string) ([]Stream, error) {
	if variant != "" && variant != "normal" {
		return nil, fmt.Errorf("%w: hohoj variant %s", ErrNotFound, variant)
	}
	return s.Resolve(ctx, code)
}

var _ Source = (*HohoJSource)(nil)
var _ Prober = (*HohoJSource)(nil)
var _ VariantResolver = (*HohoJSource)(nil)
