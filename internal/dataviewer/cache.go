package dataviewer

import (
	"hash/fnv"
	"strconv"
	"sync"
	"time"
)

// cache is a small TTL cache of computed profiles. Because the DuckDB engine is
// a single serialized connection, a cache hit is the difference between an
// instant header populate and a queued multi-query scan — so the cache is
// load-bearing, not merely an optimisation. Eviction is lazy (on read).
type cache struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]cacheEntry
}

type cacheEntry struct {
	profiles []ColumnProfile
	expires  time.Time
}

func newCache(ttl time.Duration) *cache {
	return &cache{ttl: ttl, m: make(map[string]cacheEntry)}
}

func (c *cache) get(key string, now time.Time) ([]ColumnProfile, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok {
		return nil, false
	}
	if now.After(e.expires) {
		delete(c.m, key)
		return nil, false
	}
	return e.profiles, true
}

func (c *cache) put(key string, profiles []ColumnProfile, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = cacheEntry{profiles: profiles, expires: now.Add(c.ttl)}
}

// cacheKey identifies a profile by its reconstructed reader expression (which
// already encodes bucket/key/format/all reader options) and the sample cap.
func cacheKey(from string, cap int) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(from))
	_, _ = h.Write([]byte{'|'})
	_, _ = h.Write([]byte(strconv.Itoa(cap)))
	return strconv.FormatUint(h.Sum64(), 16)
}
