package javdb

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheNilIsPassthrough(t *testing.T) {
	var c *cache
	calls := 0
	for i := 0; i < 3; i++ {
		v, err := c.Do("k", func() (any, error) {
			calls++
			return i, nil
		})
		requireNoErr(t, err)
		if v.(int) != i {
			t.Fatalf("value: %v", v)
		}
	}
	if calls != 3 {
		t.Fatalf("a nil cache must never memoise, ran %d times", calls)
	}
	if _, ok := c.Get("k"); ok {
		t.Fatal("nil cache reported a hit")
	}
	c.Purge() // must be safe on a nil receiver
}

func TestCacheTTL(t *testing.T) {
	c := newCache(40 * time.Millisecond)
	runs := 0
	get := func() {
		if _, err := c.Do("k", func() (any, error) {
			runs++
			return "v", nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	get()
	get()
	if runs != 1 {
		t.Fatalf("fresh entry was not reused: %d runs", runs)
	}
	time.Sleep(60 * time.Millisecond)
	get()
	if runs != 2 {
		t.Fatalf("expired entry was served from the cache: %d runs", runs)
	}
}

func TestCacheStoresOnlySuccess(t *testing.T) {
	c := newCache(time.Minute)
	want := errors.New("boom")
	for i := 0; i < 2; i++ {
		_, err := c.Do("k", func() (any, error) { return nil, want })
		if !errors.Is(err, want) {
			t.Fatalf("error not propagated: %v", err)
		}
	}
	// Failures must not be cached, so the producer ran twice, then a success
	// is cached and the third call is served from the entry.
	if _, err := c.Do("k", func() (any, error) { return "ok", nil }); err != nil {
		t.Fatal(err)
	}
	v, ok := c.Get("k")
	if !ok || v.(string) != "ok" {
		t.Fatalf("hit after failure: %v %v", v, ok)
	}
}

func TestCacheDedupesConcurrentCallers(t *testing.T) {
	const goroutines = 32
	c := newCache(time.Minute)

	start := make(chan struct{})
	var runs, hits atomic.Int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			<-start
			_, err := c.Do("key", func() (any, error) {
				runs.Add(1)
				time.Sleep(20 * time.Millisecond)
				return "value", nil
			})
			if err == nil {
				hits.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := runs.Load(); got != 1 {
		t.Fatalf("producer ran %d times, want 1 (in-flight de-duplication)", got)
	}
	if got := hits.Load(); got != goroutines {
		t.Fatalf("%d/%d callers received a value", got, goroutines)
	}
}

func TestCachePurge(t *testing.T) {
	c := newCache(time.Minute)
	if _, err := c.Do("a", func() (any, error) { return 1, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do("b", func() (any, error) { return 2, nil }); err != nil {
		t.Fatal(err)
	}
	c.Purge()
	if _, ok := c.Get("a"); ok {
		t.Fatal("Purge left entries behind")
	}
	ran := false
	if _, err := c.Do("a", func() (any, error) { ran = true; return 3, nil }); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("producer skipped after Purge")
	}
}

func TestNewCacheDisabledForNonPositiveTTL(t *testing.T) {
	for _, ttl := range []time.Duration{0, -time.Second} {
		if c := newCache(ttl); c != nil {
			t.Fatalf("newCache(%v) = %p, want nil (caching disabled)", ttl, c)
		}
	}
	if c := newCache(time.Second); c == nil {
		t.Fatal("newCache(1s) must build a cache")
	} else {
		// Keys are plain strings; a collision would silently serve wrong data.
		key := fmt.Sprintf("ranking:%s:%s", RankingMovies, PeriodDaily)
		if _, err := c.Do(key, func() (any, error) { return "x", nil }); err != nil {
			t.Fatal(err)
		}
		if v, ok := c.Get(key); !ok || v.(string) != "x" {
			t.Fatalf("key %q not cached", key)
		}
	}
}
