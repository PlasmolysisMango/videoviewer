package aacg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

// Article fetches one article detail page and returns its metadata, plain text and video links.
func (c *Client) Article(ctx context.Context, articleURL string) (ArticleDetail, error) {
	budget := maxHops
	body, resp, err := c.get(ctx, articleURL, "", &budget)
	if err != nil {
		return ArticleDetail{}, fmt.Errorf("aacg article: %w", err)
	}
	if challengePage(body) {
		return ArticleDetail{}, fmt.Errorf("aacg article: challenge page")
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return ArticleDetail{}, err
	}
	return extractArticle(doc, resp.Request.URL)
}

func extractArticle(doc *goquery.Document, base *url.URL) (ArticleDetail, error) {
	post, err := contentOne(doc.Find("div#post[role=main]"), "article page")
	if err != nil {
		return ArticleDetail{}, err
	}
	article, err := contentOne(post.Find("article[itemtype$='/BlogPosting']"), "article")
	if err != nil {
		return ArticleDetail{}, err
	}
	result := ArticleDetail{Videos: []VideoLink{}, ContentParts: []ContentPart{}}
	result.URL = base.String()
	headline, err := contentOne(article.Find(`h1[itemprop~="headline"]`), "article headline")
	if err != nil {
		return ArticleDetail{}, err
	}
	result.Title = contentSpace(contentText(headline))
	if result.Title == "" {
		return ArticleDetail{}, fmt.Errorf("aacg article: empty article title")
	}
	result.Summary, err = articleMeta(doc.Selection, `meta[name="description"]`, "description")
	if err != nil {
		return ArticleDetail{}, err
	}
	result.PublishedAt, err = articleMeta(article, `meta[itemprop="datePublished"]`, "article date")
	if err != nil {
		return ArticleDetail{}, err
	}
	cover, err := articleMeta(article, `meta[itemprop="image"]`, "article cover")
	if err != nil {
		return ArticleDetail{}, err
	}
	if cover != "" {
		result.CoverURL, err = contentURL(base, cover)
		if err != nil {
			return ArticleDetail{}, err
		}
	}
	body, err := contentOne(article.Find(`div.post-content[itemprop="articleBody"]`), "article body")
	if err != nil {
		return ArticleDetail{}, err
	}
	result.Videos, err = articleVideos(article, base)
	if err != nil {
		return ArticleDetail{}, err
	}
	// post-near sits inside the body and is stripped below, so read it first.
	result.Previous, result.Next = articleNear(article, base)
	articleStrip(body)
	result.Content = contentText(body)
	if result.Content == "" {
		return ArticleDetail{}, fmt.Errorf("aacg article: empty article content")
	}
	result.ContentParts = articleContentParts(body, base, result.Content)
	return result, nil
}

// 合集文章里指向其他文章的站内链接只认 /archives/<id>/ 形式。
var articleLinkPattern = regexp.MustCompile(`^/archives/\d+/?$`)

// articleContentParts splits the plain-text Content into text, link and image
// runs. Anchors are located by walking them in document order with a cursor
// over Content; skipped anchors (ads, self links) still consume their text so
// repeated anchor texts keep pairing with the right occurrence. Body images
// are placed by the collapsed-text length preceding them.
func articleContentParts(body *goquery.Selection, base *url.URL, content string) []ContentPart {
	type articleAnchor struct {
		text string
		url  string // empty = consume the text, do not emit a link
	}
	anchors := []articleAnchor{}
	body.Find("a[href]").Each(func(_ int, link *goquery.Selection) {
		text := contentText(link)
		if text == "" {
			return
		}
		anchors = append(anchors, articleAnchor{text: text, url: articleLinkURL(link.AttrOr("href", ""), base)})
	})
	parts := []ContentPart{}
	cursor, emitted := 0, 0
	hasLink := false
	for _, anchor := range anchors {
		index := strings.Index(content[cursor:], anchor.text)
		if index < 0 {
			continue
		}
		start := cursor + index
		end := start + len(anchor.text)
		cursor = end
		if anchor.url == "" {
			continue
		}
		if start > emitted {
			parts = append(parts, ContentPart{Text: content[emitted:start]})
		}
		parts = append(parts, ContentPart{Text: content[start:end], URL: anchor.url})
		emitted = end
		hasLink = true
	}
	images := articleImages(body, content)
	if !hasLink {
		if len(images) == 0 {
			return parts
		}
		parts = append(parts, ContentPart{Text: content})
	} else if emitted < len(content) {
		parts = append(parts, ContentPart{Text: content[emitted:]})
	}
	return insertArticleImages(parts, images)
}

