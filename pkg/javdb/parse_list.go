package javdb

import (
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// HTML listing parser. Selectors mirror the production reference implementation
// (JAVDB_AutoSpider javdb/rust_core/src/scraper/index_parser.rs) so both
// projects agree on what a card means:
//
//	<div class="movie-list h cols-4 vcols-8">
//	  <div class="item"><a class="box" href="/v/<id>" title="...">
//	    <div class="cover"><img src="..."> <span class="ranking">1</span></div>
//	    <div class="video-title"><strong>MIDA-783</strong> 标题…</div>
//	    <div class="score"><span class="value">4.45分, 由386人評價</span></div>
//	    <div class="meta">2026-09-15</div>
//	    <div class="tags has-addons"><span class="tag is-success">含磁鏈</span></div>
//	  </a></div>
//	</div>

const (
	tagWithMagnet   = "含磁鏈"
	tagWithSubtitle = "含中字磁鏈"
	tagToday        = "今日新種"
	tagPlayable     = "可播放"
)

// isListingBadge reports whether a tag chip is one of JavDB's utility badges
// rather than a real genre, so callers get clean Tags lists.
func isListingBadge(text string) bool {
	switch text {
	case tagWithMagnet, tagWithSubtitle, tagToday, tagPlayable:
		return true
	}
	return strings.Contains(text, "磁鏈")
}

// parseMovieList extracts every card from every movie-list on the page
// (home pages carry a recommendation strip plus the main listing).
func parseMovieList(doc *goquery.Document, pageNum int) []Movie {
	var out []Movie
	doc.Find("div.movie-list div.item").Each(func(_ int, item *goquery.Selection) {
		m, ok := parseMovieItem(item, pageNum)
		if ok {
			out = append(out, m)
		}
	})
	if len(out) > 0 {
		return out
	}
	// Fall back to any anchor into /v/ (recommendation carousels, lists).
	doc.Find("a[href^='/v/']").Each(func(_ int, a *goquery.Selection) {
		if a.Parent().Is("div.item") {
			return
		}
		if m, ok := parseMovieAnchor(a, pageNum); ok {
			out = append(out, m)
		}
	})
	return out
}

func parseMovieItem(item *goquery.Selection, pageNum int) (Movie, bool) {
	a := item.Find("a.box").First()
	if a.Length() == 0 {
		a = item.Find("a").First()
	}
	if a.Length() == 0 {
		return Movie{}, false
	}
	m, ok := parseMovieAnchor(a, pageNum)
	if !ok {
		return m, false
	}

	// Score block: "4.45分, 由386人評價" wrapped in star markup.
	if scoreText := strings.TrimSpace(a.Find("div.score span.value").First().Text()); scoreText != "" {
		rate, comments := SplitRateAndComments(collapseSpace(scoreText))
		m.Score = ParseFloat(rate)
		m.RateText = collapseSpace(scoreText)
		m.Ratings = ParseInt(comments)
	}

	// Meta line is normally the release date, sometimes "日期 / 片商" pairs.
	if meta := strings.TrimSpace(a.Find("div.meta").First().Text()); meta != "" {
		m.ReleaseDate = firstDate(meta)
		if m.ReleaseDate == "" {
			m.ReleaseDate = collapseSpace(meta)
		}
	}

	// Tag badges.
	a.Find("div.tags.has-addons span.tag, div.tags span.tag").Each(func(_ int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if text == "" {
			return
		}
		switch {
		case strings.Contains(text, "中字"):
			m.HasCNSub = true
		case text == tagToday:
			m.NewMagnets = true
		case text == tagPlayable:
			m.CanPlay = true
		}
		if !isListingBadge(text) {
			// 卡片角标题材名转简体展示。
			m.Tags = append(m.Tags, ToSimplified(text))
		}
	})

	// Ranking badge (TOP250 / 热播 pages).
	if r := strings.TrimSpace(a.Find("span.ranking").First().Text()); r != "" {
		if n, err := strconv.Atoi(r); err == nil {
			m.Ranking = n
		}
	}

	// Cover image (lazy loading keeps the URL in data-src). Both web and app
	// listings serve wide 16:9 stills from /covers/; the vertical 2:3 card art
	// lives under /thumbs/ and is derived via posterFromCover.
	cover := a.Find("div.cover").First()
	img := cover.Find("img").First()
	if img.Length() > 0 {
		src, _ := img.Attr("src")
		if src == "" {
			src, _ = img.Attr("data-src")
		}
		m.CoverURL = FixImageURL(src)
		m.ThumbURL = m.CoverURL
		m.PosterURL = posterFromCover(m.CoverURL)
	}
	if cover.Is(".tag-can-play") || cover.Find(".tag-can-play").Length() > 0 {
		m.CanPlay = true
	}
	return m, true
}

// parseMovieAnchor reads href + title, the two fields every card variant has.
func parseMovieAnchor(a *goquery.Selection, pageNum int) (Movie, bool) {
	href, _ := a.Attr("href")
	if href == "" {
		return Movie{}, false
	}
	m := Movie{Page: pageNum, Href: NormalizeHref(href), Source: "web"}
	m.ID = HrefID(href)

	titleSel := a.Find("div.video-title").First()
	full := collapseSpace(titleSel.Text())
	if full == "" {
		full = collapseSpace(a.AttrOr("title", ""))
	}
	if strong := collapseSpace(titleSel.Find("strong").First().Text()); strong != "" {
		m.Code = NormalizeCode(strong)
		m.Title = strings.TrimSpace(strings.TrimPrefix(full, strong))
	} else if full != "" {
		parts := strings.SplitN(full, " ", 2)
		if IsPlausibleVideoCode(parts[0]) {
			m.Code = NormalizeCode(parts[0])
			if len(parts) > 1 {
				m.Title = parts[1]
			}
		} else {
			m.Title = full
		}
	}
	if m.Code == "" && m.Title == "" {
		return Movie{}, false
	}
	m.OriginTitle = m.Title
	return m, true
}

// parseActorBoxes reads the actor grid used by /actors*, /rankings/actors and
// actor sections on detail pages:
//
//	<div class="box actor-box"><a href="/actors/kzx6" title="别名,…">
//	  <figure class="image"><img class="avatar" src="…"></figure><strong>名字</strong>
func parseActorBoxes(doc *goquery.Document) []Actor {
	var out []Actor
	seen := map[string]bool{}
	collect := func(sel *goquery.Selection) {
		sel.Each(func(_ int, a *goquery.Selection) {
			href, _ := a.Attr("href")
			if !strings.Contains(href, "/actors/") {
				return
			}
			id := HrefID(href)
			if id == "" || seen[id] {
				return
			}
			seen[id] = true
			actor := Actor{ID: id, Href: NormalizeHref(href), Source: "web"}
			actor.Name = collapseSpace(a.Find("strong").First().Text())
			if actor.Name == "" {
				actor.Name = collapseSpace(a.Text())
			}
			if title := collapseSpace(a.AttrOr("title", "")); title != "" {
				parts := strings.Split(title, ",")
				if strings.TrimSpace(parts[0]) != actor.Name && actor.Name == "" {
					actor.Name = strings.TrimSpace(parts[0])
				}
				if len(parts) > 1 {
					actor.OtherName = strings.TrimSpace(strings.Join(parts[1:], ", "))
				}
			}
			img := a.Find("img").First()
			src, _ := img.Attr("src")
			if src == "" {
				src, _ = img.Attr("data-src")
			}
			actor.AvatarURL = FixImageURL(src)
			if n := ParseInt(a.Find("span.video-count, div.video-count").First().Text()); n > 0 {
				actor.VideosCount = n
			}
			if actor.Name != "" {
				out = append(out, actor)
			}
		})
	}
	collect(doc.Find("#actors .actor-box a, div.actors .actor-box a"))
	if len(out) == 0 {
		collect(doc.Find(".actor-list a[href^='/actors/'], div.column a[href^='/actors/']"))
	}
	return out
}

// parsePagination reads Bulma pagination: current page and the largest
// numbered link (the site never prints a real total page count).
func parsePagination(doc *goquery.Document) (current, maxPage int) {
	cur := doc.Find("a.pagination-link.is-current, span.pagination-link.is-current").First()
	if cur.Length() > 0 {
		current = ParseInt(cur.Text())
	}
	doc.Find("a.pagination-link").Each(func(_ int, s *goquery.Selection) {
		n := ParseInt(s.Text())
		if n > maxPage {
			maxPage = n
		}
	})
	if current == 0 {
		current = 1
	}
	if doc.Find("a.pagination-next").Length() > 0 && maxPage <= current {
		// "下一頁" exists but the numbered list was truncated: the true total
		// is unknown, so report one page beyond what we saw.
		maxPage = current + 1
	}
	return current, maxPage
}

// ParsePageParam is exported for callers that build their own listing loops.
func ParsePageParam(href string) int {
	i := strings.Index(href, "?")
	if i < 0 {
		return 1
	}
	q := strings.SplitN(href[i+1:], "#", 2)[0]
	for _, pair := range strings.Split(q, "&") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 && kv[0] == "page" {
			n, _ := strconv.Atoi(kv[1])
			if n > 0 {
				return n
			}
		}
	}
	return 1
}

// collapseSpace normalises whitespace so titles compare cleanly.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// firstDate finds the first "YYYY-MM-DD" substring in scraped text.
func firstDate(s string) string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == '/' || r == '、' || r == ','
	})
	for _, f := range fields {
		if IsReleaseDate(f) {
			return f
		}
	}
	return ""
}

// eachWithBreak is intentionally absent: goquery selections are an external
// type, so iteration helpers are plain functions instead of methods.
