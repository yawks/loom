package main

import "time"

const messageSummaryCacheTTL = time.Minute

func (cache *queryCache[T]) invalidate() {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.generation++
	cache.expiresAt = time.Time{}
}

func (cache *queryCache[T]) getOrLoad(ttl time.Duration, load func() (T, error)) (T, error) {
	cache.mu.RLock()
	if time.Now().Before(cache.expiresAt) {
		value := cache.data
		cache.mu.RUnlock()
		return value, nil
	}
	cache.mu.RUnlock()
	cache.loadMu.Lock()
	defer cache.loadMu.Unlock()
	cache.mu.RLock()
	if time.Now().Before(cache.expiresAt) {
		value := cache.data
		cache.mu.RUnlock()
		return value, nil
	}
	generation := cache.generation
	cache.mu.RUnlock()
	value, err := load()
	if err != nil {
		return value, err
	}
	cache.mu.Lock()
	// An event may arrive during the query. Never let an older in-flight read
	// overwrite that invalidation and keep stale data fresh for the entire TTL.
	if cache.generation == generation {
		cache.data = value
		cache.expiresAt = time.Now().Add(ttl)
	}
	cache.mu.Unlock()
	return value, nil
}
