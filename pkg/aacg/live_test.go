//go:build live

package aacg

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLiveDiscoverAndCheck(t *testing.T) {
	c, err := New(Options{
		EntryURL: os.Getenv("AACG_ENTRY_URL"),
		Proxy:    os.Getenv("AACG_PROXY"),
		Timeout:  20 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	discovery, err := c.Discover(ctx)
	if err != nil {
		t.Fatalf("discovery failed (entry=%s landing=%s): %v", discovery.EntryURL, discovery.LandingURL, err)
	}
	t.Logf("entry=%s landing=%s discovered=%d", discovery.EntryURL, discovery.LandingURL, len(discovery.Targets))
	if len(discovery.Targets) == 0 {
		t.Fatal("no target routes discovered")
	}
	type outcome struct {
		health Health
		err    error
	}
	results := make([]outcome, len(discovery.Targets))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			for i := range jobs {
				results[i].health, results[i].err = c.Check(ctx, discovery.Targets[i])
			}
		})
	}
	for i := range discovery.Targets {
		jobs <- i
	}
	close(jobs)
	workers.Wait()
	usable := 0
	for _, result := range results {
		h := result.health
		t.Logf("[%s] %s url=%s final=%s status=%d type=%q title=%q bytes=%d elapsed=%s articles=%d recognized=%t usable=%t error=%v",
			h.Target.Kind, h.Target.Name, h.Target.URL, h.FinalURL, h.StatusCode, h.ContentType, h.Title,
			h.Bytes, h.Elapsed.Round(time.Millisecond), h.ArticleCount, h.Recognized, h.Usable, result.err)
		if result.err == nil && h.Usable {
			usable++
		}
	}
	t.Logf("summary: discovered=%d checked=%d usable=%d failed=%d", len(discovery.Targets), len(results), usable, len(results)-usable)
	if err := ctx.Err(); err != nil {
		t.Fatalf("live run did not finish within its deadline: %v", err)
	}
	if usable == 0 {
		t.Fatal("no target homepage was actually accessible and recognized")
	}
}