// insertArticleImages weaves image runs into text parts at their collapsed-text
// positions. Every text run stays a contiguous slice of content, so joining
// all Text fields still reproduces content exactly. Images strictly inside a
// link run are dropped; images touching a link boundary stay in place.
func insertArticleImages(parts []ContentPart, images []articleImage) []ContentPart {
	if len(images) == 0 {
		return parts
	}
	result := make([]ContentPart, 0, len(parts)+len(images))
	offset := 0
	image := 0
	for _, part := range parts {
		start := offset
		end := offset + len(part.Text)
		offset = end
		if part.URL != "" {
			for image < len(images) && images[image].pos <= start {
				result = append(result, ContentPart{ImageURL: images[image].url})
				image++
			}
			for image < len(images) && images[image].pos < end {
				image++
			}
			result = append(result, part)
			continue
		}
		cursor := 0
		for image < len(images) && images[image].pos < end {
			pos := images[image].pos
			if pos <= start+cursor {
				result = append(result, ContentPart{ImageURL: images[image].url})
				image++
				continue
			}
			cut := pos - start
			result = append(result, ContentPart{Text: part.Text[cursor:cut]})
			result = append(result, ContentPart{ImageURL: images[image].url})
			image++
			cursor = cut
		}
		if cursor < len(part.Text) {
			result = append(result, ContentPart{Text: part.Text[cursor:]})
		}
	}
	for image < len(images) {
		result = append(result, ContentPart{ImageURL: images[image].url})
		image++
	}
	return result
}

// articleImage is one body image and the length of the collapsed text before it.
type articleImage struct {
	pos int
	url string
}

// articleImages locates body images in the collapsed content stream. Positions
// are computed by truncating a clone right after each image and re-collapsing:
// the clone text length then equals the text preceding the image. Truncation
// runs from the last image to the first, shrinking the clone in place.
func articleImages(body *goquery.Selection, content string) []articleImage {
	type imageNode struct {
		node *html.Node
		url  string
	}
	nodes := []imageNode{}
	body.Find("img").Each(func(_ int, img *goquery.Selection) {
		url := articleImageURL(img)
		if url == "" || img.Length() == 0 {
			return
		}
		nodes = append(nodes, imageNode{node: img.Get(0), url: url})
	})
	if len(nodes) == 0 {
		return nil
	}
	root := body.Get(0)
	clone := body.Clone()
	cloneRoot := clone.Get(0)
	if root == nil || cloneRoot == nil {
		return nil
	}
	images := make([]articleImage, len(nodes))
	found := make([]bool, len(nodes))
	for i := len(nodes) - 1; i >= 0; i-- {
		path, ok := nodePath(root, nodes[i].node)
		if !ok {
			continue
		}
		target := nodeAt(cloneRoot, path)
		if target == nil {
			continue
		}
		removeFollowing(cloneRoot, target)
		images[i] = articleImage{pos: min(len(contentText(clone)), len(content)), url: nodes[i].url}
		found[i] = true
	}
	result := make([]articleImage, 0, len(images))
	for i := range images {
		if !found[i] {
			continue
		}
		if len(result) > 0 {
			images[i].pos = max(images[i].pos, result[len(result)-1].pos)
		}
		result = append(result, images[i])
	}
	return result
}

// articleImageURL picks the real address out of an <img> tag. The site hides it
// in a scrambled data-* attribute next to a placeholder src, so any absolute
// http(s) attribute value (except text/fallback attributes) wins; a plain
// absolute src is the fallback. Relative placeholder sources yield "".
func articleImageURL(img *goquery.Selection) string {
	node := img.Get(0)
	if node == nil {
		return ""
	}
	for _, attr := range node.Attr {
		switch attr.Key {
		case "src", "alt", "title", "srcset":
			continue
		}
		if u, err := parseURL(attr.Val); err == nil {
			return u.String()
		}
	}
	if u, err := parseURL(img.AttrOr("src", "")); err == nil {
		return u.String()
	}
	return ""
}

// nodePath returns the child indexes leading from ancestor down to node.
func nodePath(ancestor, node *html.Node) ([]int, bool) {
	path := []int{}
	for n := node; n != nil; n = n.Parent {
		if n == ancestor {
			for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
				path[i], path[j] = path[j], path[i]
			}
			return path, true
		}
		index := 0
		for prev := n.PrevSibling; prev != nil; prev = prev.PrevSibling {
			index++
		}
		path = append(path, index)
	}
	return nil, false
}

// nodeAt follows a child-index path from root; a missing child yields nil.
func nodeAt(root *html.Node, path []int) *html.Node {
	node := root
	for _, index := range path {
		child := node.FirstChild
		for ; index > 0 && child != nil; index-- {
			child = child.NextSibling
		}
		if child == nil {
			return nil
		}
		node = child
	}
	return node
}

// removeFollowing drops every sibling after node at each level up to (and
// excluding) root, so the tree collapses to the part preceding node plus node
// itself. An img node has no text, so its own content never shifts a cut.
func removeFollowing(root, node *html.Node) {
	for n := node; n != nil && n != root; n = n.Parent {
		for next := n.NextSibling; next != nil; {
			after := next.NextSibling
			n.Parent.RemoveChild(next)
			next = after
		}
	}
}

