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
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/algorand/go-algorand/test/partitiontest"
)

func TestLRUCache_BasicOperations(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	cache := newLRUCache[string, int](3)

	// Initially empty
	require.Equal(t, 0, cache.Len())

	// Put and Get
	cache.Put("a", 1)
	require.Equal(t, 1, cache.Len())

	val, ok := cache.Get("a")
	require.True(t, ok)
	require.Equal(t, 1, val)

	// Get non-existent key
	val, ok = cache.Get("nonexistent")
	require.False(t, ok)
	require.Equal(t, 0, val)
}

func TestLRUCache_UpdateExistingKey(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	cache := newLRUCache[string, int](3)

	cache.Put("a", 1)
	cache.Put("a", 2)

	// Length should stay the same
	require.Equal(t, 1, cache.Len())

	// Value should be updated
	val, ok := cache.Get("a")
	require.True(t, ok)
	require.Equal(t, 2, val)
}

func TestLRUCache_Eviction(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	cache := newLRUCache[string, int](3)

	// Fill cache
	cache.Put("a", 1)
	cache.Put("b", 2)
	cache.Put("c", 3)
	require.Equal(t, 3, cache.Len())

	// All keys should exist
	_, ok := cache.Get("a")
	require.True(t, ok)
	_, ok = cache.Get("b")
	require.True(t, ok)
	_, ok = cache.Get("c")
	require.True(t, ok)

	// After accessing a, b, c in that order, the order is:
	// - After Get("a"): a is MRU, order is: a (MRU), c, b (LRU)
	// - After Get("b"): b is MRU, order is: b (MRU), a, c (LRU)
	// - After Get("c"): c is MRU, order is: c (MRU), b, a (LRU)
	// Adding "d" should evict "a" (LRU)
	cache.Put("d", 4)
	require.Equal(t, 3, cache.Len())

	// "a" should be evicted
	_, ok = cache.Get("a")
	require.False(t, ok)

	// Others should still exist
	_, ok = cache.Get("b")
	require.True(t, ok)
	_, ok = cache.Get("c")
	require.True(t, ok)
	_, ok = cache.Get("d")
	require.True(t, ok)
}

func TestLRUCache_EvictionOrder(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	cache := newLRUCache[string, int](3)

	// Insert a, b, c
	cache.Put("a", 1) // a
	cache.Put("b", 2) // b, a
	cache.Put("c", 3) // c, b, a

	// Access "a" to make it MRU
	cache.Get("a") // a, c, b

	// Insert "d", should evict "b" (LRU)
	cache.Put("d", 4) // d, a, c

	// "b" should be evicted
	_, ok := cache.Get("b")
	require.False(t, ok, "b should have been evicted")

	// a, c, d should exist
	_, ok = cache.Get("a")
	require.True(t, ok, "a should still exist")
	_, ok = cache.Get("c")
	require.True(t, ok, "c should still exist")
	_, ok = cache.Get("d")
	require.True(t, ok, "d should still exist")
}

func TestLRUCache_PutMovesMRU(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	cache := newLRUCache[string, int](3)

	// Insert a, b, c
	cache.Put("a", 1) // a
	cache.Put("b", 2) // b, a
	cache.Put("c", 3) // c, b, a

	// Update "a" (should move it to MRU)
	cache.Put("a", 10) // a, c, b

	// Insert "d", should evict "b" (LRU)
	cache.Put("d", 4) // d, a, c

	// "b" should be evicted
	_, ok := cache.Get("b")
	require.False(t, ok, "b should have been evicted")

	// "a" should still exist with updated value
	val, ok := cache.Get("a")
	require.True(t, ok, "a should still exist")
	require.Equal(t, 10, val, "a should have updated value")
}

func TestLRUCache_CapacityOne(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	cache := newLRUCache[string, int](1)

	cache.Put("a", 1)
	require.Equal(t, 1, cache.Len())

	val, ok := cache.Get("a")
	require.True(t, ok)
	require.Equal(t, 1, val)

	// Adding another should evict "a"
	cache.Put("b", 2)
	require.Equal(t, 1, cache.Len())

	_, ok = cache.Get("a")
	require.False(t, ok)

	val, ok = cache.Get("b")
	require.True(t, ok)
	require.Equal(t, 2, val)
}

func TestLRUCache_ZeroCapacityPanics(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	require.Panics(t, func() {
		newLRUCache[string, int](0)
	})
}

func TestLRUCache_NegativeCapacityPanics(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	require.Panics(t, func() {
		newLRUCache[string, int](-1)
	})
}

func TestLRUCache_GenericTypes(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	// Test with int keys and string values
	cache1 := newLRUCache[int, string](2)
	cache1.Put(1, "one")
	cache1.Put(2, "two")

	val, ok := cache1.Get(1)
	require.True(t, ok)
	require.Equal(t, "one", val)

	// Test with struct key
	type cacheKey struct {
		round   uint64
		address string
	}

	cache2 := newLRUCache[cacheKey, uint64](2)
	key1 := cacheKey{round: 100, address: "ADDR1"}
	key2 := cacheKey{round: 100, address: "ADDR2"}

	cache2.Put(key1, 1000)
	cache2.Put(key2, 2000)

	val2, ok := cache2.Get(key1)
	require.True(t, ok)
	require.Equal(t, uint64(1000), val2)

	val2, ok = cache2.Get(key2)
	require.True(t, ok)
	require.Equal(t, uint64(2000), val2)
}

func TestLRUCache_ConcurrentAccess(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	cache := newLRUCache[int, int](100)

	var wg sync.WaitGroup
	numGoroutines := 10
	numOperations := 100

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				cache.Put(base*numOperations+j, j)
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				cache.Get(base*numOperations + j)
			}
		}(i)
	}

	wg.Wait()

	// Cache should not exceed capacity
	require.LessOrEqual(t, cache.Len(), 100)
}

func TestLRUCache_ConcurrentReadWrite(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	cache := newLRUCache[string, int](10)

	var wg sync.WaitGroup

	// Writer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			cache.Put("key", i)
		}
	}()

	// Reader goroutines
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				cache.Get("key")
			}
		}()
	}

	wg.Wait()
}

func TestLRUCache_LargeCapacity(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	capacity := 10000
	cache := newLRUCache[int, int](capacity)

	// Fill to capacity
	for i := 0; i < capacity; i++ {
		cache.Put(i, i*2)
	}
	require.Equal(t, capacity, cache.Len())

	// All entries should exist
	for i := 0; i < capacity; i++ {
		val, ok := cache.Get(i)
		require.True(t, ok, "key %d should exist", i)
		require.Equal(t, i*2, val)
	}

	// Add one more, first should be evicted
	cache.Put(capacity, capacity*2)
	require.Equal(t, capacity, cache.Len())

	// Key 0 should be evicted (it was accessed first in the Get loop above,
	// but then all others were accessed after, making 0 the LRU)
	_, ok := cache.Get(0)
	require.False(t, ok, "key 0 should have been evicted")
}
