package javdb

import (
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Detail-page parser, ported from the reference Rust implementation
// (JAVDB_AutoSpider javdb/rust_core/src/scraper/detail_parser.rs).
//
// Labels are bilingual because JavDB serves Traditional Chinese by default and
// English on some paths / locales.

var detailLabels = map[string][]string{
	"code":      {"番號:", "ID:"},
	"date":      {"日期:", "Released Date:"},
	"duration":  {"時長:", "Duration:"},
	"director":  {"導演:", "Director:"},
	"maker":     {"片商:", "Maker:"},
	"publisher": {"發行商:", "Publisher:"},
	"series":    {"系列:", "Series:"},
	"rating":    {"評分:", "Rating:"},
	"tags":      {"類別:", "Tags:"},
	"actor":     {"演員:", "Actor(s):"},
}

// parseDetail reads a /v/{id} page into a Detail.
func parseDetail(doc *goquery.Document, movieID, source string) *Detail {
	d := &Detail{Movie: Movie{ID: movieID, Href: "/v/" + movieID, Source: source}}
	d.OriginTitle = strings.TrimSpace(doc.Find("strong.current-title").First().Text())
	d.Title = d.OriginTitle
	if d.Title == "" {
		d.Title = strings.TrimSpace(doc.Find("h2.title strong").First().Text())
	}

	panel := doc.Find("div.video-meta-panel")
	blocks := panel.Find("div.panel-block")

	value := func(key string) *goquery.Selection {
		block := findPanelBlock(blocks, key)
		if block == nil {
			return nil
		}
		v := block.Find("span.value").First()
		if v.Length() == 0 {
			return nil
		}
		return v
	}
	text := func(key string) string {
		v := value(key)
		if v == nil {
			return ""
		}
		return collapseSpace(v.Text())
	}
	links := func(key string) []Link {
		v := value(key)
		if v == nil {
			return nil
		}
		var out []Link
		v.Find("a").Each(func(_ int, a *goquery.Selection) {
			href, _ := a.Attr("href")
			name := collapseSpace(a.Text())
			if name == "" {
				return
			}
			out = append(out, Link{Name: name, ID: HrefID(href), Href: NormalizeHref(href), Kind: linkKind(href)})
		})
		return out
	}

	if codeBlock := value("code"); codeBlock != nil {
		d.Code = NormalizeCode(collapseSpace(codeBlock.Text()))
		if a := codeBlock.Find("a").First(); a.Length() > 0 {
			href, _ := a.Attr("href")
			d.CodePrefix = &Link{Name: collapseSpace(a.Text()), ID: HrefID(href), Href: NormalizeHref(href), Kind: "video_code"}
		}
	}
	d.ReleaseDate = text("date")
	d.Duration = ParseInt(text("duration"))
	d.Directors = links("director")
	d.Maker = firstLink(links("maker"))
	d.Publisher = firstLink(links("publisher"))
	d.Series = firstLink(links("series"))
	d.Genres = links("tags")
	d.ActorCredits = parseDetailActors(blocks)

	if rating := value("rating"); rating != nil {
		scoreText := collapseSpace(rating.Text())
		rate, comments := SplitRateAndComments(scoreText)
		d.Score = ParseFloat(rate)
		d.RateText = scoreText
		d.Ratings = ParseInt(comments)
	}

	if cover := panel.Find("div.column-video-cover img.video-cover").First(); cover.Length() > 0 {
		src, _ := cover.Attr("src")
		d.PosterURL = FixImageURL(src)
		d.CoverURL = d.PosterURL
		d.ThumbURL = d.PosterURL
	}
	doc.Find("div.tile-images a.tile-item").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		// 预览 tile 的 href 一律指向大图；广告位链接指向跳转页而非图片，
		// 以此滤除（广告 img 本身可能是 .jpg，不可信）。
		large := imageAssetURL(href)
		if large == "" {
			return
		}
		large = FixImageURL(large)
		d.FanartURLs = append(d.FanartURLs, large)
		d.PreviewImages = append(d.PreviewImages, large)
	})
	if v := doc.Find("video#preview-video").First(); v.Length() > 0 {
		src, _ := v.Attr("src")
		if src == "" || strings.HasPrefix(src, "blob:") {
			src, _ = v.Find("source").First().Attr("src")
		}
		d.PreviewVideo = src
	}
	if tab := doc.Find("a.review-tab").First(); tab.Length() > 0 {
		d.ReviewsCount = ParseInt(tab.Text())
	}
	doc.Find("span.is-size-7").Each(func(_ int, s *goquery.Selection) {
		t := collapseSpace(s.Text())
		switch {
		case strings.Contains(t, "人想看"), strings.Contains(t, "want to watch"):
			d.WantCount = ParseInt(t)
		case strings.Contains(t, "人看過"), strings.Contains(t, "have seen"):
			d.WatchedCount = ParseInt(t)
		}
	})

	d.Magnets = parseMagnets(doc)
	d.MagnetsFetched = doc.Find("div#magnets-content").Length() > 0
	if len(d.Magnets) > 0 {
		d.MagnetsCount = len(d.Magnets)
	}
	for _, m := range d.Magnets {
		if m.CNSub {
			d.HasCNSub = true
		}
	}
	for _, a := range d.ActorCredits {
		if a.Name != "" {
			d.Actors = append(d.Actors, a.Name)
		}
	}
	for _, g := range d.Genres {
		d.Tags = append(d.Tags, g.Name)
	}
	return d
}