// articleNear reads the previous/next article links from the footer navigation
// block. Malformed or off-site entries degrade to nil (navigation is auxiliary).
func articleNear(article *goquery.Selection, base *url.URL) (*ArticleLink, *ArticleLink) {
	near := article.Find("div.post-near").First()
	if near.Length() == 0 {
		return nil, nil
	}
	return articleNearLink(near.Find("span.prev a[href]").First(), base),
		articleNearLink(near.Find("span.next a[href]").First(), base)
}

func articleNearLink(a *goquery.Selection, base *url.URL) *ArticleLink {
	if a.Length() == 0 {
		return nil
	}
	address, err := contentURL(base, a.AttrOr("href", ""))
	if err != nil {
		return nil
	}
	u, err := url.Parse(address)
	if err != nil || u.Host != base.Host {
		return nil
	}
	title := contentSpace(a.AttrOr("title", ""))
	if title == "" {
		title = contentSpace(contentText(a))
	}
	if title == "" {
		return nil
	}
	return &ArticleLink{Title: title, URL: address}
}

// articleLinkURL resolves href into a same-site article address; any other
// link (off-site, category, self) yields an empty string.
func articleLinkURL(href string, base *url.URL) string {
	address, err := contentURL(base, href)
	if err != nil {
		return ""
	}
	u, err := url.Parse(address)
	if err != nil || u.Host != base.Host || !articleLinkPattern.MatchString(u.Path) {
		return ""
	}
	if strings.TrimSuffix(u.Path, "/") == strings.TrimSuffix(base.Path, "/") {
		return ""
	}
	return address
}

func articleMeta(scope *goquery.Selection, selector, label string) (string, error) {
	metas := scope.Find(selector)
	if metas.Length() > 1 {
		return "", fmt.Errorf("aacg article: ambiguous %s", label)
	}
	return strings.TrimSpace(metas.AttrOr("content", "")), nil
}

// articleStrip drops fallback players, ads and template furniture the site embeds in the article body.
func articleStrip(body *goquery.Selection) {
	body.Find("script, style, iframe, noscript, template, object, embed, " +
		"div.dplayer, .txt-apps, .btn-download, .copy-box, " +
		"blockquote, div.post-near, p.content-copyright, div.tags, section.hot-news-section, " +
		"div.content-tabs, table, " +
		`p:contains("热门吃瓜"), p:contains("版权声明"), ` +
		"[data-ad_slot_key], [data-ad_id], [class~=tjtagmanager], " +
		"[data-ad_type], [data-creative_id], img[id^=article-bottom-ads]").Remove()
}

func articleVideos(article *goquery.Selection, base *url.URL) ([]VideoLink, error) {
	videos := []VideoLink{}
	seen := make(map[string]bool)
	var parseErr error
	article.Find("div.dplayer").EachWithBreak(func(_ int, player *goquery.Selection) bool {
		raw := strings.TrimSpace(player.AttrOr("data-config", ""))
		if raw == "" {
			parseErr = fmt.Errorf("aacg article: player without config")
			return false
		}
		var config dplayerConfig
		if err := json.Unmarshal([]byte(raw), &config); err != nil {
			parseErr = fmt.Errorf("aacg article: invalid player config: %w", err)
			return false
		}
		// video 与 video_h265 是同一视频的两种编码，浏览器按能力择一播放；
		// 每个 player 汇总为一条 VideoLink（避免重复播放按钮），候选按
		// 「H.264 主源在前、H.265 备选在后」保序去重放在 Sources 里，
		// 播放端可逐个探测回退；跨 player 仍按首选地址去重。
		order := config.VideoH265
		if config.Video != nil {
			order = append([]dplayerSource{*config.Video}, config.VideoH265...)
		}
		candidates := make([]string, 0, len(order))
		primaryType := ""
		seenHere := make(map[string]bool, len(order))
		for _, source := range order {
			address, err := contentURL(base, source.URL)
			if err != nil {
				parseErr = fmt.Errorf("aacg article: invalid video link: %w", err)
				return false
			}
			if seenHere[address] {
				continue
			}
			seenHere[address] = true
			if len(candidates) == 0 {
				primaryType = strings.TrimSpace(source.Type)
			}
			candidates = append(candidates, address)
		}
		if len(candidates) == 0 || seen[candidates[0]] {
			return true
		}
		seen[candidates[0]] = true
		videos = append(videos, VideoLink{URL: candidates[0], Type: primaryType, Sources: candidates})
		return true
	})
	if parseErr != nil {
		return nil, parseErr
	}
	return videos, nil
}

type dplayerConfig struct {
	Video     *dplayerSource `json:"video"`
	VideoH265 dplayerSources `json:"video_h265"`
}

type dplayerSource struct {
	URL  string `json:"url"`
	Type string `json:"type"`
}

// dplayerSources accepts both the empty-array and single-object forms seen for video_h265.
type dplayerSources []dplayerSource

func (s *dplayerSources) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if trimmed[0] == '[' {
		return json.Unmarshal(trimmed, (*[]dplayerSource)(s))
	}
	var single dplayerSource
	if err := json.Unmarshal(trimmed, &single); err != nil {
		return err
	}
	*s = append(*s, single)
	return nil
}
