package goserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
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

	cookie, token := s.javdb.Session()
	// 持久化会话与凭据：重启不丢登录，JWT 失效时自动重登。
	saveSession(cookie, token, req.Username, req.Password)
	s.mu.Lock()
	s.username, s.password = req.Username, req.Password
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{
		"token":    token,
		"username": req.Username,
	})
}

// handleLogout forgets the JavDB session locally (backend + session.json).
// Note: JavDB has no app-API logout endpoint, so the server-side session is
// simply dropped.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.javdb.ClearSession()
	clearSession()
	s.mu.Lock()
	s.username, s.password = "", ""
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
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
	sort := javdb.SortBy(r.URL.Query().Get("sort"))

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
		// app 后端忽略 limit 固定返回整页，这里自行截断
		if len(actors) > limit {
			actors = actors[:limit]
		}
		writeJSON(w, http.StatusOK, map[string]any{"actors": actors, "page": page})
		return
	}

	opts := []javdb.QueryOption{javdb.WithPage(page, limit)}
	if sort != "" {
		opts = append(opts, javdb.WithSort(sort))
	}
	results, err := s.javdb.SearchMovies(r.Context(), q, opts...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 当搜索关键词精确匹配某部影片的番号时，从详情页拉取演员信息填入搜索结果。
	// JavDB 搜索 API 不返回影片演员，只有详情页才有；番号搜索时用户期望看到正确的演员。
	var enrichedActors []javdb.Actor
	if len(results.Movies) > 0 {
		want := javdb.NormalizeCode(q)
		for i := range results.Movies {
			if results.Movies[i].Code == want {
				if detail, derr := s.javdb.Movie(r.Context(), results.Movies[i].ID); derr == nil && detail != nil {
					for _, ac := range detail.ActorCredits {
						if ac.Name != "" {
							enrichedActors = append(enrichedActors, javdb.Actor{
								ID:   ac.ID,
								Name: ac.Name,
								Href: ac.Href,
							})
						}
					}
					// 把演员名也填入影片的 Actors 字段
					results.Movies[i].Actors = nil
					for _, ac := range detail.ActorCredits {
						if ac.Name != "" {
							results.Movies[i].Actors = append(results.Movies[i].Actors, ac.Name)
						}
					}
				}
				break
			}
		}
	}

	// The app-API backend attaches matched actors to movie results; the web
	// backend leaves the slice empty. When we enriched actors from detail,
	// prefer those; otherwise pass through the original actor results.
	actors := results.Actors
	if len(enrichedActors) > 0 {
		actors = enrichedActors
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"movies":  results.Movies,
		"actors":  actors,
		"page":    results.Current,
		"maxPage": results.MaxPage,
	})
}

