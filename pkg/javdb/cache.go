package javdb

import (
	"sync"
	"time"
)

// cache is a tiny TTL cache with in-flight de-duplication. Ranking and search
// results are identical for every caller for minutes at a time, and detail
// pages are frequently requested twice (list -> detail), so this removes a
// surprising amount of upstream load.
type cache struct {
	ttl time.Duration

	mu       sync.Mutex
	entries  map[string]cacheEntry
	inflight map[string]*cacheCall
}

type cacheEntry struct {
	value   any
	expires time.Time
}

type cacheCall struct {
	wg   sync.WaitGroup
	val  any
	err  error
	done bool
}

func newCache(ttl time.Duration) *cache {
	if ttl <= 0 {
		return nil
	}
	return &cache{
		ttl:      ttl,
		entries:  map[string]cacheEntry{},
		inflight: map[string]*cacheCall{},
	}
}

// Get returns a cached value when still fresh.
func (c *cache) Get(key string) (any, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expires) {
		if ok {
			delete(c.entries, key)
		}
		return nil, false
	}
	return e.value, true
}

// Do runs fn once per key, serving duplicates (concurrent or cached) from the
// shared result.
func (c *cache) Do(key string, fn func() (any, error)) (any, error) {
	if c == nil {
		return fn()
	}
	if v, ok := c.Get(key); ok {
		return v, nil
	}
	c.mu.Lock()
	if call, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		call.wg.Wait()
		return call.val, call.err
	}
	call := &cacheCall{}
	call.wg.Add(1)
	c.inflight[key] = call
	c.mu.Unlock()

	call.val, call.err = fn()
	call.done = true

	if call.err == nil {
		c.mu.Lock()
		c.entries[key] = cacheEntry{value: call.val, expires: time.Now().Add(c.ttl)}
		c.mu.Unlock()
	}
	c.mu.Lock()
	delete(c.inflight, key)
	c.mu.Unlock()
	call.wg.Done()
	return call.val, call.err
}

// Purge drops every entry (used by tests and by Client.ResetCache).
func (c *cache) Purge() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]cacheEntry{}
}
