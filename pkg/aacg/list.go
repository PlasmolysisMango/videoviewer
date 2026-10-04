package aacg

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

var (
	listCoverPattern = regexp.MustCompile(`loadBannerDirect\('([^']+)'`)
	// errSkipCard 标记不承载内容的卡片（促销角标卡片，或站点偶发渲染的
	// 无标题空壳卡片，如占位/已下架条目）——跳过而非让整页解析失败。
	errSkipCard = errors.New("aacg list: skipped card")
)

// CategoryList fetches one page of a category archive from the given category URL; page 1 is the newest entries.
func (c *Client) CategoryList(ctx context.Context, categoryURL string, page int) (ArticleList, error) {
	if _, err := categoryPageURL(categoryURL, page); err != nil {
		return ArticleList{}, err
	}
	return c.Feed(ctx, categoryURL, page)
}

// categoryPageURL rebuilds /category/<slug>/[<page>/] and accepts any page of the same archive.
func categoryPageURL(raw string, page int) (string, error) {
	if page < 1 {
		return "", fmt.Errorf("aacg list: invalid page %d", page)
	}
	u, err := parseURL(raw)
	if err != nil {
		return "", fmt.Errorf("aacg list: invalid category URL: %w", err)
	}
	parts := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
	if len(parts) < 2 || len(parts) > 3 || parts[0] != "category" || parts[1] == "." || parts[1] == ".." ||
		len(parts) == 3 && !allDigits(parts[2]) {
		return "", fmt.Errorf("aacg list: not a category URL: %s", u)
	}
	result := &url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/category/" + parts[1] + "/"}
	if page > 1 {
		result.Path += strconv.Itoa(page) + "/"
	}
	return result.String(), nil
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// listTitle prefers the breadcrumb link to the category itself so page 2+ keeps the category name.
func listTitle(archive *goquery.Selection, base *url.URL) (string, error) {
	wrap, err := contentOne(archive.ChildrenFiltered("div.nav-breadcrumb-wrap"), "breadcrumb")
	if err != nil {
		return "", err
	}
	title := ""
	wrap.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		address, err := contentURL(base, a.AttrOr("href", ""))
		if err != nil {
			return
		}
		parsed, err := parseURL(address)
		if err != nil || !strings.HasPrefix(parsed.Path, "/category/") ||
			parsed.Scheme != base.Scheme || parsed.Hostname() != base.Hostname() || contentPort(parsed) != contentPort(base) {
			return
		}
		if text := contentSpace(contentText(a)); text != "" {
			title = text
		}
	})
	if title == "" {
		name, err := contentOne(wrap.ChildrenFiltered("span.name"), "breadcrumb name")
		if err != nil {
			return "", err
		}
		title = contentSpace(contentText(name))
	}
	if title == "" {
		return "", fmt.Errorf("aacg list: empty category title")
	}
	return title, nil
}

func listCards(archive *goquery.Selection, base *url.URL) ([]ArticleSummary, error) {
	items := []ArticleSummary{}
	seen := make(map[string]bool)
	var parseErr error
	archive.Find("article[itemtype$='/BlogPosting']").EachWithBreak(func(_ int, card *goquery.Selection) bool {
		item, err := listCard(card, base)
		if errors.Is(err, errSkipCard) {
			return true
		}
		if err != nil {
			parseErr = err
			return false
		}
		if !seen[item.URL] {
			items = append(items, item)
			seen[item.URL] = true
		}
		return true
	})
	if parseErr != nil {
		return nil, parseErr
	}
	return items, nil
}

func listCard(card *goquery.Selection, base *url.URL) (ArticleSummary, error) {
	var item ArticleSummary
	link, err := contentOne(card.Find(`meta[itemprop~="mainEntityOfPage"]`), "card link")
	if err != nil {
		return item, err
	}
	item.URL, err = contentURL(base, link.AttrOr("content", ""))
	if err != nil {
		return item, err
	}
	headlines := card.Find(`h2[itemprop="headline"]`)
	if headlines.Length() == 0 {
		return item, errSkipCard // 无标题空壳卡片
	}
	headline, err := contentOne(headlines, "card headline")
	if err != nil {
		return item, err
	}
	clean := headline.Clone()
	clean.Find("div.wrap").Remove()
	item.Title = contentSpace(contentText(clean))
	if item.Title == "" {
		if headline.Find("div.wrap").Length() > 0 {
			return item, errSkipCard
		}
		return item, fmt.Errorf("aacg list: empty card headline")
	}
	dates := card.Find(`span[itemprop="datePublished"]`)
	if dates.Length() > 1 {
		return item, fmt.Errorf("aacg list: ambiguous card date")
	}
	item.PublishedAt = strings.TrimSpace(dates.AttrOr("content", ""))
	card.Find("script").EachWithBreak(func(_ int, script *goquery.Selection) bool {
		match := listCoverPattern.FindStringSubmatch(script.Text())
		if match == nil {
			return true
		}
		item.CoverURL, err = contentURL(base, match[1])
		return false
	})
	if err != nil {
		return item, err
	}
	return item, nil
}

func listPagination(archive *goquery.Selection, base *url.URL) (Pagination, error) {
	result := Pagination{Current: 1}
	info := archive.Find("span.page-info")
	if info.Length() > 1 {
		return Pagination{}, fmt.Errorf("aacg list: ambiguous page info")
	}
	if info.Length() == 1 {
		current, total, err := listPageInfo(info.Text())
		if err != nil {
			return Pagination{}, err
		}
		result.Current, result.Total = current, total
	}
	navigator := archive.Find("ul.page-navigator")
	if navigator.Length() == 0 {
		return result, nil
	}
	navigator, err := contentOne(navigator, "page navigator")
	if err != nil {
		return Pagination{}, err
	}
	active, err := contentOne(navigator.Find("li.active"), "active page")
	if err != nil {
		return Pagination{}, err
	}
	result.Current, err = strconv.Atoi(contentSpace(active.Text()))
	if err != nil || result.Current < 1 {
		return Pagination{}, fmt.Errorf("aacg list: invalid current page")
	}
	for _, direction := range []struct {
		class string
		value *string
	}{{"next", &result.NextURL}, {"prev", &result.PreviousURL}} {
		link := navigator.Find("li." + direction.class + " a[href]")
		if link.Length() == 0 {
			continue
		}
		link, err = contentOne(link, direction.class+" page link")
		if err != nil {
			return Pagination{}, err
		}
		*direction.value, err = contentURL(base, link.AttrOr("href", ""))
		if err != nil {
			return Pagination{}, err
		}
	}
	return result, nil
}

func listPageInfo(text string) (int, int, error) {
	current, total, ok := strings.Cut(contentSpace(text), "/")
	if !ok {
		return 0, 0, fmt.Errorf("aacg list: invalid page info")
	}
	currentN, err := strconv.Atoi(strings.TrimSpace(current))
	if err != nil || currentN < 1 {
		return 0, 0, fmt.Errorf("aacg list: invalid page info")
	}
	totalN, err := strconv.Atoi(strings.TrimSpace(total))
	if err != nil || totalN < currentN {
		return 0, 0, fmt.Errorf("aacg list: invalid page info")
	}
	return currentN, totalN, nil
}
