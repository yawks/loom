package main

import (
	"Loom/pkg/models"
	"sync"
	"testing"
)

func TestContactChanges(t *testing.T) {
	cache := &contactChangesCache{}
	contacts := []models.MetaContact{{ID: 2, DisplayName: "two"}, {ID: 1, DisplayName: "one"}}
	initial, err := cache.changes(contacts, "")
	if err != nil || !initial.Reset || len(initial.Contacts) != 2 || initial.Contacts[0].ID != 1 {
		t.Fatalf("initial=%+v err=%v", initial, err)
	}
	unchanged, err := cache.changes(contacts, initial.Revision)
	if err != nil || unchanged.Reset || len(unchanged.Contacts) != 0 || unchanged.Revision != initial.Revision {
		t.Fatalf("unchanged=%+v err=%v", unchanged, err)
	}
	updated := []models.MetaContact{{ID: 2, DisplayName: "changed"}, {ID: 3, DisplayName: "three"}}
	delta, err := cache.changes(updated, initial.Revision)
	if err != nil || delta.Reset || len(delta.Contacts) != 2 || len(delta.RemovedIDs) != 1 || delta.RemovedIDs[0] != 1 {
		t.Fatalf("delta=%+v err=%v", delta, err)
	}
	empty, err := cache.changes(nil, delta.Revision)
	if err != nil || empty.Reset || len(empty.RemovedIDs) != 2 {
		t.Fatalf("empty=%+v err=%v", empty, err)
	}
	reset, err := cache.changes(nil, "unknown")
	if err != nil || !reset.Reset || len(reset.Contacts) != 0 {
		t.Fatalf("reset=%+v err=%v", reset, err)
	}
	for i := 0; i < 5; i++ {
		_, _ = cache.changes([]models.MetaContact{{ID: uint(i + 10)}}, "")
	}
	reset, _ = cache.changes(contacts, initial.Revision)
	if !reset.Reset || len(cache.history) != 4 {
		t.Fatalf("eviction=%+v history=%d", reset, len(cache.history))
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, err := cache.changes(contacts, reset.Revision)
			if err != nil || response.Reset || len(response.Contacts) != 0 {
				t.Errorf("concurrent=%+v err=%v", response, err)
			}
		}()
	}
	wg.Wait()
}
