package goserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"videoviewer/pkg/av"
	"videoviewer/pkg/javdb"
)

// API handlers

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password required")
		return
	}

	cred := javdb.Credentials{Username: req.Username, Password: req.Password}
	if err := s.javdb.Login(r.Context(), cred); err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	_, token := s.javdb.Session()
	writeJSON(w, http.StatusOK, map[string]string{
		"token":    token,
		"username": req.Username,
	})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "q parameter required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 20
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}

	// scope=actor searches actor profiles by name/alias, so the frontend can
	// offer direct entry into an actor page instead of keyword results.
	if r.URL.Query().Get("scope") == "actor" {
		actors, err := s.javdb.SearchActors(r.Context(), javdb.Query{
			Keyword: q,
			Page:    javdb.Page{Page: page, Limit: limit},
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"actors": actors, "page": page})
		return
	}

	results, err := s.javdb.SearchMovies(r.Context(), q, javdb.WithPage(page, limit))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// The app-API backend attaches matched actors to movie results; the web
	// backend leaves the slice empty. Pass through either way.
	writeJSON(w, http.StatusOK, map[string]any{
		"movies":  results.Movies,
		"actors":  results.Actors,
		"page":    results.Current,
		"maxPage": results.MaxPage,
	})
}

// handleActorMovies serves the works list of one actor: /api/actor-movies/{id}.
func (s *Server) handleActorMovies(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 || parts[3] == "" {
		writeError(w, http.StatusBadRequest, "actor ID required")
		return
	}
	id := parts[3]
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	results, err := s.javdb.ActorMovies(r.Context(), id, javdb.Page{Page: page, Limit: limit})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"movies":  results.Movies,
		"page":    results.Current,
		"maxPage": results.MaxPage,
	})
}

func (s *Server) handleMovie(w http.ResponseWriter, r *http.Request) {
	// Extract ID from path: /api/movie/{id}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "movie ID required")
		return
	}
	id := parts[3]

	movie, err := s.javdb.Movie(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	// Also fetch magnets
	magnets, err := s.javdb.BestMagnet(r.Context(), id)
	if err != nil {
		// Non-fatal, movie detail still useful
		magnets = nil
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"movie":   movie,
		"magnets": magnets,
	})
}

func (s *Server) handleRanking(w http.ResponseWriter, r *http.Request) {
	// Extract kind from path: /api/ranking/{kind}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "ranking kind required")
		return
	}
	kind := javdb.RankingKind(parts[3])

	category := javdb.Category(r.URL.Query().Get("category"))
	period := javdb.Period(r.URL.Query().Get("period"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 20
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}

	q := javdb.RankingQuery{
		Kind:     kind,
		Category: category,
		Period:   period,
		Page:     javdb.Page{Page: page, Limit: limit},
	}

	ranking, err := s.javdb.Ranking(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"movies":  ranking.Movies,
		"actors":  ranking.Actors,
		"page":    ranking.Page,
		"maxPage": ranking.MaxPage,
	})
}

func (s *Server) handleMagnets(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "movie ID required")
		return
	}
	id := parts[3]

	magnets, err := s.javdb.BestMagnet(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"magnets": magnets,
	})
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	tags, err := s.javdb.TagGroups(r.Context(), javdb.Category(category))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tags": tags,
	})
}

func (s *Server) handleActor(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "actor ID required")
		return
	}
	id := parts[3]

	actor, err := s.javdb.Actor(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"actor": actor,
	})
}

// AV handlers (MissAV/Jable/HohoJ for playback and download)

func (s *Server) handleAVSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "q parameter required")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 20
	}
	source := r.URL.Query().Get("source")

	videos, err := s.av.Search(r.Context(), av.Query{
		Keyword: q,
		Page:    page,
		Limit:   limit,
		Source:  source,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"videos": videos,
		"page":   page,
	})
}

func (s *Server) handleAVResolve(w http.ResponseWriter, r *http.Request) {
	// /api/av/resolve/{code} → 切分后 code 在第 5 段（前 4 段为空/api/av/resolve）
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		writeError(w, http.StatusBadRequest, "video code required")
		return
	}
	code := parts[4]
	source := r.URL.Query().Get("source")

	streams, err := s.av.Resolve(r.Context(), code, source)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"streams": streams,
	})
}

func (s *Server) handleAVPlay(w http.ResponseWriter, r *http.Request) {
	// /api/av/play/{code} → 切分后 code 在第 5 段
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		writeError(w, http.StatusBadRequest, "video code required")
		return
	}
	code := parts[4]
	source := r.URL.Query().Get("source")

	stream, err := s.av.Play(r.Context(), code, av.DownloadOptions{Source: source})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"stream": stream,
	})
}

func (s *Server) handleAVDownload(w http.ResponseWriter, r *http.Request) {
	// /api/av/download/{code} → 切分后 code 在第 5 段
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		writeError(w, http.StatusBadRequest, "video code required")
		return
	}
	code := parts[4]
	source := r.URL.Query().Get("source")

	if s.cfg.DownloadDir == "" {
		writeError(w, http.StatusBadRequest, "download directory not configured")
		return
	}

	opt := av.DownloadOptions{
		Source:      source,
		RemuxToMP4:  true,
		Concurrency: 8,
	}

	result, err := s.av.Download(r.Context(), code, s.cfg.DownloadDir, opt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleAVDetail(w http.ResponseWriter, r *http.Request) {
	// /api/av/detail/{code} → 切分后 code 在第 5 段
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		writeError(w, http.StatusBadRequest, "video code required")
		return
	}
	code := parts[4]
	source := r.URL.Query().Get("source")

	video, err := s.av.Detail(r.Context(), code, source)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"video": video,
	})
}