// parseDetailActors reads the 演員 panel, including the ♀/♂ gender marker that
// JavDB prints in a <strong class="symbol female|male"> after each link.
func parseDetailActors(blocks *goquery.Selection) []Actor {
	block := findPanelBlock(blocks, "actor")
	if block == nil {
		return nil
	}
	var out []Actor
	block.Find("span.value a").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		if !strings.Contains(href, "/actors/") {
			return
		}
		name := collapseSpace(a.Text())
		if name == "" {
			return
		}
		actor := Actor{ID: HrefID(href), Name: name, Href: NormalizeHref(href), Gender: genderAfter(a)}
		out = append(out, actor)
	})
	return out
}

// genderAfter inspects the sibling <strong class="symbol female"> marker.
func genderAfter(a *goquery.Selection) string {
	next := a.Next()
	for i := 0; i < 3 && next.Length() > 0; i++ {
		if next.Is("a") {
			return ""
		}
		classes := next.AttrOr("class", "")
		switch {
		case strings.Contains(classes, "female"):
			return "female"
		case strings.Contains(classes, "male"):
			return "male"
		}
		next = next.Next()
	}
	return ""
}

// parseMagnets reads the magnet table, skipping the ad rows JavDB injects
// into the same container.
func parseMagnets(doc *goquery.Document) []Magnet {
	content := doc.Find("div#magnets-content")
	if content.Length() == 0 {
		return nil
	}
	var out []Magnet
	content.Find("div.item").Each(func(_ int, item *goquery.Selection) {
		if item.Closest("div.sda-content").Length() > 0 {
			return
		}
		nameDiv := item.Find("div.magnet-name").First()
		if nameDiv.Length() == 0 {
			return
		}
		a := nameDiv.Find("a").First()
		if a.Length() == 0 {
			return
		}
		href, _ := a.Attr("href")
		m := Magnet{
			Name:   collapseSpace(a.Find("span.name").First().Text()),
			Source: "web",
		}
		if m.Name == "" {
			m.Name = collapseSpace(a.Text())
		}
		meta := collapseSpace(a.Find("span.meta").First().Text())
		m.SizeText = findSize(meta)
		m.SizeBytes = ParseSizeBytes(m.SizeText)
		m.Files = ParseFileCount(meta)
		item.Find("span.tag").Each(func(_ int, s *goquery.Selection) {
			t := strings.TrimSpace(s.Text())
			if t != "" {
				m.Tags = append(m.Tags, t)
			}
			switch {
			case strings.Contains(t, "中字"):
				m.CNSub = true
			case strings.Contains(t, "高清"), strings.Contains(t, "HD"):
				m.HD = true
			}
		})
		if ts := item.Find("span.time").First(); ts.Length() > 0 {
			if title := ts.AttrOr("title", ""); title != "" {
				m.CreatedAt = firstDate(title)
				if m.CreatedAt == "" {
					m.CreatedAt = title
				}
			} else {
				m.CreatedAt = firstDate(collapseSpace(ts.Text()))
			}
		}
		if strings.HasPrefix(href, "magnet:") {
			m.Hash = hashFromMagnet(href)
		}
		if m.Name != "" || m.Hash != "" {
			out = append(out, m)
		}
	})
	return out
}

func hashFromMagnet(uri string) string {
	i := strings.Index(uri, "btih:")
	if i < 0 {
		return ""
	}
	rest := uri[i+len("btih:"):]
	if j := strings.IndexAny(rest, "&#"); j >= 0 {
		rest = rest[:j]
	}
	return strings.ToLower(rest)
}

// findPanelBlock locates the panel block whose <strong> label matches one of
// the bilingual labels for key.
func findPanelBlock(blocks *goquery.Selection, key string) *goquery.Selection {
	labels := detailLabels[key]
	var found *goquery.Selection
	blocks.EachWithBreak(func(_ int, block *goquery.Selection) bool {
		strong := block.Find("strong").First()
		if strong.Length() == 0 {
			return true
		}
		text := collapseSpace(strong.Text())
		for _, lbl := range labels {
			if text == lbl {
				found = block
				return false
			}
		}
		return true
	})
	return found
}

func linkKind(href string) string {
	switch {
	case strings.Contains(href, "/actors/"):
		return "actor"
	case strings.Contains(href, "/makers/"):
		return "maker"
	case strings.Contains(href, "/publishers/"):
		return "publisher"
	case strings.Contains(href, "/series/"):
		return "series"
	case strings.Contains(href, "/directors/"):
		return "director"
	case strings.Contains(href, "/video_codes/"):
		return "video_code"
	case strings.Contains(href, "/tags"):
		return "tag"
	default:
		return ""
	}
}

func firstLink(links []Link) *Link {
	if len(links) == 0 {
		return nil
	}
	l := links[0]
	return &l
}

// findSize pulls the first "1.24GB"-style token out of magnet meta text.
func findSize(s string) string {
	for _, m := range sizeRe.FindAllString(s, -1) {
		if ParseSizeBytes(m) > 0 {
			return strings.TrimSpace(m)
		}
	}
	return ""
}

// imageAssetURL returns u when it points at a static image asset (image file
// extension, query/fragment stripped), otherwise "". JavDB inserts ad tiles
// between the preview images whose links lead to redirect pages, so this
// filters them out while keeping real preview URLs.
func imageAssetURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	switch {
	case strings.HasSuffix(strings.ToLower(u), ".jpg"),
		strings.HasSuffix(strings.ToLower(u), ".jpeg"),
		strings.HasSuffix(strings.ToLower(u), ".png"),
		strings.HasSuffix(strings.ToLower(u), ".webp"),
		strings.HasSuffix(strings.ToLower(u), ".gif"),
		strings.HasSuffix(strings.ToLower(u), ".avif"):
		return u
	}
	return ""
}
