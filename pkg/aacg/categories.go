package aacg

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Categories fetches the target homepage and returns its navigation categories in page order.
func (c *Client) Categories(ctx context.Context, target Target) ([]Category, error) {
	budget := maxHops
	body, resp, err := c.get(ctx, target.URL, "", &budget)
	if err != nil {
		return nil, fmt.Errorf("aacg categories: %w", err)
	}
	if challengePage(body) {
		return nil, fmt.Errorf("aacg categories: challenge page")
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	return extractCategories(doc, resp.Request.URL)
}

func extractCategories(doc *goquery.Document, base *url.URL) ([]Category, error) {
	categories := []Category{}
	seen := make(map[string]bool)
	var parseErr error
	doc.Find("nav#navbar a[href], nav#site-navigation a[href]").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		href := strings.TrimSpace(a.AttrOr("href", ""))
		if !strings.Contains(href, "/category/") {
			return true
		}
		address, err := contentURL(base, href)
		if err != nil {
			parseErr = err
			return false
		}
		parsed, err := url.Parse(address)
		if err != nil {
			parseErr = err
			return false
		}
		slug, ok := strings.CutPrefix(parsed.Path, "/category/")
		if !ok || strings.Trim(slug, "/") == "" ||
			parsed.Scheme != base.Scheme || parsed.Hostname() != base.Hostname() || contentPort(parsed) != contentPort(base) {
			return true
		}
		if seen[address] {
			return true
		}
		name := contentSpace(contentText(a))
		if name == "" {
			parseErr = fmt.Errorf("aacg categories: empty category name")
			return false
		}
		categories = append(categories, Category{Name: name, URL: address})
		seen[address] = true
		return true
	})
	if parseErr != nil {
		return nil, parseErr
	}
	if len(categories) == 0 {
		return nil, fmt.Errorf("aacg categories: no navigation categories found")
	}
	return categories, nil
}
