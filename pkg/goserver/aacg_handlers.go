package goserver

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"videoviewer/pkg/aacg"
)

// AACG 专栏（镜像自动发现 + 解析后的信息流，抓取实现见 pkg/aacg）。
// 镜像发现重且变化慢：结果懒解析后缓存 30 分钟；home/categories/search
// 抓取失败时失效缓存，由下一次请求重新发现。

const (
	aacgTargetTTL = 30 * time.Minute
	aacgTimeout   = 45 * time.Second
)

// aacgUsableTarget 返回缓存中的可用镜像；缓存缺失或过期时重新发现。
// 发现过程持锁串行，并发请求共享同一次结果。
func (s *Server) aacgUsableTarget(ctx context.Context) (aacg.Target, error) {
	s.aacgMu.Lock()
	defer s.aacgMu.Unlock()
	if s.aacgTarget.URL != "" && time.Since(s.aacgAt) < aacgTargetTTL {
		return s.aacgTarget, nil
	}
	discovery, err := s.aacg.Discover(ctx)
	if err != nil {
		return aacg.Target{}, err
	}
	for _, target := range discovery.Targets {
		health, err := s.aacg.Check(ctx, target)
		if err == nil && health.Usable {
			s.aacgTarget = target
			s.aacgAt = time.Now()
			return target, nil
		}
	}
	return aacg.Target{}, fmt.Errorf("aacg: no usable mirror found")
}

func (s *Server) aacgInvalidateTarget() {
	s.aacgMu.Lock()
	s.aacgTarget = aacg.Target{}
	s.aacgAt = time.Time{}
	s.aacgMu.Unlock()
}

// requireAacg 在 AACG 客户端不可用（构造失败）时写出 503 并返回 false。
func (s *Server) requireAacg(w http.ResponseWriter) bool {
	if s.aacg == nil {
		writeError(w, http.StatusServiceUnavailable, "aacg client unavailable")
		return false
	}
	return true
}

func aacgPage(r *http.Request) int {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		return 1
	}
	return page
}

// 首页推荐流：使用当前发现的镜像，后续翻页由客户端传 page 递增。
func (s *Server) handleAacgHome(w http.ResponseWriter, r *http.Request) {
	if !s.requireAacg(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), aacgTimeout)
	defer cancel()
	target, err := s.aacgUsableTarget(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	list, err := s.aacg.Feed(ctx, target.URL, aacgPage(r))
	if err != nil {
		s.aacgInvalidateTarget()
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	payload := aacgListJSON(list)
	payload["site"] = target.URL
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleAacgCategories(w http.ResponseWriter, r *http.Request) {
	if !s.requireAacg(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), aacgTimeout)
	defer cancel()
	target, err := s.aacgUsableTarget(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	categories, err := s.aacg.Categories(ctx, target)
	if err != nil {
		s.aacgInvalidateTarget()
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(categories))
	for _, c := range categories {
		items = append(items, map[string]any{"name": c.Name, "url": c.URL})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"site":       target.URL,
		"categories": items,
	})
}

// 分类流：URL 来自本服务此前返回的分类/推荐响应，直连抓取，不触镜像缓存。
func (s *Server) handleAacgFeed(w http.ResponseWriter, r *http.Request) {
	if !s.requireAacg(w) {
		return
	}
	feedURL := r.URL.Query().Get("url")
	if feedURL == "" {
		writeError(w, http.StatusBadRequest, "url parameter required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), aacgTimeout)
	defer cancel()
	list, err := s.aacg.Feed(ctx, feedURL, aacgPage(r))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, aacgListJSON(list))
}

func (s *Server) handleAacgSearch(w http.ResponseWriter, r *http.Request) {
	if !s.requireAacg(w) {
		return
	}
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "q parameter required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), aacgTimeout)
	defer cancel()
	target, err := s.aacgUsableTarget(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	list, err := s.aacg.Search(ctx, target.URL, q, aacgPage(r))
	if err != nil {
		s.aacgInvalidateTarget()
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, aacgListJSON(list))
}

// 图片代理：封面/缩略图是站点 AES 混淆密文，客户端无法直接解码，
// 由 pkg/aacg 解密后转发；URL 来自本服务此前返回的列表/详情响应。
func (s *Server) handleAacgImage(w http.ResponseWriter, r *http.Request) {
	if !s.requireAacg(w) {
		return
	}
	imageURL := r.URL.Query().Get("url")
	if imageURL == "" {
		writeError(w, http.StatusBadRequest, "url parameter required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), aacgTimeout)
	defer cancel()
	data, contentType, err := s.aacg.Image(ctx, imageURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(data)
}

// 文章详情：URL 来自列表响应，直连抓取（视频 URL 可能含一次性 auth_key，仅返回给本机前端）。
func (s *Server) handleAacgArticle(w http.ResponseWriter, r *http.Request) {
	if !s.requireAacg(w) {
		return
	}
	articleURL := r.URL.Query().Get("url")
	if articleURL == "" {
		writeError(w, http.StatusBadRequest, "url parameter required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), aacgTimeout)
	defer cancel()
	detail, err := s.aacg.Article(ctx, articleURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, aacgArticleJSON(detail))
}

func aacgItemJSON(item aacg.ArticleSummary) map[string]any {
	return map[string]any{
		"title":        item.Title,
		"url":          item.URL,
		"summary":      item.Summary,
		"cover_url":    item.CoverURL,
		"published_at": item.PublishedAt,
	}
}

func aacgListJSON(list aacg.ArticleList) map[string]any {
	items := make([]map[string]any, 0, len(list.Items))
	for _, item := range list.Items {
		items = append(items, aacgItemJSON(item))
	}
	return map[string]any{
		"title":   list.Title,
		"items":   items,
		"page":    list.Pagination.Current,
		"maxPage": list.Pagination.Total,
		"hasNext": list.Pagination.NextURL != "",
		"hasPrev": list.Pagination.PreviousURL != "",
	}
}

func aacgArticleJSON(detail aacg.ArticleDetail) map[string]any {
	videos := make([]map[string]any, 0, len(detail.Videos))
	for _, v := range detail.Videos {
		videos = append(videos, map[string]any{
			"url":        v.URL,
			"type":       v.Type,
			"poster_url": v.PosterURL,
		})
	}
	payload := aacgItemJSON(detail.ArticleSummary)
	payload["content"] = detail.Content
	payload["videos"] = videos
	return payload
}
