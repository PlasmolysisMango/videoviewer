package goserver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"videoviewer/pkg/aacg"
	"videoviewer/pkg/av"
)

// AACG 专栏（镜像自动发现 + 解析后的信息流，抓取实现见 pkg/aacg）。
// 镜像发现重且变化慢：结果懒解析后缓存 30 分钟；home/categories/search
// 抓取失败时立即失效缓存、重新发现并重试一次；feed/article/image 失败时把
// 旧响应里的镜像域名改写到当前镜像重试一次，避免域名轮换打断前端刷新。

const (
	aacgTargetTTL = 30 * time.Minute
	aacgTimeout   = 45 * time.Second

	// AACG 站点视频走随机轮换的 CDN 域名（主列表/分片/密钥各自不同），无法静态
	// 白名单；注册来源仅有文章详情响应与已放行 playlist 的内层 URI 两条，TTL 与
	// 容量有界，HLS 代理据此放行（见 handlers.go 的 hlsFetcher）。
	aacgMediaTTL     = 12 * time.Hour
	aacgMediaMaxSize = 512
)

// hostOf 返回绝对 URL 的主机名（无端口）；解析失败返回空串。
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// registerAacgMediaHosts 注册（或刷新）AACG 媒体主机，供 HLS 代理放行。
// 超容量时先清过期项、仍超则丢最旧；零值 Server 可直接使用。
func (s *Server) registerAacgMediaHosts(hosts ...string) {
	now := time.Now()
	s.aacgMediaMu.Lock()
	defer s.aacgMediaMu.Unlock()
	if s.aacgMediaHosts == nil {
		s.aacgMediaHosts = make(map[string]time.Time)
	}
	for _, host := range hosts {
		if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
			s.aacgMediaHosts[host] = now
		}
	}
	if len(s.aacgMediaHosts) <= aacgMediaMaxSize {
		return
	}
	for host, at := range s.aacgMediaHosts {
		if now.Sub(at) >= aacgMediaTTL {
			delete(s.aacgMediaHosts, host)
		}
	}
	for len(s.aacgMediaHosts) > aacgMediaMaxSize {
		oldestHost := ""
		var oldest time.Time
		for host, at := range s.aacgMediaHosts {
			if oldestHost == "" || at.Before(oldest) {
				oldestHost, oldest = host, at
			}
		}
		delete(s.aacgMediaHosts, oldestHost)
	}
}

func (s *Server) aacgMediaHostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	s.aacgMediaMu.Lock()
	defer s.aacgMediaMu.Unlock()
	at, ok := s.aacgMediaHosts[host]
	if !ok {
		return false
	}
	if time.Since(at) >= aacgMediaTTL {
		delete(s.aacgMediaHosts, host)
		return false
	}
	return true
}

// aacgUsableTarget 返回缓存中的可用镜像；缓存缺失或过期时重新发现。
// 发现过程持锁串行，并发请求共享同一次结果；发现失败或发现结果整体不可用时，
// 回退校验旧目标（会话内缓存 → 持久化列表，见 aacg_targets.go）续用，避免
// 发现链路抖动/阻断直接打断前端；新发现可用时同步写回持久化列表。
func (s *Server) aacgUsableTarget(ctx context.Context) (aacg.Target, error) {
	s.aacgMu.Lock()
	defer s.aacgMu.Unlock()
	if s.aacgTarget.URL != "" && time.Since(s.aacgAt) < aacgTargetTTL {
		return s.aacgTarget, nil
	}
	discovery, err := s.aacg.Discover(ctx)
	if err != nil {
		if target, ok := s.recheckKnownTargets(ctx); ok {
			return target, nil
		}
		return aacg.Target{}, err
	}
	for _, target := range discovery.Targets {
		health, checkErr := s.aacg.Check(ctx, target)
		if checkErr == nil && health.Usable {
			s.aacgKnown = discovery.Targets
			saveAacgTargets(discovery.Targets)
			s.aacgTarget = target
			s.aacgAt = time.Now()
			return target, nil
		}
	}
	if target, ok := s.recheckKnownTargets(ctx); ok {
		return target, nil
	}
	return aacg.Target{}, fmt.Errorf("aacg: no usable mirror found")
}

