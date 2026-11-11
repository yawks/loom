package main

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestQueryCacheCollapsesConcurrentMissesAndInvalidates(t *testing.T) {
	var cache queryCache[int32]
	var calls atomic.Int32
	var group sync.WaitGroup
	start := make(chan struct{})
	for range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			value, err := cache.getOrLoad(time.Minute, func() (int32, error) { return calls.Add(1), nil })
			if err != nil || value != 1 {
				t.Errorf("unexpected cached result: %d, %v", value, err)
			}
		}()
	}
	close(start)
	group.Wait()
	if calls.Load() != 1 {
		t.Fatalf("loaded %d times", calls.Load())
	}
	cache.invalidate()
	value, _ := cache.getOrLoad(time.Minute, func() (int32, error) { return calls.Add(1), nil })
	if value != 2 {
		t.Fatal("invalidation did not refresh")
	}
	cache.mu.Lock()
	cache.expiresAt = time.Time{}
	cache.mu.Unlock()
	value, _ = cache.getOrLoad(time.Minute, func() (int32, error) { return calls.Add(1), nil })
	if value != 3 {
		t.Fatal("TTL did not refresh")
	}
}

func TestQueryCacheDoesNotLoseConcurrentInvalidationOrCacheErrors(t *testing.T) {
	var cache queryCache[int]
	value, err := cache.getOrLoad(time.Minute, func() (int, error) {
		cache.invalidate()
		return 1, nil
	})
	if err != nil || value != 1 || !cache.expiresAt.IsZero() {
		t.Fatal("in-flight load erased invalidation")
	}
	_, err = cache.getOrLoad(time.Minute, func() (int, error) { return 0, errors.New("failed") })
	if err == nil || !cache.expiresAt.IsZero() {
		t.Fatal("cached a failed query")
	}
	value, err = cache.getOrLoad(time.Minute, func() (int, error) { return 2, nil })
	if err != nil || value != 2 {
		t.Fatal("cache did not recover")
	}
}