// handleActorMovies serves the works list of one actor: /api/actor-movies/{id}.
// 数据走 app API 的实体类别端点（Client.ActorMovies 内部优先，匿名可用）。
// mode 参数：空=全部；"solo"=单体作品（演员页 filter_tags 的 main flag s，
// 服务端过滤）；"costar"=共演作品（API 无对应 flag，聚合全部与单体两侧
// 全部页后做集合差，保持“全部”的原排序；上限各 5 页）。
// sort 参数：优先透传上游 sort_by（app API 服务端排序，实测支持
// release±/score/hit，无评论/最低分取值）；上游已排序的方式跳过客户端
// 重排（列表行无评分数据，客户端重排只会打乱上游结果）；其余方式
// （most_magnets 行内有数据）继续客户端排序。
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
	mode := r.URL.Query().Get("mode")
	sort := javdb.SortBy(r.URL.Query().Get("sort"))
	ctx := r.Context()

	var movies []javdb.Movie
	maxPage := 0
	switch mode {
	case "costar":
		// 共演 = 全部 − 单体。两路服务端排序不同（实测），按页差集会错位，
		// 因此聚合两侧的全部页后做集合差。
		const maxAggPages = 5
		all, err := s.javdb.CategoryMovies(ctx, javdb.CategoryQuery{
			ActorID: id, Page: javdb.Page{Page: 1, Limit: limit},
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		allMovies := all.Movies
		for p := 2; p <= min(all.MaxPage, maxAggPages); p++ {
			res, err := s.javdb.CategoryMovies(ctx, javdb.CategoryQuery{
				ActorID: id, Page: javdb.Page{Page: p, Limit: limit},
			})
			if err != nil {
				break
			}
			allMovies = append(allMovies, res.Movies...)
			if len(res.Movies) < 40 {
				break
			}
		}
		soloIDs := map[string]bool{}
		for p := 1; p <= maxAggPages; p++ {
			res, err := s.javdb.CategoryMovies(ctx, javdb.CategoryQuery{
				ActorID: id, SoloOnly: true, Page: javdb.Page{Page: p, Limit: limit},
			})
			if err != nil {
				break
			}
			for _, m := range res.Movies {
				soloIDs[m.ID] = true
			}
			if len(res.Movies) < 40 {
				break
			}
		}
		seen := map[string]bool{}
		for _, m := range allMovies {
			if !soloIDs[m.ID] && !seen[m.ID] {
				seen[m.ID] = true
				movies = append(movies, m)
			}
		}
		maxPage = 1 // 一次性全量返回，前端无需继续翻页
	default: // "" 全部 / "solo" 单体（服务端 s flag 过滤）
		q := javdb.CategoryQuery{ActorID: id, Page: javdb.Page{Page: page, Limit: limit}, SortBy: sort}
		if mode == "solo" {
			q.SoloOnly = true
		}
		res, err := s.javdb.CategoryMovies(ctx, q)
		if err != nil {
			if errors.Is(err, javdb.ErrEmptyResult) {
				writeJSON(w, http.StatusOK, map[string]any{
					"movies": []any{}, "page": page, "maxPage": page,
				})
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		movies = res.Movies
		maxPage = res.MaxPage
		if res.Source == "api" {
			// 上游已服务端排序的方式（实测支持 release±/score/hit）跳过
			// 客户端重排：列表行无评分/评论数据，客户端重排只会打乱结果。
			switch sort {
			case javdb.SortNewest, javdb.SortOldest, javdb.SortHighest,
				javdb.SortMostPlayed, javdb.SortMostWatched:
				sort = ""
			}
		}
	}

	// 客户端排序：在返回结果上按 sort 排序（costar 已保持“全部”原序；
	// 上游已服务端排序时 sort 已被置空）。
	if sort != "" {
		sortMovies(movies, sort)
	}
	if movies == nil {
		movies = []javdb.Movie{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"movies":  movies,
		"page":    page,
		"maxPage": maxPage,
	})
}

// handleSeriesMovies serves the movie list of one series (合集): /api/series-movies/{id}.
// 复用 CategoryMovies 的系列页抓取（/series/{id}），支持分页与客户端排序。
func (s *Server) handleSeriesMovies(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 || parts[3] == "" {
		writeError(w, http.StatusBadRequest, "series ID required")
		return
	}
	id := parts[3]
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	results, err := s.javdb.CategoryMovies(r.Context(), javdb.CategoryQuery{
		Series: id,
		Page:   javdb.Page{Page: page, Limit: limit},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 客户端排序：后端 HTML 抓取无服务端排序能力，在此对返回结果排序。
	sort := javdb.SortBy(r.URL.Query().Get("sort"))
	if sort != "" {
		sortMovies(results.Movies, sort)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"movies":  results.Movies,
		"page":    results.Current,
		"maxPage": results.MaxPage,
	})
}

// handleListSearch searches JavDB community lists ("影單"): /api/lists/search?q=&page=.
// 走 web 后端的 /search?f=list 页面，匿名可用。
func (s *Server) handleListSearch(w http.ResponseWriter, r *http.Request) {
	keyword := strings.TrimSpace(r.URL.Query().Get("q"))
	if keyword == "" {
		writeError(w, http.StatusBadRequest, "query q required")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}
	lists, err := s.javdb.SearchLists(r.Context(), keyword, javdb.Page{Page: page})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lists": lists})
}

// handleListMovies serves the movie list of one community list: /api/lists/{id}?page=.
func (s *Server) handleListMovies(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 || parts[3] == "" {
		writeError(w, http.StatusBadRequest, "list ID required")
		return
	}
	id := parts[3]
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	results, err := s.javdb.ListMovies(r.Context(), id, javdb.Page{Page: page, Limit: limit})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	sort := javdb.SortBy(r.URL.Query().Get("sort"))
	if sort != "" {
		sortMovies(results.Movies, sort)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"movies":  results.Movies,
		"page":    results.Current,
		"maxPage": results.MaxPage,
	})
}

// handleSubscriptions serves GET/POST /api/subscriptions.
// GET 返回全部订阅，POST body {id,name,movies_count} 新增（重复忽略）。
func (s *Server) handleSubscriptions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"subscriptions": listSubscriptions()})
	case http.MethodPost:
		var sub Subscription
		if err := json.NewDecoder(r.Body).Decode(&sub); err != nil || strings.TrimSpace(sub.ID) == "" {
			writeError(w, http.StatusBadRequest, "subscription body must contain id")
			return
		}
		if err := addSubscription(sub); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"subscriptions": listSubscriptions()})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleSubscriptionDelete serves DELETE /api/subscriptions/{id}?kind=.
