package cache

import (
	"fmt"
	"sync"
	"testing"
)

func TestLRUEvictsLeastRecentlyUsedEntry(t *testing.T) {
	lru := NewLRU[string, int](2)
	lru.Add("first", 1)
	lru.Add("second", 2)
	if _, ok := lru.Get("first"); !ok {
		t.Fatal("expected first entry")
	}
	lru.Add("third", 3)

	if _, ok := lru.Get("second"); ok {
		t.Fatal("least-recently-used entry was not evicted")
	}
	if value, ok := lru.Get("first"); !ok || value != 1 {
		t.Fatalf("recent entry = %d, present=%t; want 1", value, ok)
	}
	if lru.Len() != 2 {
		t.Fatalf("cache length = %d, want 2", lru.Len())
	}
}

func TestLRUUpdateAndDelete(t *testing.T) {
	lru := NewLRU[string, int](2)
	lru.Add("key", 1)
	lru.Add("key", 2)
	if value, ok := lru.Get("key"); !ok || value != 2 {
		t.Fatalf("updated entry = %d, present=%t; want 2", value, ok)
	}
	lru.Delete("key")
	if _, ok := lru.Get("key"); ok || lru.Len() != 0 {
		t.Fatal("deleted entry remains in cache")
	}
}

func TestLRUConcurrentOperations(t *testing.T) {
	const capacity = 128
	const workers = 16
	const iterations = 500
	lru := NewLRU[int, string](capacity)
	var wait sync.WaitGroup
	for worker := range workers {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for iteration := range iterations {
				key := worker*iterations + iteration
				lru.Add(key, fmt.Sprint(key))
				lru.Get(key)
				if iteration%3 == 0 {
					lru.Delete(key)
				}
			}
		}(worker)
	}
	wait.Wait()
	if got := lru.Len(); got > capacity {
		t.Fatalf("cache length = %d, exceeds capacity %d", got, capacity)
	}
}

func TestLRUZeroCapacityDisablesCaching(t *testing.T) {
	lru := NewLRU[string, int](0)
	lru.Add("key", 1)
	if _, ok := lru.Get("key"); ok || lru.Len() != 0 {
		t.Fatal("zero-capacity cache retained an entry")
	}
}
