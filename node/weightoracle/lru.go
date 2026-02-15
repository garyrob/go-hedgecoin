// Copyright (C) 2019-2026 Algorand, Inc.
// This file is part of go-algorand
//
// go-algorand is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.
//
// go-algorand is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with go-algorand.  If not, see <https://www.gnu.org/licenses/>.

package weightoracle

import (
	"github.com/algorand/go-deadlock"

	"github.com/algorand/go-algorand/util"
)

// lruEntry holds a key-value pair for the LRU cache.
type lruEntry[K comparable, V any] struct {
	key   K
	value V
}

// lruCache is a thread-safe, bounded LRU cache with O(1) operations.
// It uses a doubly-linked list for recency ordering and a hash map for fast lookups.
// When the cache reaches capacity, the least recently used entry is evicted on Put.
//
// Note: Get() mutates the list (moves accessed node to front), so we use deadlock.Mutex
// instead of RWMutex. This is the standard LRU tradeoff.
type lruCache[K comparable, V any] struct {
	mu       deadlock.Mutex
	capacity int
	list     *util.List[*lruEntry[K, V]]
	items    map[K]*util.ListNode[*lruEntry[K, V]]
}

// newLRUCache creates a new bounded LRU cache with the specified capacity.
// The capacity must be greater than 0.
func newLRUCache[K comparable, V any](capacity int) *lruCache[K, V] {
	if capacity <= 0 {
		panic("lruCache capacity must be > 0")
	}
	return &lruCache[K, V]{
		capacity: capacity,
		list:     util.NewList[*lruEntry[K, V]]().AllocateFreeNodes(capacity),
		items:    make(map[K]*util.ListNode[*lruEntry[K, V]], capacity),
	}
}

// Get retrieves a value from the cache by key.
// If the key exists, the entry is moved to the front (most recently used) and
// the value is returned with ok=true.
// If the key does not exist, the zero value of V is returned with ok=false.
func (c *lruCache[K, V]) Get(key K) (value V, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	node, exists := c.items[key]
	if !exists {
		var zero V
		return zero, false
	}

	// Move to front (most recently used)
	c.list.MoveToFront(node)
	return node.Value.value, true
}

// Put adds or updates a key-value pair in the cache.
// If the key already exists, the value is updated and the entry is moved to front.
// If the cache is at capacity and the key is new, the least recently used entry
// is evicted before adding the new entry.
func (c *lruCache[K, V]) Put(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if key exists
	if node, exists := c.items[key]; exists {
		// Update value and move to front
		node.Value.value = value
		c.list.MoveToFront(node)
		return
	}

	// Evict LRU entry if at capacity
	if len(c.items) >= c.capacity {
		back := c.list.Back()
		if back != nil {
			delete(c.items, back.Value.key)
			c.list.Remove(back)
		}
	}

	// Add new entry at front
	entry := &lruEntry[K, V]{key: key, value: value}
	node := c.list.PushFront(entry)
	c.items[key] = node
}

// Len returns the current number of entries in the cache.
func (c *lruCache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}