// kind 可选：collection（默认）/ genre / actor，同一 ID 可在不同 kind 下共存。
func (s *Server) handleSubscriptionDelete(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 || parts[3] == "" {
		writeError(w, http.StatusBadRequest, "subscription ID required")
		return
	}
	kind := r.URL.Query().Get("kind")
	if err := removeSubscription(parts[3], kind); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscriptions": listSubscriptions()})
}

// sortMovies 对影片列表进行客户端排序。
// 支持的排序：newest（最新）、oldest（最早）、highest（最高评分）、
// most_magnets（最多磁链）、most_played（最多播放）、most_watched（最多人看）、
// most_comments（最多评论）。
// 播放/观看数不在列表字段中，播放类回退评分、评论类回退评分人数。
func sortMovies(movies []javdb.Movie, sort javdb.SortBy) {
	if len(movies) == 0 {
		return
	}
	// 使用简单冒泡排序，影片列表通常不长
	for i := 0; i < len(movies)-1; i++ {
		for j := i + 1; j < len(movies); j++ {
			swap := false
			switch sort {
			case javdb.SortNewest:
				swap = movies[j].ReleaseDate > movies[i].ReleaseDate
			case javdb.SortOldest:
				swap = movies[j].ReleaseDate < movies[i].ReleaseDate
			case javdb.SortHighest:
				swap = movies[j].Score > movies[i].Score
			case javdb.SortLowest:
				swap = movies[j].Score < movies[i].Score
			case javdb.SortMostMagnet:
				swap = movies[j].MagnetsCount > movies[i].MagnetsCount
			case javdb.SortMostPlayed, javdb.SortMostWatched:
				// 无直接播放/观看数字段，回退到评分
				swap = movies[j].Score > movies[i].Score
			case javdb.SortMostComments:
				// 优先评论数（详情端才有），回退评分人数
				cj, ci := movies[j].Comments, movies[i].Comments
				if cj == 0 && ci == 0 {
					cj, ci = movies[j].Ratings, movies[i].Ratings
				}
				swap = cj > ci
			}
			if swap {
				movies[i], movies[j] = movies[j], movies[i]
			}
		}
	}
}

