package av

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

var (
	hohojVideoSrcRe = regexp.MustCompile(`var\s+videoSrc\s*=\s*"([^"]+)"`)
	hohojIDRe       = regexp.MustCompile(`[?&]id=(\d+)`)
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

// findID 搜索番号并返回首个视频 id。
func (s *HohoJSource) findID(ctx context.Context, code string) (string, error) {
	searchURL := fmt.Sprintf("%s/search?text=%s", s.base(), strings.ToLower(code))
	body, err := s.http.GetWithRetry(ctx, searchURL, s.base()+"/", 2)
	if err != nil {
		return "", err
	}
	m := hohojIDRe.FindStringSubmatch(string(body))
	if m == nil {
		return "", fmt.Errorf("%w: hohoj id for %s", ErrNotFound, code)
	}
	return m[1], nil
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

var _ Source = (*HohoJSource)(nil)
