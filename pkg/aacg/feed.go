package aacg

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Feed fetches one page of the site feed; feedURL selects the stream:
// "/" or "/page/N/" is the homepage feed, "/category/<slug>/[N/]" a category feed.
// page is the requested page number, 1-based.
func (c *Client) Feed(ctx context.Context, feedURL string, page int) (ArticleList, error) {
	address, err := feedPageURL(feedURL, page)
	if err != nil {
		return ArticleList{}, err
	}
	budget := maxHops
	body, resp, err := c.get(ctx, address, "", &budget)
	if err != nil {
		return ArticleList{}, fmt.Errorf("aacg feed: %w", err)
	}
	if challengePage(body) {
		return ArticleList{}, fmt.Errorf("aacg feed: challenge page")
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return ArticleList{}, err
	}
	list, err := extractFeed(doc, resp.Request.URL)
	if err != nil {
		return ArticleList{}, err
	}
	if list.Pagination.Current != page {
		return ArticleList{}, fmt.Errorf("aacg feed: requested page %d, got page %d", page, list.Pagination.Current)
	}
	return list, nil
}

// feedPageURL rebuilds the clean page URL for a homepage or category stream.
func feedPageURL(raw string, page int) (string, error) {
	if page < 1 {
		return "", fmt.Errorf("aacg feed: invalid page %d", page)
	}
	u, err := parseURL(raw)
	if err != nil {
		return "", fmt.Errorf("aacg feed: invalid feed URL: %w", err)
	}
	parts := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
	if len(parts) > 0 && parts[0] == "category" {
		return categoryPageURL(raw, page)
	}
	if len(parts) == 0 || len(parts) == 2 && parts[0] == "page" && allDigits(parts[1]) {
		result := &url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/"}
		if page > 1 {
			result.Path = "/page/" + strconv.Itoa(page) + "/"
		}
		return result.String(), nil
	}
	return "", fmt.Errorf("aacg feed: not a feed URL: %s", u)
}

// feedHeading reads the page's single blog-title heading; the heading is decorative,
// so a missing or ambiguous one yields "".
func feedHeading(doc *goquery.Document) string {
	if titles := doc.Find("h1.blog-title"); titles.Length() == 1 {
		return contentSpace(contentText(titles))
	}
	return ""
}

func extractFeed(doc *goquery.Document, base *url.URL) (ArticleList, error) {
	container, err := contentOne(doc.Find("div#index[role=main], div#archive[role=main]"), "feed container")
	if err != nil {
		return ArticleList{}, err
	}
	result := ArticleList{Items: []ArticleSummary{}}
	if container.AttrOr("id", "") == "index" {
		result.Title = feedHeading(doc)
	} else {
		result.Title, err = listTitle(container, base)
		if err != nil {
			return ArticleList{}, err
		}
	}
	result.Items, err = listCards(container, base)
	if err != nil {
		return ArticleList{}, err
	}
	result.Pagination, err = listPagination(container, base)
	if err != nil {
		return ArticleList{}, err
	}
	return result, nil
}