func (s *Server) handleMovie(w http.ResponseWriter, r *http.Request) {
	// Extract ID from path: /api/movie/{id}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "movie ID required")
		return
	}
	id := parts[3]

	// cast=1：仅拉取影片详情（供搜索结果补演员用），跳过磁链查询减半上游请求。
	if r.URL.Query().Get("cast") == "1" {
		movie, err := s.javdb.Movie(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"movie": movie})
		return
	}

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
	// TOP250 切面（合集）：year=2025 -> 2025TOP250，vtype=censored -> 有码TOP250。
	if kind == javdb.RankingTop250 || kind == "" {
		if year, yerr := strconv.Atoi(r.URL.Query().Get("year")); yerr == nil && year > 0 {
			q.Slice = javdb.Top250OfYear(year)
		}
		switch javdb.Category(r.URL.Query().Get("vtype")) {
		case javdb.CategoryCensored:
			q.Slice = javdb.Top250Censored
		case javdb.CategoryUncensored:
			q.Slice = javdb.Top250Uncensored
		case javdb.CategoryWestern:
			q.Slice = javdb.Top250Western
		case javdb.CategoryFC2:
			q.Slice = javdb.Top250FC2
		}
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

// handleSimilar serves related-movie recommendations: GET /api/similar/{id}.
// 相似维度：同一女演员（必须女性，拉其作品列表）、同系列 / 同番号前缀
// （/series、/video_codes 列表页）、同题材 tag（/tags?c{N} 页面，需网页
// Cookie，未导入时该维度静默跳过）。并行拉取、按 ID 去重（合并命中原因）、
// 排除当前影片，返回 {similar: [{movie, reason}]}。
func (s *Server) handleSimilar(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 || parts[3] == "" {
		writeError(w, http.StatusBadRequest, "movie ID required")
		return
	}
	id := parts[3]

	detail, err := s.javdb.Movie(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	const (
		perActress   = 4 // 每个女演员取的作品数
		maxActresses = 2 // 最多参与推荐的女演员数
		perTag       = 6 // 每个题材取的作品数
		maxTags      = 2
		perSeries    = 6
	)

	type agg struct {
		movie   javdb.Movie
		reasons map[string]bool
		score   int
	}
	byID := map[string]*agg{}
	var mu sync.Mutex
	add := func(movies []javdb.Movie, reason string, score int) {
		for _, m := range movies {
			if m.ID == "" || m.ID == id {
				continue
			}
			a, ok := byID[m.ID]
			if !ok {
				a = &agg{movie: m, reasons: map[string]bool{}}
				byID[m.ID] = a
			}
			if !a.reasons[reason] {
				a.reasons[reason] = true
				a.score += score
			}
		}
	}

	var wg sync.WaitGroup
	// goFetch 并行拉取一个维度的候选；limit 截断每个维度的配额
	// （app 后端忽略 limit 固定回一页 40 条，会挤占其他维度）。
	goFetch := func(limit int, fn func() ([]javdb.Movie, string, int)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			movies, reason, score := fn()
			if len(movies) > limit {
				movies = movies[:limit]
			}
			if len(movies) > 0 {
				mu.Lock()
				add(movies, reason, score)
				mu.Unlock()
			}
		}()
	}

	// 1) 同一女演员（必须女性）：显式 ♀ 优先；详情页全无性别标注时才用
	// 未标注者兜底（♂ 始终排除）。
	actresses := make([]javdb.Actor, 0, maxActresses)
	for _, a := range detail.ActorCredits {
		if a.Gender == "female" {
			actresses = append(actresses, a)
		}
	}
	if len(actresses) == 0 {
		for _, a := range detail.ActorCredits {
			if a.Gender != "male" {
				actresses = append(actresses, a)
			}
		}
	}
	if len(actresses) > maxActresses {
		actresses = actresses[:maxActresses]
	}
	for _, a := range actresses {
		actor := a
		goFetch(perActress, func() ([]javdb.Movie, string, int) {
			res, err := s.javdb.ActorMovies(r.Context(), actor.ID, javdb.Page{Page: 1, Limit: perActress})
			if err != nil {
				return nil, "", 0
			}
			return res.Movies, "同女演员·" + actor.Name, 5
		})
	}

	// 2) 同系列（作品系列优先，否则番号前缀；FC2 前缀范围过泛跳过）
	if detail.Series != nil && detail.Series.ID != "" {
		seriesID := detail.Series.ID
		seriesName := detail.Series.Name
		goFetch(perSeries, func() ([]javdb.Movie, string, int) {
			res, err := s.javdb.CategoryMovies(r.Context(), javdb.CategoryQuery{
				Series: seriesID,
				Page:   javdb.Page{Page: 1, Limit: perSeries},
			})
			if err != nil {
				return nil, "", 0
			}
			return res.Movies, "同系列·" + seriesName, 4
		})
	} else if prefix := seriesPrefix(detail.Code); prefix != "" {
		goFetch(perSeries, func() ([]javdb.Movie, string, int) {
			res, err := s.javdb.CategoryMovies(r.Context(), javdb.CategoryQuery{
				VideoCode: prefix,
				Page:      javdb.Page{Page: 1, Limit: perSeries},
			})
			if err != nil {
				// /video_codes 列表页需要 web 登录态；退回番号前缀搜索
				// （app API，匿名可用），多取一部以容自身占据首位。
				if sr, serr := s.javdb.SearchMovies(r.Context(), prefix, javdb.WithPage(1, perSeries+1)); serr == nil {
					return sr.Movies, "同系列·" + prefix, 3
				}
				return nil, "", 0
			}
			return res.Movies, "同系列·" + prefix, 3
		})
	}

	// 3) 同题材。web 详情链接自带 /tags?c{N}={id} 坐标；app API 详情只有
	// /tags/{id}，先用题材分组反查补齐 web 筛选组号（TagGroups 匿名可用且有
	// 缓存）。未导入 web cookie 时该维度会因登录墙静默失败，不影响其余维度。
	tags := make([]javdb.TagFilter, 0, len(detail.Genres))
	for _, g := range detail.Genres {
		group, id := javdb.ParseTagHref(g.Href)
		if id != "" {
			tags = append(tags, javdb.TagFilter{Group: group, ID: id, Name: g.Name})
		}
	}
	if groups, err := s.javdb.TagGroups(r.Context(), javdb.CategoryCensored); err == nil {
		byTag := map[string]string{} // tag id -> web c{N} 组号
		for _, grp := range groups {
			web := grp.CategoryID
			if mapped, ok := javdb.WebTagGroupID[grp.CategoryID]; ok {
				web = mapped // app API 组名（role/subject…）→ web c{N} 组号
			}
			for _, o := range grp.Options {
				byTag[o.ID] = web
			}
		}
		withGroup := make([]javdb.TagFilter, 0, len(tags))
		for _, t := range tags {
			if t.Group == "" {
				t.Group = byTag[t.ID]
			}
			if t.Group != "" {
				withGroup = append(withGroup, t)
			}
		}
		tags = withGroup
	} else {
		withGroup := tags[:0]
		for _, t := range tags {
			if t.Group != "" {
				withGroup = append(withGroup, t)
			}
		}
		tags = withGroup
	}
	if len(tags) > maxTags {
		tags = tags[:maxTags]
	}
	for _, t := range tags {
		tag := t
		goFetch(perTag, func() ([]javdb.Movie, string, int) {
			res, err := s.javdb.CategoryMovies(r.Context(), javdb.CategoryQuery{
				TagIDs: map[string]string{tag.Group: tag.ID},
				Page:   javdb.Page{Page: 1, Limit: perTag},
			})
			if err != nil {
				return nil, "", 0
			}
			return res.Movies, "同题材·" + tag.Name, 2
		})
	}

	wg.Wait()

	aggs := make([]*agg, 0, len(byID))
	for _, a := range byID {
		aggs = append(aggs, a)
	}
	sort.Slice(aggs, func(i, j int) bool {
		if aggs[i].score != aggs[j].score {
			return aggs[i].score > aggs[j].score
		}
		return aggs[i].movie.ReleaseDate > aggs[j].movie.ReleaseDate
	})

	const maxResults = 12
	type similarMovie struct {
		Movie  javdb.Movie `json:"movie"`
		Reason string      `json:"reason"`
	}
	out := make([]similarMovie, 0, maxResults)
	for _, a := range aggs {
		if len(out) >= maxResults {
			break
		}
		reasons := make([]string, 0, len(a.reasons))
		for r := range a.reasons {
			reasons = append(reasons, r)
		}
		sort.Strings(reasons)
		out = append(out, similarMovie{Movie: a.movie, Reason: strings.Join(reasons, " / ")})
	}
	writeJSON(w, http.StatusOK, map[string]any{"similar": out})
}

// seriesPrefix extracts the letter prefix of a code ("SONE-340" -> "SONE").
// 仅保留 2–6 个纯字母的前缀，FC2 等过泛前缀返回空。
func seriesPrefix(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if i := strings.IndexByte(code, '-'); i > 0 {
		code = code[:i]
	}
	if len(code) < 2 || len(code) > 6 || code == "FC2" {
		return ""
	}
	for _, r := range code {
		if r < 'A' || r > 'Z' {
			return ""
		}
	}
	return code
}

// handleReviews serves JavDB user comments: GET /api/reviews/{id}?sort=&page=。
// 走 app API（公开可用）。按需拉取：每次只透传一页（默认 10 条），前端滚动
// 加载更多，翻到末页为止（无上限）。该端点不支持服务端排序（实测所有
// sort_by 取值同序，默认即热度序），hotly 直接用服务端热度序；latest 由
// 前端对已加载的累计数据做本地重排，加载越多越准。
func (s *Server) handleReviews(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 || parts[3] == "" {
		writeError(w, http.StatusBadRequest, "movie ID required")
		return
	}
	sort := r.URL.Query().Get("sort")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}
	res, err := s.javdb.Reviews(r.Context(), javdb.ReviewQuery{
		MovieID: parts[3],
		Sort:    javdb.SortBy(sort),
		Page:    javdb.Page{Page: page, Limit: 10},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	reviews := res.Reviews
	if reviews == nil {
		reviews = []javdb.Review{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reviews":      reviews,
		"total":        res.Total, // app 响应的真实总数（0 表示端点未返回）
		"current_page": page,
	})
}

// sortReviewSlice 已随评论聚合改为按需拉取而移除：服务端只透传单页，
// hotly 用服务端热度序，latest 由前端对累计数据本地重排。

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	tags, err := s.javdb.TagGroups(r.Context(), javdb.Category(category))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 每组附上 web 端筛选组号（/tags?c{N}={id}），供题材浏览使用。
	groups := make([]map[string]any, 0, len(tags))
	for _, g := range tags {
		entry := map[string]any{
			"category_id": g.CategoryID,
			"name":        g.Name,
			"options":     g.Options,
		}
		if web, ok := javdb.WebTagGroupID[g.CategoryID]; ok {
			entry["web_group_id"] = web
		}
		groups = append(groups, entry)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tags": groups,
	})
}

