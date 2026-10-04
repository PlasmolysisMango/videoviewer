package aacg

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

type Category struct {
	Name string
	URL  string
}

type SearchForm struct {
	Action    string
	QueryName string
}

type HomePage struct {
	Categories      []Category
	Recommendations []ArticleSummary
	Search          SearchForm
}

type ArticleSummary struct {
	Title       string
	URL         string
	Summary     string
	CoverURL    string
	PublishedAt string
}

type Pagination struct {
	Current     int
	Total       int // page count; 0 when the page does not report it
	NextURL     string
	PreviousURL string
}

type ArticleList struct {
	Title      string
	Items      []ArticleSummary
	Pagination Pagination
}

type ArticleDetail struct {
	ArticleSummary
	Content      string
	ContentParts []ContentPart
	Videos       []VideoLink
	Previous     *ArticleLink // footer navigation, never inline content links
	Next         *ArticleLink
}

// ContentPart is one run of article text; URL is set for links to other
// articles, ImageURL for body images (Text is empty then).
type ContentPart struct {
	Text     string
	URL      string
	ImageURL string
}

// ArticleLink is one footer navigation entry (previous/next article).
type ArticleLink struct {
	Title string
	URL   string
}

type VideoLink struct {
	URL       string
	Type      string
	PosterURL string
}

// ParseHome accepts synthetic data-role markup; it never requests a website or its resources.
func ParseHome(r io.Reader, baseURL string) (HomePage, error) {
	var result HomePage
	doc, base, err := contentDocument(r, baseURL)
	if err != nil {
		return result, err
	}
	nav, err := contentOne(doc.Find("nav[data-role=categories]"), "categories")
	if err != nil {
		return result, err
	}
	recommendations, err := contentOne(doc.Find("section[data-role=recommendations]"), "recommendations")
	if err != nil {
		return result, err
	}
	form, err := contentOne(doc.Find("form[role=search]"), "search form")
	if err != nil {
		return result, err
	}
	result.Categories = []Category{}
	seen := make(map[string]bool)
	nav.Find("a").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		name := contentSpace(contentText(a))
		if name == "" {
			err = fmt.Errorf("aacg content: empty category name")
			return false
		}
		var address string
		address, err = contentURL(base, a.AttrOr("href", ""))
		if err != nil {
			return false
		}
		if !seen[address] {
			result.Categories = append(result.Categories, Category{Name: name, URL: address})
			seen[address] = true
		}
		return true
	})
	if err != nil {
		return HomePage{}, err
	}
	result.Recommendations, err = contentCards(recommendations, base)
	if err != nil {
		return HomePage{}, err
	}
	result.Search, err = contentSearch(form, base)
	if err != nil {
		return HomePage{}, err
	}
	return result, nil
}

// ParseList shares the same synthetic markup for category listings and search results.
func ParseList(r io.Reader, baseURL string) (ArticleList, error) {
	var result ArticleList
	doc, base, err := contentDocument(r, baseURL)
	if err != nil {
		return result, err
	}
	list, err := contentOne(doc.Find("main[data-role=article-list]"), "article list")
	if err != nil {
		return result, err
	}
	result.Title, err = contentTitle(list)
	if err != nil {
		return ArticleList{}, err
	}
	result.Items, err = contentCards(list, base)
	if err != nil {
		return ArticleList{}, err
	}
	result.Pagination, err = contentPagination(list, base)
	if err != nil {
		return ArticleList{}, err
	}
	return result, nil
}

// ParseArticle returns plain text and HTML5 source URLs, without executing or fetching anything.
func ParseArticle(r io.Reader, baseURL string) (ArticleDetail, error) {
	var result ArticleDetail
	doc, base, err := contentDocument(r, baseURL)
	if err != nil {
		return result, err
	}
	article, err := contentOne(doc.Find("article[data-role=article]"), "article detail")
	if err != nil {
		return result, err
	}
	body, err := contentOne(article.Find("[data-role=content]"), "article content")
	if err != nil {
		return result, err
	}
	result.ArticleSummary, err = contentSummary(article, base)
	if err != nil {
		return ArticleDetail{}, err
	}
	result.URL = base.String()
	result.Videos, err = contentVideos(body, base)
	if err != nil {
		return ArticleDetail{}, err
	}
	result.Content = contentText(body)
	return result, nil
}

func BuildSearchURL(form SearchForm, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("aacg content: empty search query")
	}
	if !contentQueryName(form.QueryName) {
		return "", fmt.Errorf("aacg content: invalid search query field")
	}
	u, err := parseURL(form.Action)
	if err != nil {
		return "", fmt.Errorf("aacg content: invalid search action: %w", err)
	}
	params, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", fmt.Errorf("aacg content: invalid search parameters: %w", err)
	}
	params.Set(form.QueryName, query)
	u.RawQuery = params.Encode()
	return u.String(), nil
}