func TestLiveCategories(t *testing.T) {
	c, err := New(Options{
		EntryURL: os.Getenv("AACG_ENTRY_URL"),
		Proxy:    os.Getenv("AACG_PROXY"),
		Timeout:  20 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	discovery, err := c.Discover(ctx)
	if err != nil {
		t.Fatalf("discovery failed (entry=%s landing=%s): %v", discovery.EntryURL, discovery.LandingURL, err)
	}
	t.Logf("entry=%s landing=%s discovered=%d", discovery.EntryURL, discovery.LandingURL, len(discovery.Targets))
	var categories []Category
	var source Target
	var lastErr error
	for _, target := range discovery.Targets {
		categories, lastErr = c.Categories(ctx, target)
		if lastErr == nil {
			source = target
			break
		}
		t.Logf("[%s] %s url=%s categories failed: %v", target.Kind, target.Name, target.URL, lastErr)
	}
	if lastErr != nil {
		t.Fatalf("no target yielded categories: %v", lastErr)
	}
	t.Logf("target=%s categories=%d", source.URL, len(categories))
	for _, category := range categories {
		t.Logf("category name=%q url=%s", category.Name, category.URL)
	}
	budget := maxHops
	body, resp, err := c.get(ctx, categories[0].URL, "", &budget)
	if err != nil {
		t.Fatalf("first category page %s failed: %v", categories[0].URL, err)
	}
	t.Logf("first category page: url=%s status=%d bytes=%d", categories[0].URL, resp.StatusCode, len(body))
	if err := ctx.Err(); err != nil {
		t.Fatalf("live run did not finish within its deadline: %v", err)
	}
}

func TestLiveCategoryList(t *testing.T) {
	c, err := New(Options{
		EntryURL: os.Getenv("AACG_ENTRY_URL"),
		Proxy:    os.Getenv("AACG_PROXY"),
		Timeout:  20 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	discovery, err := c.Discover(ctx)
	if err != nil {
		t.Fatalf("discovery failed (entry=%s landing=%s): %v", discovery.EntryURL, discovery.LandingURL, err)
	}
	var categories []Category
	var source Target
	var lastErr error
	for _, target := range discovery.Targets {
		categories, lastErr = c.Categories(ctx, target)
		if lastErr == nil {
			source = target
			break
		}
		t.Logf("[%s] %s url=%s categories failed: %v", target.Kind, target.Name, target.URL, lastErr)
	}
	if lastErr != nil {
		t.Fatalf("no target yielded categories: %v", lastErr)
	}
	category := categories[0]
	t.Logf("target=%s category=%q url=%s", source.URL, category.Name, category.URL)
	first, err := c.CategoryList(ctx, category.URL, 1)
	if err != nil {
		t.Fatalf("category list page 1 failed: %v", err)
	}
	if first.Title != category.Name || first.Pagination.Current != 1 || first.Pagination.Total < 2 ||
		first.Pagination.NextURL == "" || first.Pagination.PreviousURL != "" || len(first.Items) == 0 {
		t.Fatalf("unexpected page 1: %+v", first)
	}
	assertUniqueItems(t, first.Items)
	if first.Items[0].CoverURL == "" || first.Items[0].PublishedAt == "" {
		t.Fatalf("lost page 1 card metadata: %+v", first.Items[0])
	}
	t.Logf("page 1: title=%q items=%d total=%d next=%s", first.Title, len(first.Items), first.Pagination.Total, first.Pagination.NextURL)
	for i, item := range first.Items {
		if i == 3 {
			break
		}
		t.Logf("page 1 item %d: title=%q url=%s cover=%q date=%s", i, item.Title, item.URL, item.CoverURL, item.PublishedAt)
	}
	second, err := c.CategoryList(ctx, category.URL, 2)
	if err != nil {
		t.Fatalf("category list page 2 failed: %v", err)
	}
	if second.Pagination.Current != 2 || second.Pagination.Total != first.Pagination.Total ||
		second.Pagination.PreviousURL == "" || len(second.Items) == 0 || second.Items[0].URL == first.Items[0].URL {
		t.Fatalf("unexpected page 2: %+v", second)
	}
	assertUniqueItems(t, second.Items)
	t.Logf("page 2: title=%q items=%d total=%d next=%s prev=%s", second.Title, len(second.Items), second.Pagination.Total, second.Pagination.NextURL, second.Pagination.PreviousURL)
	for i, item := range second.Items {
		if i == 3 {
			break
		}
		t.Logf("page 2 item %d: title=%q url=%s cover=%q date=%s", i, item.Title, item.URL, item.CoverURL, item.PublishedAt)
	}
	budget := maxHops
	body, resp, err := c.get(ctx, second.Items[0].URL, "", &budget)
	if err != nil {
		t.Fatalf("first page-2 item %s failed: %v", second.Items[0].URL, err)
	}
	t.Logf("first page-2 item: url=%s status=%d bytes=%d", second.Items[0].URL, resp.StatusCode, len(body))
	if err := ctx.Err(); err != nil {
		t.Fatalf("live run did not finish within its deadline: %v", err)
	}
}

func TestLiveArticle(t *testing.T) {
	c, err := New(Options{
		EntryURL: os.Getenv("AACG_ENTRY_URL"),
		Proxy:    os.Getenv("AACG_PROXY"),
		Timeout:  20 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	discovery, err := c.Discover(ctx)
	if err != nil {
		t.Fatalf("discovery failed (entry=%s landing=%s): %v", discovery.EntryURL, discovery.LandingURL, err)
	}
	var categories []Category
	var source Target
	var lastErr error
	for _, target := range discovery.Targets {
		categories, lastErr = c.Categories(ctx, target)
		if lastErr == nil {
			source = target
			break
		}
		t.Logf("[%s] %s url=%s categories failed: %v", target.Kind, target.Name, target.URL, lastErr)
	}
	if lastErr != nil {
		t.Fatalf("no target yielded categories: %v", lastErr)
	}
	t.Logf("target=%s categories=%d", source.URL, len(categories))
	checked := 0
	for i, category := range categories {
		if i >= 5 || checked >= 2 {
			break
		}
		list, err := c.CategoryList(ctx, category.URL, 1)
		if err != nil || len(list.Items) == 0 {
			t.Logf("category %q page 1 unusable: items=%d err=%v", category.Name, len(list.Items), err)
			continue
		}
		item := list.Items[0]
		detail, err := c.Article(ctx, item.URL)
		if err != nil {
			t.Errorf("article %s failed: %v", item.URL, err)
			continue
		}
		if detail.Title != item.Title {
			t.Errorf("article %s title mismatch: list=%q detail=%q", item.URL, item.Title, detail.Title)
			continue
		}
		if detail.Content == "" || detail.PublishedAt == "" || detail.CoverURL == "" || len(detail.Videos) == 0 {
			t.Errorf("article %s incomplete: %+v", item.URL, detail)
			continue
		}
		checked++
		head := detail.Content
		if len(head) > 80 {
			head = head[:80]
		}
		t.Logf("category=%q article=%q url=%s content=%d videos=%d summary=%t cover-match=%t head=%q",
			category.Name, detail.Title, detail.URL, len(detail.Content), len(detail.Videos),
			detail.Summary != "", detail.CoverURL == item.CoverURL, head)
		for _, video := range detail.Videos {
			parsed, err := parseURL(video.URL)
			host := ""
			if err == nil {
				host = parsed.Host
			}
			t.Logf("video type=%q host=%s", video.Type, host)
		}
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("live run did not finish within its deadline: %v", err)
	}
	if checked == 0 {
		t.Fatal("no article detail could be fetched and validated")
	}
}

func TestLiveFeed(t *testing.T) {
	c, err := New(Options{
		EntryURL: os.Getenv("AACG_ENTRY_URL"),
		Proxy:    os.Getenv("AACG_PROXY"),
		Timeout:  20 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	discovery, err := c.Discover(ctx)
	if err != nil {
		t.Fatalf("discovery failed (entry=%s landing=%s): %v", discovery.EntryURL, discovery.LandingURL, err)
	}
	var target Target
	var first ArticleList
	var sourceErr error
	for _, candidate := range discovery.Targets {
		health, err := c.Check(ctx, candidate)
		if err != nil || !health.Usable {
			t.Logf("[%s] %s url=%s not usable: %v", candidate.Kind, candidate.Name, candidate.URL, err)
			continue
		}
		first, sourceErr = c.Feed(ctx, candidate.URL, 1)
		if sourceErr != nil || len(first.Items) == 0 {
			t.Logf("[%s] %s url=%s feed failed: items=%d err=%v", candidate.Kind, candidate.Name, candidate.URL, len(first.Items), sourceErr)
			continue
		}
		target = candidate
		break
	}
	if target.URL == "" {
		t.Fatalf("no target yielded a usable homepage feed: %v", sourceErr)
	}
	if first.Pagination.Current != 1 || first.Pagination.Total < 2 || first.Pagination.NextURL == "" || first.Pagination.PreviousURL != "" {
		t.Fatalf("unexpected homepage page 1: %+v", first.Pagination)
	}
	assertUniqueItems(t, first.Items)
	if first.Items[0].Title == "" || first.Items[0].CoverURL == "" || first.Items[0].PublishedAt == "" {
		t.Fatalf("lost homepage card metadata: %+v", first.Items[0])
	}
	t.Logf("homepage feed: target=%s title=%q items=%d total=%d next=%s", target.URL, first.Title, len(first.Items), first.Pagination.Total, first.Pagination.NextURL)
	logFeedItems(t, "homepage page 1", first.Items)
	second, err := c.Feed(ctx, target.URL, 2)
	if err != nil {
		t.Fatalf("homepage page 2 failed: %v", err)
	}
	if second.Pagination.Current != 2 || second.Pagination.Total != first.Pagination.Total ||
		second.Pagination.PreviousURL == "" || len(second.Items) == 0 || second.Items[0].URL == first.Items[0].URL {
		t.Fatalf("unexpected homepage page 2: %+v", second)
	}
	assertUniqueItems(t, second.Items)
	t.Logf("homepage page 2: title=%q items=%d total=%d prev=%s next=%s", second.Title, len(second.Items), second.Pagination.Total, second.Pagination.PreviousURL, second.Pagination.NextURL)
	logFeedItems(t, "homepage page 2", second.Items)

	categories, err := c.Categories(ctx, target)
	if err != nil {
		t.Fatalf("categories for %s failed: %v", target.URL, err)
	}
	category := categories[0]
	feed, err := c.Feed(ctx, category.URL, 1)
	if err != nil {
		t.Fatalf("category feed %s failed: %v", category.URL, err)
	}
	if feed.Title != category.Name || feed.Pagination.Current != 1 || len(feed.Items) == 0 {
		t.Fatalf("unexpected category feed: %+v", feed)
	}
	assertUniqueItems(t, feed.Items)
	t.Logf("category feed: name=%q title=%q items=%d total=%d next=%s", category.Name, feed.Title, len(feed.Items), feed.Pagination.Total, feed.Pagination.NextURL)
	logFeedItems(t, "category page 1", feed.Items)
	if err := ctx.Err(); err != nil {
		t.Fatalf("live run did not finish within its deadline: %v", err)
	}
}

func TestLiveSearch(t *testing.T) {
	c, err := New(Options{
		EntryURL: os.Getenv("AACG_ENTRY_URL"),
		Proxy:    os.Getenv("AACG_PROXY"),
		Timeout:  20 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	discovery, err := c.Discover(ctx)
	if err != nil {
		t.Fatalf("discovery failed (entry=%s landing=%s): %v", discovery.EntryURL, discovery.LandingURL, err)
	}
	var target Target
	for _, candidate := range discovery.Targets {
		health, err := c.Check(ctx, candidate)
		if err != nil || !health.Usable {
			t.Logf("[%s] %s url=%s not usable: %v", candidate.Kind, candidate.Name, candidate.URL, err)
			continue
		}
		target = candidate
		break
	}
	if target.URL == "" {
		t.Fatal("no target was usable for search")
	}
	first, err := c.Search(ctx, target.URL, "51", 1)
	if err != nil {
		t.Fatalf("search page 1 failed: %v", err)
	}
	if !strings.Contains(first.Title, "51") || len(first.Items) == 0 || first.Pagination.Current != 1 || first.Pagination.NextURL == "" {
		t.Fatalf("unexpected search page 1: %+v", first)
	}
	assertUniqueItems(t, first.Items)
	t.Logf("search page 1: target=%s title=%q items=%d total=%d next=%s", target.URL, first.Title, len(first.Items), first.Pagination.Total, first.Pagination.NextURL)
	logFeedItems(t, "search page 1", first.Items)
	second, err := c.Search(ctx, target.URL, "51", 2)
	if err != nil {
		t.Fatalf("search page 2 failed: %v", err)
	}
	if second.Pagination.Current != 2 || second.Pagination.PreviousURL == "" || len(second.Items) == 0 || second.Items[0].URL == first.Items[0].URL {
		t.Fatalf("unexpected search page 2: %+v", second)
	}
	assertUniqueItems(t, second.Items)
	t.Logf("search page 2: title=%q items=%d total=%d prev=%s next=%s", second.Title, len(second.Items), second.Pagination.Total, second.Pagination.PreviousURL, second.Pagination.NextURL)
	logFeedItems(t, "search page 2", second.Items)
	if err := ctx.Err(); err != nil {
		t.Fatalf("live run did not finish within its deadline: %v", err)
	}
}

func logFeedItems(t *testing.T, label string, items []ArticleSummary) {
	t.Helper()
	for i, item := range items {
		if i == 3 {
			break
		}
		t.Logf("%s item %d: title=%q url=%s cover=%q date=%s", label, i, item.Title, item.URL, item.CoverURL, item.PublishedAt)
	}
}

func assertUniqueItems(t *testing.T, items []ArticleSummary) {
	t.Helper()
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if seen[item.URL] {
			t.Fatalf("duplicate item URL: %s", item.URL)
		}
		seen[item.URL] = true
	}
}