// handleGenre serves tag-scoped browsing: /api/genre?group=2&tag=1&page=.
// 题材浏览由 app 端 /v1/movies/tags 承载（tag id 全局唯一，group 键仅作
// 兼容保留），无需网页版登录态；app API 不可用时客户端自动回退 web 端。
func (s *Server) handleGenre(w http.ResponseWriter, r *http.Request) {
	group := strings.TrimSpace(r.URL.Query().Get("group"))
	tag := strings.TrimSpace(r.URL.Query().Get("tag"))
	if group == "" || tag == "" {
		writeError(w, http.StatusBadRequest, "group and tag required")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	results, err := s.javdb.CategoryMovies(r.Context(), javdb.CategoryQuery{
		TagIDs: map[string]string{group: tag},
		Page:   javdb.Page{Page: page, Limit: limit},
	})
	if err != nil {
		if errors.Is(err, javdb.ErrEmptyResult) {
			// app 端点不报总页数（合成边界翻过末页时命中），以空列表呈现而非报错。
			writeJSON(w, http.StatusOK, map[string]any{
				"movies": []any{}, "page": page, "maxPage": page,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	sort := javdb.SortBy(r.URL.Query().Get("sort"))
	if sort != "" {
		sortMovies(results.Movies, sort)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"movies":  results.Movies,
		"page":    results.Current,
		"maxPage": results.MaxPage,
	})
}

// handleWebCookie imports a browser web-session cookie (POST /api/web-cookie,
// body {"cookie": "..."}) to unlock login-walled web pages (/tags).
// 验证码登录无法自动化，导入是唯一的网页版登录途径；同时持久化。
func (s *Server) handleWebCookie(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		Cookie string `json:"cookie"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Cookie) == "" {
		writeError(w, http.StatusBadRequest, "cookie required")
		return
	}
	s.javdb.SetWebCookie(req.Cookie)
	cookie, token := s.javdb.Session()
	s.mu.Lock()
	// 保留已保存的账号密码（若有），只更新 cookie/token。
	saveSession(cookie, token, s.username, s.password)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
	variant := r.URL.Query().Get("variant") // 可选："uncensored" / "cnsub" / "normal"

	// 惰性加载：指定 variant 时仅解析该变体，未指定时回退全量解析
	var streams []av.Stream
	var err error
	if variant != "" {
		streams, err = s.av.ResolveVariant(r.Context(), code, variant, source)
	} else {
		streams, err = s.av.Resolve(r.Context(), code, source)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"streams": streams,
	})
}

// avProbeTTL 是探测结果的缓存时长：详情页每次进入都会触发逐源探测，
// 而上游站点抓取明显偏重；同一番号+源的探测结果（可用变体列表）
// 短时间变化很小，10 分钟内直接复用。
const avProbeTTL = 10 * time.Minute

// avProbeEntry 是缓存中的一条探测结果。
type avProbeEntry struct {
	result  *av.ProbeResult
	expires time.Time
}

// handleAVProbe 轻量探测番号的可用变体（仅抓取 HTML，不拉取播放列表）。
// 用于前端在详情页快速展示变体按钮，用户点击后才按需调用 resolve。
// 结果按 (source, code) 做短时缓存（avProbeTTL）。
func (s *Server) handleAVProbe(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		writeError(w, http.StatusBadRequest, "video code required")
		return
	}
	code := parts[4]
	source := r.URL.Query().Get("source")

	key := source + "|" + code
	s.avProbeMu.Lock()
	if ent, ok := s.avProbeCache[key]; ok && time.Now().Before(ent.expires) {
		s.avProbeMu.Unlock()
		writeJSON(w, http.StatusOK, ent.result)
		return
	}
	s.avProbeMu.Unlock()

	// 总超时兜底：上游站点全不可达时，逐源探测（3 源×单源 25s）会挂
	// 75s+；服务器侧 40s 快速失败，前端好尽早进入“无播放源”降级。
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	result, err := s.av.Probe(ctx, code, source)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.avProbeMu.Lock()
	if s.avProbeCache == nil {
		s.avProbeCache = make(map[string]avProbeEntry)
	}
	// 惰性清理：写入时顺手摘掉过期条目，控制长期运行的内存占用。
	now := time.Now()
	for k, e := range s.avProbeCache {
		if now.After(e.expires) {
			delete(s.avProbeCache, k)
		}
	}
	s.avProbeCache[key] = avProbeEntry{result: result, expires: now.Add(avProbeTTL)}
	s.avProbeMu.Unlock()

	writeJSON(w, http.StatusOK, result)
}

// handleAVCFCookie 接受前端/脚本提交的 Cloudflare cf_clearance Cookie。
// 提交后服务端会存储并在后续请求中自动注入，绕过 CF 质询。
//
//	POST /api/av/cf-cookie
//	{"host": "missav.ws", "cookie": "cf_clearance=xxx", "ua": "Mozilla/5.0..."}
func (s *Server) handleAVCFCookie(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host   string `json:"host"`
		Cookie string `json:"cookie"`
		UA     string `json:"ua"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Host == "" || req.Cookie == "" {
		writeError(w, http.StatusBadRequest, "host and cookie are required")
		return
	}

	cred := av.CFCredential{
		Cookie: req.Cookie,
		UA:     req.UA,
	}
	s.av.SetCFCookie(req.Host, cred)

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"host":   req.Host,
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

	// 总超时兜底（同 handleAVProbe）：上游不可达时快速失败。
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	video, err := s.av.Detail(ctx, code, source)
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

// handleAVSources 返回已注册的 AV 数据源列表（按优先级排序）。
func (s *Server) handleAVSources(w http.ResponseWriter, r *http.Request) {
	sources := s.av.Sources()
	writeJSON(w, http.StatusOK, map[string]any{
		"sources": sources,
	})
}

// health check endpoint
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"time":    time.Now().Format(time.RFC3339),
		"version": "1.0.0",
	})
}
