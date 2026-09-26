package cache

import (
	"container/list"
	"sync"
)

const ReaderCacheCapacity = 128
const MaxSourceMetadataBytes int64 = 128 << 10

// CappedSourceBytes sums source sizes without allowing growth beyond the cache threshold.
func CappedSourceBytes(total, next int64) int64 {
	if next < 0 || total > MaxSourceMetadataBytes || next > MaxSourceMetadataBytes-total {
		return MaxSourceMetadataBytes + 1
	}
	return total + next
}

type lruEntry[K comparable, V any] struct {
	key   K
	value V
}

// LRU is a thread-safe least-recently-used cache with a fixed entry bound.
type LRU[K comparable, V any] struct {
	mu       sync.Mutex
	capacity int
	items    map[K]*list.Element
	recency  *list.List
}

func NewLRU[K comparable, V any](capacity int) *LRU[K, V] {
	if capacity < 0 {
		capacity = 0
	}
	return &LRU[K, V]{
		capacity: capacity,
		items:    make(map[K]*list.Element, capacity),
		recency:  list.New(),
	}
}

func (c *LRU[K, V]) Get(key K) (V, bool) {
	var zero V
	if c == nil {
		return zero, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	element, ok := c.items[key]
	if !ok {
		return zero, false
	}
	c.recency.MoveToFront(element)
	return element.Value.(lruEntry[K, V]).value, true
}

func (c *LRU[K, V]) Add(key K, value V) {
	if c == nil || c.capacity == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if element, ok := c.items[key]; ok {
		element.Value = lruEntry[K, V]{key: key, value: value}
		c.recency.MoveToFront(element)
		return
	}

	element := c.recency.PushFront(lruEntry[K, V]{key: key, value: value})
	c.items[key] = element
	if c.recency.Len() <= c.capacity {
		return
	}
	oldest := c.recency.Back()
	entry := oldest.Value.(lruEntry[K, V])
	delete(c.items, entry.key)
	c.recency.Remove(oldest)
}

func (c *LRU[K, V]) Delete(key K) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if element, ok := c.items[key]; ok {
		delete(c.items, key)
		c.recency.Remove(element)
	}
}

func (c *LRU[K, V]) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.recency.Len()
}