// recheckKnownTargets 依次校验会话内缓存与持久化的旧目标，返回首个仍为可识别
// 首页（Check 通过）的镜像并刷新目标缓存；全部不可用返回 false。去重避免同一
// URL 重复校验。调用方须持有 aacgMu。
func (s *Server) recheckKnownTargets(ctx context.Context) (aacg.Target, bool) {
	checked := make(map[string]bool)
	for _, target := range append([]aacg.Target{s.aacgTarget}, s.aacgKnown...) {
		if target.URL == "" || checked[target.URL] {
			continue
		}
		checked[target.URL] = true
		if health, err := s.aacg.Check(ctx, target); err == nil && health.Usable {
			s.aacgTarget = target
			s.aacgAt = time.Now()
			return target, true
		}
	}
	return aacg.Target{}, false
}

func (s *Server) aacgInvalidateTarget() {
	s.aacgMu.Lock()
	s.aacgTarget = aacg.Target{}
	s.aacgAt = time.Time{}
	s.aacgMu.Unlock()
}

// aacgWithFreshTarget 以当前可用镜像执行 op；失败即失效镜像缓存并重新发现，
// 按新镜像重试一次（镜像轮换/瞬断自愈）。重新发现失败时返回原始 op 错误
// （此时根因是镜像而非内容），重试仍失败则失效缓存并返回重试错误。
func aacgWithFreshTarget[T any](s *Server, ctx context.Context, op func(aacg.Target) (T, error)) (T, aacg.Target, error) {
	target, err := s.aacgUsableTarget(ctx)
	if err != nil {
		var zero T
		return zero, aacg.Target{}, err
	}
	result, err := op(target)
	if err == nil {
		return result, target, nil
	}
	originalErr := err
	s.aacgInvalidateTarget()
	fresh, freshErr := s.aacgUsableTarget(ctx)
	if freshErr != nil {
		var zero T
		return zero, aacg.Target{}, originalErr
	}
	result, retryErr := op(fresh)
	if retryErr != nil {
		s.aacgInvalidateTarget()
		var zero T
		return zero, aacg.Target{}, retryErr
	}
	return result, fresh, nil
}