// imageHostSuffixes 图片代理白名单：仅转发可信的图片/媒体 CDN，防止被当作开放代理滥用。
var imageHostSuffixes = []string{
	"jdbstatic.com",                        // JavDB 图片 CDN
	"spfcas.com",                           // JavDB 镜像站图片 CDN (tp.spfcas.com)
	"surrit.com",                           // MissAV 图片/视频 CDN
	"missav.ai", "missav.ws", "missav.com", // MissAV 站点
	"jable.tv",              // Jable 站点及图片
	"hohoj.tv", "ggjav.com", // HohoJ 站点及视频流
}

func imageHostAllowed(host string) bool {
	host = strings.ToLower(host)
	for _, suffix := range imageHostSuffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

// handleImage 把远程图片经后端转发给前端。Flutter Web 用 canvas 绘制网络图片，
// 需要读取像素数据，因此要求图片响应带 CORS 头；第三方 CDN 不提供，
// 故改由同源的后端代理转发（后端响应自带 CORS 中间件的头）。
func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	rawURL := r.URL.Query().Get("url")
	if rawURL == "" {
		writeError(w, http.StatusBadRequest, "url parameter required")
		return
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		writeError(w, http.StatusBadRequest, "invalid url")
		return
	}
	if !imageHostAllowed(u.Hostname()) {
		writeError(w, http.StatusForbidden, "host not allowed")
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, rawURL, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36")

	resp, err := s.imgClient.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "fetch image: "+err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		writeError(w, resp.StatusCode, "upstream status "+resp.Status)
		return
	}

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	// 防御性限制单张图片体积
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 20<<20))
}

// hlsURIRe 匹配 playlist 标签中的 URI="..."（EXT-X-KEY / EXT-X-MAP 等）。
var hlsURIRe = regexp.MustCompile(`URI="([^"]+)"`)

// resolveAgainst 把 playlist 内的相对 URI 解析为绝对 URL（基于当前 playlist 的 URL）。
func resolveAgainst(base, ref string) string {
	ru, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	bu, err := url.Parse(base)
	if err != nil {
		return ref
	}
	return bu.ResolveReference(ru).String()
}

// proxyHlsURL 把上游媒体 URL 包装成本代理的 URL：
// 子播放列表指向 /api/hls/playlist（需再次改写），媒体分片指向 /api/hls/segment。
func proxyHlsURL(r *http.Request, mediaURL, referer string) string {
	endpoint := "segment"
	if strings.HasSuffix(strings.ToLower(mediaURL), ".m3u8") {
		endpoint = "playlist"
	}
	q := url.Values{}
	q.Set("u", mediaURL)
	if referer != "" {
		q.Set("ref", referer)
	}
	return fmt.Sprintf("http://%s/api/hls/%s?%s", r.Host, endpoint, q.Encode())
}

// fetchMedia 带浏览器 UA 与 Referer/Origin 请求上游媒体资源（经配置的代理）。
// surrit 等 CDN 强制校验 Referer，而浏览器无法伪造该头，因此必须由后端转发。
func (s *Server) fetchMedia(ctx context.Context, u, referer string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	if referer != "" {
		req.Header.Set("Referer", referer)
		if ru, err := url.Parse(referer); err == nil && ru.Host != "" {
			req.Header.Set("Origin", ru.Scheme+"://"+ru.Host)
		}
	}
	return s.imgClient.Do(req)
}

// handleHlsPlaylist 转发并改写 HLS 播放列表：把其中的分片/子列表/密钥 URI
// 重写为本代理的 URL，浏览器（hls.js）即可同源加载被 Referer 防盗链保护的流。
func (s *Server) handleHlsPlaylist(w http.ResponseWriter, r *http.Request) {
	u := r.URL.Query().Get("u")
	referer := r.URL.Query().Get("ref")
	pu, err := url.Parse(u)
	if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || !imageHostAllowed(pu.Hostname()) {
		writeError(w, http.StatusBadRequest, "invalid media url")
		return
	}

	resp, err := s.fetchMedia(r.Context(), u, referer)
	if err != nil {
		writeError(w, http.StatusBadGateway, "fetch playlist: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		writeError(w, resp.StatusCode, "upstream status "+resp.Status)
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		writeError(w, http.StatusBadGateway, "read playlist: "+err.Error())
		return
	}

	lines := strings.Split(string(body), "\n")
	for i, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			if strings.Contains(trimmed, "URI=\"") {
				lines[i] = hlsURIRe.ReplaceAllStringFunc(trimmed, func(m string) string {
					inner := m[5 : len(m)-1]
					return `URI="` + proxyHlsURL(r, resolveAgainst(u, inner), referer) + `"`
				})
			}
			continue
		}
		lines[i] = proxyHlsURL(r, resolveAgainst(u, trimmed), referer)
	}

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(strings.Join(lines, "\n")))
}

// handleHlsSegment 流式转发媒体分片（.ts / init 段 / 子资源），带上游 Referer。
func (s *Server) handleHlsSegment(w http.ResponseWriter, r *http.Request) {
	u := r.URL.Query().Get("u")
	referer := r.URL.Query().Get("ref")
	pu, err := url.Parse(u)
	if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || !imageHostAllowed(pu.Hostname()) {
		writeError(w, http.StatusBadRequest, "invalid media url")
		return
	}

	resp, err := s.fetchMedia(r.Context(), u, referer)
	if err != nil {
		writeError(w, http.StatusBadGateway, "fetch segment: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		writeError(w, resp.StatusCode, "upstream status "+resp.Status)
		return
	}

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// health check endpoint
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"time":    time.Now().Format(time.RFC3339),
		"version": "1.0.0",
	})
}
