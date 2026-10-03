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

// Search fetches one page of site search results; siteURL is the site root ("/" or "/page/N/").
func (c *Client) Search(ctx context.Context, siteURL string, query string, page int) (ArticleList, error) {
	address, err := searchPageURL(siteURL, query, page)
	if err != nil {
		return ArticleList{}, err
	}
	budget := maxHops
	body, resp, err := c.get(ctx, address, "", &budget)
	if err != nil {
		return ArticleList{}, fmt.Errorf("aacg search: %w", err)
	}
	if challengePage(body) {
		return ArticleList{}, fmt.Errorf("aacg search: challenge page")
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return ArticleList{}, err
	}
	list, err := extractSearch(doc, resp.Request.URL)
	if err != nil {
		return ArticleList{}, err
	}
	if list.Pagination.Current != page {
		return ArticleList{}, fmt.Errorf("aacg search: requested page %d, got page %d", page, list.Pagination.Current)
	}
	return list, nil
}

// searchPageURL rebuilds /search/<term>/[<page>/] on the site root; the term stays within one path segment.
func searchPageURL(siteURL, query string, page int) (string, error) {
	if page < 1 {
		return "", fmt.Errorf("aacg search: invalid page %d", page)
	}
	term := contentSpace(query)
	if term == "" {
		return "", fmt.Errorf("aacg search: empty query")
	}
	if term == "." || term == ".." {
		return "", fmt.Errorf("aacg search: invalid query %q", term)
	}
	u, err := parseURL(siteURL)
	if err != nil {
		return "", fmt.Errorf("aacg search: invalid site URL: %w", err)
	}
	parts := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
	if len(parts) > 0 && !(len(parts) == 2 && parts[0] == "page" && allDigits(parts[1])) {
		return "", fmt.Errorf("aacg search: not a site URL: %s", u)
	}
	path := "/search/" + url.PathEscape(term) + "/"
	if page > 1 {
		path += strconv.Itoa(page) + "/"
	}
	return u.Scheme + "://" + u.Host + path, nil
}

func extractSearch(doc *goquery.Document, base *url.URL) (ArticleList, error) {
	container, err := contentOne(doc.Find("div#archive[role=main]"), "search archive")
	if err != nil {
		return ArticleList{}, err
	}
	result := ArticleList{Title: feedHeading(doc), Items: []ArticleSummary{}}
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