func contentDocument(r io.Reader, baseURL string) (*goquery.Document, *url.URL, error) {
	base, err := parseURL(baseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("aacg content: invalid base URL: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(r, maxBodyBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("aacg content: read HTML: %w", err)
	}
	if len(data) > maxBodyBytes {
		return nil, nil, fmt.Errorf("aacg content: HTML exceeds %d bytes", maxBodyBytes)
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(data))
	if err != nil {
		return nil, nil, err
	}
	doc.Find("script, style, iframe, noscript, template, object, embed, [hidden], [data-role=ad]").Remove()
	doc.Find("[aria-hidden], [style]").Each(func(_ int, s *goquery.Selection) {
		if strings.EqualFold(strings.TrimSpace(s.AttrOr("aria-hidden", "")), "true") || contentHiddenStyle(s.AttrOr("style", "")) {
			s.Remove()
		}
	})
	return doc, base, nil
}

func contentHiddenStyle(style string) bool {
	values := make(map[string]string)
	priorities := make(map[string]bool)
	for _, declaration := range strings.Split(strings.ToLower(style), ";") {
		name, value, ok := strings.Cut(declaration, ":")
		name = strings.TrimSpace(name)
		if !ok || name != "display" && name != "visibility" {
			continue
		}
		value, priority, important := strings.Cut(value, "!")
		value = strings.TrimSpace(value)
		if value == "" || important && strings.TrimSpace(priority) != "important" || priorities[name] && !important {
			continue
		}
		values[name], priorities[name] = value, important
	}
	return values["display"] == "none" || values["visibility"] == "hidden" || values["visibility"] == "collapse"
}

func contentOne(s *goquery.Selection, label string) (*goquery.Selection, error) {
	if s.Length() != 1 {
		return nil, fmt.Errorf("aacg content: expected one %s, found %d", label, s.Length())
	}
	return s, nil
}

func contentSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func contentTitle(s *goquery.Selection) (string, error) {
	title, err := contentOne(s.ChildrenFiltered("[data-role=title]"), "title")
	if err != nil {
		return "", err
	}
	text := contentSpace(contentText(title))
	if text == "" {
		return "", fmt.Errorf("aacg content: empty title")
	}
	return text, nil
}

func contentURL(base *url.URL, reference string) (string, error) {
	if strings.Contains(reference, "\\") || strings.IndexFunc(reference, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("aacg content: invalid URL characters")
	}
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return "", fmt.Errorf("aacg content: empty URL")
	}
	ref, err := url.Parse(reference)
	if err != nil {
		return "", fmt.Errorf("aacg content: invalid URL")
	}
	u, err := parseURL(base.ResolveReference(ref).String())
	if err != nil {
		return "", fmt.Errorf("aacg content: invalid link: %w", err)
	}
	return u.String(), nil
}

func contentSummary(s *goquery.Selection, base *url.URL) (ArticleSummary, error) {
	var result ArticleSummary
	var err error
	result.Title, err = contentTitle(s)
	if err != nil {
		return result, err
	}
	summary := s.ChildrenFiltered("[data-role=summary]")
	published := s.ChildrenFiltered("time[datetime]")
	image := s.ChildrenFiltered("img[data-role=cover]")
	if summary.Length() > 1 || published.Length() > 1 || image.Length() > 1 {
		return ArticleSummary{}, fmt.Errorf("aacg content: ambiguous article metadata")
	}
	result.Summary = contentSpace(contentText(summary))
	result.PublishedAt = strings.TrimSpace(published.AttrOr("datetime", ""))
	if image.Length() > 0 {
		src := image.AttrOr("data-src", "")
		if src == "" {
			src = image.AttrOr("src", "")
		}
		result.CoverURL, err = contentURL(base, src)
		if err != nil {
			return ArticleSummary{}, err
		}
	}
	return result, nil
}

