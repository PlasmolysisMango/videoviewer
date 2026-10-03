package aacg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
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
	result := ArticleDetail{Videos: []VideoLink{}}
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
	articleStrip(body)
	result.Content = contentText(body)
	if result.Content == "" {
		return ArticleDetail{}, fmt.Errorf("aacg article: empty article content")
	}
	return result, nil
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
		"[data-ad_slot_key], [data-ad_id], [class~=tjtagmanager]").Remove()
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
		sources := config.VideoH265
		if config.Video != nil {
			sources = append([]dplayerSource{*config.Video}, sources...)
		}
		for _, source := range sources {
			address, err := contentURL(base, source.URL)
			if err != nil {
				parseErr = fmt.Errorf("aacg article: invalid video link: %w", err)
				return false
			}
			if !seen[address] {
				videos = append(videos, VideoLink{URL: address, Type: strings.TrimSpace(source.Type)})
				seen[address] = true
			}
		}
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