// aacgRebaseTargetURL 取当前可用镜像，把 raw 的 scheme+host 重写到该镜像
// （保留 path/query），供旧响应中的镜像域名兜底重试；目标不可用、raw 无
// 主机名或与目标本就同源（无改写空间）时返回空串。
func (s *Server) aacgRebaseTargetURL(ctx context.Context, raw string) string {
	target, err := s.aacgUsableTarget(ctx)
	if err != nil {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	base, err := url.Parse(target.URL)
	if err != nil || base.Host == "" {
		return ""
	}
	if u.Host == base.Host {
		return ""
	}
	u.Scheme = base.Scheme
	u.Host = base.Host
	return u.String()
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
	list, target, err := aacgWithFreshTarget(s, ctx, func(t aacg.Target) (aacg.ArticleList, error) {
		return s.aacg.Feed(ctx, t.URL, aacgPage(r))
	})
	if err != nil {
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
	categories, target, err := aacgWithFreshTarget(s, ctx, func(t aacg.Target) ([]aacg.Category, error) {
		return s.aacg.Categories(ctx, t)
	})
	if err != nil {
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

// 分类流：URL 来自本服务此前返回的分类/推荐响应，直连抓取；失败时把旧
// 响应里的镜像域名改写到当前镜像重试一次。
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
		if retryURL := s.aacgRebaseTargetURL(ctx, feedURL); retryURL != "" {
			list, err = s.aacg.Feed(ctx, retryURL, aacgPage(r))
		}
	}
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
	list, _, err := aacgWithFreshTarget(s, ctx, func(t aacg.Target) (aacg.ArticleList, error) {
		return s.aacg.Search(ctx, t.URL, q, aacgPage(r))
	})
	if err != nil {
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
		if retryURL := s.aacgRebaseTargetURL(ctx, imageURL); retryURL != "" {
			data, contentType, err = s.aacg.Image(ctx, retryURL)
		}
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(data)
}

// 文章详情：URL 来自列表响应，直连抓取（视频 URL 可能含一次性 auth_key，仅返回给本机前端）；
// 失败时把旧响应里的镜像域名改写到当前镜像重试一次。
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
		if retryURL := s.aacgRebaseTargetURL(ctx, articleURL); retryURL != "" {
			detail, err = s.aacg.Article(ctx, retryURL)
		}
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	// 先注册再响应：前端随后对 probe/播放的请求即可放行这些 CDN 主机。
	s.registerAacgMediaHosts(aacgVideoHosts(detail)...)
	writeJSON(w, http.StatusOK, aacgArticleJSON(detail))
}

// aacgVideoHosts 收集文章每条视频（首选 + 候选）的主机，供 HLS 代理放行。
func aacgVideoHosts(detail aacg.ArticleDetail) []string {
	hosts := []string{}
	for _, video := range detail.Videos {
		hosts = append(hosts, hostOf(video.URL))
		for _, source := range video.Sources {
			hosts = append(hosts, hostOf(source))
		}
	}
	return hosts
}

// aacgProbeTimeout / aacgDeepTimeout 分别限时探测的播放列表阶段与分片级
// 深度阶段（key/初始化段/首分片均为轻量请求）。深度失败在真实阻断场景下
// 约 5-6 秒显现（TLS 中断），6s 上限与之吻合。
const (
	aacgProbeTimeout = 8 * time.Second
	aacgDeepTimeout  = 6 * time.Second
)

// handleAacgProbe 播放前的动态选源：并行对全部候选做两阶段探测——播放列表
// （主机已注册 + HTTP 200 + 正文以 #EXTM3U 开头）与分片级深度验证（key/
// 初始化段/首分片可取），判定与播放完全同栈。恒 200 返回
// {"url": 首选（兼容保留）, "urls": [全部播放列表级可用，深度通过者在前],
// "playable": [深度验证通过子集]}；全部不可用时 url 为空串、数组为空。
// urls[0] 即「不经代理即可真正播放」的首选源，仅播放列表可用的候选殿后作
// 备用。等齐全部候选再返回：失败语义需全体判定，成功场景各候选亚秒级；
// 并行使最坏耗时从 N×(8s+6s) 收敛到 ~8s。只回判定结果，不代理内容，也不
// 打印含 auth_key 的 URL。
func (s *Server) handleAacgProbe(w http.ResponseWriter, r *http.Request) {
	if !s.requireAacg(w) {
		return
	}
	candidates := r.URL.Query()["url"]
	if len(candidates) == 0 || len(candidates) > 4 {
		writeError(w, http.StatusBadRequest, "1-4 url parameters required")
		return
	}
	type aacgVerdict struct{ playlist, deep bool }
	verdicts := make([]aacgVerdict, len(candidates))
	var wg sync.WaitGroup
	for i, raw := range candidates {
		wg.Add(1)
		go func(i int, raw string) {
			defer wg.Done()
			p, d := s.aacgProbeDeep(r.Context(), raw)
			verdicts[i] = aacgVerdict{playlist: p, deep: d}
		}(i, raw)
	}
	wg.Wait()

	urls := []string{}
	playable := []string{}
	for i, v := range verdicts {
		if v.deep {
			playable = append(playable, candidates[i])
		}
	}
	urls = append(urls, playable...)
	for i, v := range verdicts {
		if v.playlist && !v.deep {
			urls = append(urls, candidates[i])
		}
	}
	first := ""
	if len(urls) > 0 {
		first = urls[0]
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": first, "urls": urls, "playable": playable})
}

// aacgFetchPlaylist 拉取并校验一个候选播放列表：URL/主机合法、HTTP 200、正文
// 以 #EXTM3U 开头；返回完整正文（播放列表很小，读取上限 1MB）。限时
// aacgProbeTimeout，单次尝试。
func (s *Server) aacgFetchPlaylist(ctx context.Context, raw string) ([]byte, bool) {
	pu, err := url.Parse(raw)
	if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || !s.aacgMediaHostAllowed(pu.Hostname()) {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, aacgProbeTimeout)
	defer cancel()
	resp, err := s.aacg.Fetch(ctx, raw)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, false
	}
	if !bytes.HasPrefix(body, []byte("#EXTM3U")) {
		return nil, false
	}
	return body, true
}

// aacgProbeSource 播放列表级探测（实网诊断与兼容保留）。
func (s *Server) aacgProbeSource(ctx context.Context, raw string) bool {
	_, ok := s.aacgFetchPlaylist(ctx, raw)
	return ok
}

// aacgProbeDeep 对单个候选做两阶段深度验证：播放列表可取后，进一步确认
// key（若有）/初始化段（若有）/首分片在当前网络真正可取——这些资源常与
// 播放列表分属不同 CDN 域名，播放列表可达不代表能播放。主列表下钻一层取
// 最高码率变体；变体仍为 master、无可解析分片、任一目标失败/超时均
// deepOK=false（仅播放列表可用）。网络错误不重试：确定性的阻断（如 TLS
// 被重置）重试只会放大等待；上游对 Range 返回 3xx 按不可用判，与播放代理
// 链路（传输层不跟随重定向）行为一致。
func (s *Server) aacgProbeDeep(ctx context.Context, raw string) (playlistOK, deepOK bool) {
	// 播放列表阶段整体限时 aacgProbeTimeout（master 下钻的两跳共享预算）。
	listCtx, cancelList := context.WithTimeout(ctx, aacgProbeTimeout)
	defer cancelList()
	body, ok := s.aacgFetchPlaylist(listCtx, raw)
	if !ok {
		return false, false
	}
	base := raw
	if variants := av.ParseMasterPlaylist(string(body), raw); variants != nil {
		best := variants[len(variants)-1] // 已按带宽升序，末位即最高
		body, ok = s.aacgFetchPlaylist(listCtx, best.URL)
		if !ok || av.ParseMasterPlaylist(string(body), best.URL) != nil {
			return true, false
		}
		base = best.URL
	}
	targets, ok := av.FirstMediaTargets(string(body), base)
	if !ok {
		return true, false
	}

	type probeCheck struct {
		uri string
		rng string // 空串表示不带 Range（key 是几十字节的小文件）
	}
	var checks []probeCheck
	if targets.KeyURI != "" {
		checks = append(checks, probeCheck{uri: targets.KeyURI})
	}
	if targets.MapURI != "" {
		checks = append(checks, probeCheck{uri: targets.MapURI, rng: "bytes=0-1023"})
	}
	segRange := targets.SegRange
	if segRange == "" {
		segRange = "bytes=0-1023"
	}
	checks = append(checks, probeCheck{uri: targets.SegURI, rng: segRange})

	deepCtx, cancel := context.WithTimeout(ctx, aacgDeepTimeout)
	defer cancel()
	results := make([]bool, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Add(1)
		go func(i int, c probeCheck) {
			defer wg.Done()
			results[i] = s.aacgProbeResource(deepCtx, c.uri, c.rng)
		}(i, c)
	}
	wg.Wait()
	for _, ok := range results {
		if !ok {
			return true, false
		}
	}
	return true, true
}

// aacgProbeResource 轻量验证单个媒体资源可取：200/206 且能读到 ≥1 字节；
// 只读前 4KB 即关闭，探测不整段下载。
func (s *Server) aacgProbeResource(ctx context.Context, raw, rangeHdr string) bool {
	resp, err := s.aacg.FetchRange(ctx, raw, rangeHdr)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return false
	}
	n, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return err == nil && n > 0
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
	parts := make([]map[string]any, 0, len(detail.ContentParts))
	for _, p := range detail.ContentParts {
		parts = append(parts, map[string]any{
			"text":      p.Text,
			"url":       p.URL,
			"image_url": p.ImageURL,
		})
	}
	videos := make([]map[string]any, 0, len(detail.Videos))
	for _, v := range detail.Videos {
		sources := v.Sources
		if sources == nil {
			sources = []string{}
		}
		videos = append(videos, map[string]any{
			"url":        v.URL,
			"type":       v.Type,
			"poster_url": v.PosterURL,
			"sources":    sources,
		})
	}
	payload := aacgItemJSON(detail.ArticleSummary)
	payload["content"] = detail.Content
	payload["content_parts"] = parts
	payload["videos"] = videos
	payload["prev"] = aacgArticleLinkJSON(detail.Previous)
	payload["next"] = aacgArticleLinkJSON(detail.Next)
	return payload
}

// aacgArticleLinkJSON renders footer navigation; nil (no such link) stays JSON null.
func aacgArticleLinkJSON(link *aacg.ArticleLink) any {
	if link == nil {
		return nil
	}
	return map[string]any{"title": link.Title, "url": link.URL}
}