func contentCards(container *goquery.Selection, base *url.URL) ([]ArticleSummary, error) {
	items := []ArticleSummary{}
	seen := make(map[string]bool)
	var parseErr error
	container.Find("article[data-role=card]").EachWithBreak(func(_ int, card *goquery.Selection) bool {
		item, err := contentSummary(card, base)
		if err != nil {
			parseErr = err
			return false
		}
		link := card.ChildrenFiltered("[data-role=title]")
		if !link.Is("a[href]") {
			link = link.Find("a[href]")
		}
		link, err = contentOne(link, "card title link")
		if err != nil {
			parseErr = err
			return false
		}
		item.URL, err = contentURL(base, link.AttrOr("href", ""))
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

func contentSearch(form *goquery.Selection, base *url.URL) (SearchForm, error) {
	var result SearchForm
	method := strings.ToLower(strings.TrimSpace(form.AttrOr("method", "get")))
	if method != "" && method != "get" {
		return result, fmt.Errorf("aacg content: only GET search forms are supported")
	}
	field, err := contentOne(form.Find("input[type=search]:not([disabled])"), "search input")
	if err != nil {
		return result, err
	}
	result.QueryName = field.AttrOr("name", "")
	if !contentQueryName(result.QueryName) {
		return SearchForm{}, fmt.Errorf("aacg content: invalid search input name")
	}
	action := form.AttrOr("action", "")
	if action == "" {
		action = base.String()
	}
	result.Action, err = contentURL(base, action)
	if err != nil {
		return SearchForm{}, err
	}
	u, err := parseURL(result.Action)
	if err != nil {
		return SearchForm{}, err
	}
	if u.Scheme != base.Scheme || u.Hostname() != base.Hostname() || contentPort(u) != contentPort(base) {
		return SearchForm{}, fmt.Errorf("aacg content: search action must be same-origin")
	}
	return result, nil
}

func contentQueryName(name string) bool {
	return name != "" && strings.IndexFunc(name, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

func contentPort(u *url.URL) int {
	if port := u.Port(); port != "" {
		n, _ := strconv.Atoi(port)
		return n
	}
	if u.Scheme == "https" {
		return 443
	}
	return 80
}

func contentPagination(list *goquery.Selection, base *url.URL) (Pagination, error) {
	result := Pagination{Current: 1}
	pages := list.Find("nav[data-role=pagination]")
	if pages.Length() == 0 {
		return result, nil
	}
	pages, err := contentOne(pages, "pagination")
	if err != nil {
		return Pagination{}, err
	}
	current, err := contentOne(pages.Find("[aria-current=page]"), "current page")
	if err != nil {
		return Pagination{}, err
	}
	result.Current, err = strconv.Atoi(strings.TrimSpace(current.Text()))
	if err != nil || result.Current < 1 {
		return Pagination{}, fmt.Errorf("aacg content: invalid current page")
	}
	for _, direction := range []struct {
		rel   string
		value *string
	}{{"next", &result.NextURL}, {"prev", &result.PreviousURL}} {
		link := pages.Find("a[rel~=" + direction.rel + "]")
		if link.Length() == 0 {
			continue
		}
		link, err = contentOne(link, direction.rel+" page")
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

func contentVideos(body *goquery.Selection, base *url.URL) ([]VideoLink, error) {
	videos := []VideoLink{}
	seen := make(map[string]bool)
	var parseErr error
	body.Find("video").EachWithBreak(func(_ int, video *goquery.Selection) bool {
		poster := video.AttrOr("poster", "")
		var err error
		if poster != "" {
			poster, err = contentURL(base, poster)
			if err != nil {
				parseErr = err
				return false
			}
		}
		sources := video.AddSelection(video.ChildrenFiltered("source[src]"))
		sources.EachWithBreak(func(_ int, source *goquery.Selection) bool {
			src := source.AttrOr("src", "")
			if src == "" {
				return true
			}
			address, err := contentURL(base, src)
			if err != nil {
				parseErr = err
				return false
			}
			if !seen[address] {
				videos = append(videos, VideoLink{URL: address, Type: strings.TrimSpace(source.AttrOr("type", "")), PosterURL: poster})
				seen[address] = true
			}
			return true
		})
		return parseErr == nil
	})
	if parseErr != nil {
		return nil, parseErr
	}
	return videos, nil
}

func contentText(body *goquery.Selection) string {
	var text strings.Builder
	pendingBreaks := 0
	whitespace := strings.NewReplacer("\r", " ", "\n", " ")
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			if pendingBreaks > 0 && strings.TrimSpace(n.Data) == "" {
				return
			}
			text.WriteString(strings.Repeat("\n", pendingBreaks))
			pendingBreaks = 0
			text.WriteString(whitespace.Replace(n.Data))
			return
		}
		if n.Type != html.ElementNode {
			return
		}
		if n.Data == "video" || n.Data == "source" {
			return
		}
		if n.Data == "br" {
			pendingBreaks = min(2, pendingBreaks+1)
			return
		}
		breaks := 0
		switch n.Data {
		case "p", "h1", "h2", "h3", "h4", "h5", "h6", "div", "section", "blockquote", "pre", "ul", "ol":
			breaks = 2
		case "li":
			breaks = 1
		}
		pendingBreaks = max(pendingBreaks, breaks)
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		pendingBreaks = max(pendingBreaks, breaks)
	}
	for _, node := range body.Nodes {
		walk(node)
	}
	lines := []string{}
	for _, line := range strings.Split(text.String(), "\n") {
		line = contentSpace(line)
		if line != "" || len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, line)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
