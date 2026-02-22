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
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/algorand/go-algorand/crypto"
	"github.com/algorand/go-algorand/data/basics"
	"github.com/algorand/go-algorand/ledger/ledgercore"
	"github.com/algorand/go-algorand/test/partitiontest"
)

// testServer is an HTTP test server for testing the weight oracle client.
// It handles POST requests and responds with configurable JSON responses.
type testServer struct {
	server *httptest.Server
	port   uint16
	// handler is called with the URL path and request body, returns response object
	handler func(path string, req map[string]interface{}) interface{}
}

// newTestServer creates and starts a test HTTP server on a random available port.
// The handler function processes requests and returns response objects to be JSON-encoded.
// For backward compatibility, if handler only takes req, we use a wrapper.
func newTestServer(t *testing.T, handler func(req map[string]interface{}) interface{}) *testServer {
	t.Helper()

	// Wrap the simple handler to work with the path-based handler
	pathHandler := func(path string, req map[string]interface{}) interface{} {
		return handler(req)
	}

	return newTestServerWithPath(t, pathHandler)
}

// newTestServerWithPath creates a test HTTP server where the handler receives the URL path.
func newTestServerWithPath(t *testing.T, handler func(path string, req map[string]interface{}) interface{}) *testServer {
	t.Helper()

	s := &testServer{handler: handler}

	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only handle POST requests
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Read request body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}

		// Parse JSON request (empty body is ok for some endpoints)
		var req map[string]interface{}
		if len(body) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				http.Error(w, "Invalid JSON", http.StatusBadRequest)
				return
			}
		} else {
			req = make(map[string]interface{})
		}

		// Call handler with path
		resp := s.handler(r.URL.Path, req)

		// Write JSON response
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		}
	}))

	// Extract port from the test server URL
	addr := s.server.Listener.Addr().(*net.TCPAddr)
	s.port = uint16(addr.Port)

	return s
}

// Close shuts down the test server.
func (s *testServer) Close() {
	s.server.Close()
}

// TestPingSuccess tests that Ping returns nil on a successful pong response.
func TestPingSuccess(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	server := newTestServerWithPath(t, func(path string, req map[string]interface{}) interface{} {
		require.Equal(t, "/ping", path)
		return map[string]interface{}{
			"pong": true,
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	err := client.Ping()
	require.NoError(t, err)
}

// TestPingDaemonError tests that Ping returns a DaemonError when the daemon
// returns an error response.
func TestPingDaemonError(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	server := newTestServerWithPath(t, func(path string, req map[string]interface{}) interface{} {
		require.Equal(t, "/ping", path)
		return map[string]interface{}{
			"error": "daemon is unhealthy",
			"code":  "internal",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	err := client.Ping()
	require.Error(t, err)

	// Verify it's a DaemonError and can be extracted with errors.As
	var daemonErr *ledgercore.DaemonError
	require.ErrorAs(t, err, &daemonErr)
	require.Equal(t, "internal", daemonErr.Code)
	require.Equal(t, "daemon is unhealthy", daemonErr.Msg)

	// Verify IsDaemonError helper works
	require.True(t, ledgercore.IsDaemonError(err, "internal"))
	require.False(t, ledgercore.IsDaemonError(err, "not_found"))
}

// TestPingUnreachable tests that Ping returns an error when the daemon is not
// reachable (connection refused).
func TestPingUnreachable(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	// Get a port that's guaranteed to be available but not listening:
	// bind a listener, get its port, then close it immediately
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	listener.Close()

	client := NewClient(port)
	err = client.Ping()
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to connect")

	// Should NOT be a DaemonError (network error, not daemon error)
	var daemonErr *ledgercore.DaemonError
	require.NotErrorAs(t, err, &daemonErr)
}

// TestPingMissingPong tests that Ping returns an error if the response doesn't
// contain pong:true.
func TestPingMissingPong(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	server := newTestServerWithPath(t, func(path string, req map[string]interface{}) interface{} {
		require.Equal(t, "/ping", path)
		// Return a response without pong field
		return map[string]interface{}{
			"something": "else",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	err := client.Ping()
	require.Error(t, err)
	require.Contains(t, err.Error(), "pong")
}

// TestPingPongFalse tests that Ping returns an error if pong is explicitly false.
func TestPingPongFalse(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		return map[string]interface{}{
			"pong": false,
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	err := client.Ping()
	require.Error(t, err)
	require.Contains(t, err.Error(), "pong")
}

// TestNewClient tests that NewClient correctly initializes the client.
func TestNewClient(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	client := NewClient(12345)
	require.NotNil(t, client)
	require.Equal(t, "http://127.0.0.1:12345", client.baseURL)
	require.NotNil(t, client.httpClient)
}

// TestPingConcurrent tests that multiple concurrent Ping requests work correctly.
func TestPingConcurrent(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		return map[string]interface{}{
			"pong": true,
		}
	})
	defer server.Close()

	client := NewClient(server.port)

	const numRequests = 10
	var wg sync.WaitGroup
	wg.Add(numRequests)
	errors := make(chan error, numRequests)

	for i := 0; i < numRequests; i++ {
		go func() {
			defer wg.Done()
			if err := client.Ping(); err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("unexpected error: %v", err)
	}
}

// slowTestServer is an HTTP test server that delays before responding.
// It's used to test timeout behavior.
type slowTestServer struct {
	server *httptest.Server
	port   uint16
	delay  time.Duration
}

// newSlowTestServer creates a test server that delays the specified duration before responding.
func newSlowTestServer(t *testing.T, delay time.Duration) *slowTestServer {
	t.Helper()

	s := &slowTestServer{delay: delay}

	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read request body (to complete the request)
		_, _ = io.ReadAll(r.Body)

		// Delay before responding
		time.Sleep(s.delay)

		// Send response (might fail if client timed out)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"pong": true})
	}))

	addr := s.server.Listener.Addr().(*net.TCPAddr)
	s.port = uint16(addr.Port)

	return s
}

func (s *slowTestServer) Close() {
	s.server.Close()
}

// TestPingTimeout tests that Ping returns a timeout error when the daemon
// takes too long to respond.
func TestPingTimeout(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	// Create a server that delays 500ms before responding
	server := newSlowTestServer(t, 500*time.Millisecond)
	defer server.Close()

	// Create a client with a very short timeout (50ms)
	client := NewClient(server.port)
	client.SetTimeouts(0, 50*time.Millisecond)

	err := client.Ping()
	require.Error(t, err)
	// HTTP client returns "context deadline exceeded" on timeout
	require.Contains(t, err.Error(), "context deadline exceeded")

	// Should NOT be a DaemonError (it's a timeout/network error)
	var daemonErr *ledgercore.DaemonError
	require.NotErrorAs(t, err, &daemonErr)
}

// TestSetTimeouts tests that SetTimeouts correctly configures the client.
// Note: dialTimeout is no longer changeable after client creation since it's
// baked into the HTTP Transport. This test only verifies queryTimeout changes.
func TestSetTimeouts(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	client := NewClient(12345)

	// Verify default query timeout
	require.Equal(t, DefaultQueryTimeout, client.queryTimeout)

	// Set custom query timeout (dialTimeout parameter is ignored)
	client.SetTimeouts(1*time.Second, 2*time.Second)
	require.Equal(t, 2*time.Second, client.queryTimeout)

	// Pass 0 to keep current value
	client.SetTimeouts(0, 3*time.Second)
	require.Equal(t, 3*time.Second, client.queryTimeout)

	client.SetTimeouts(4*time.Second, 0)
	require.Equal(t, 3*time.Second, client.queryTimeout) // unchanged
}

// makeTestAddress creates a deterministic test address from an index.
func makeTestAddress(index int) basics.Address {
	var addr basics.Address
	addr[0] = byte(index)
	addr[1] = byte(index >> 8)
	return addr
}

// makeTestSelectionID creates a deterministic test VRF key from an index.
func makeTestSelectionID(index int) crypto.VRFVerifier {
	var selID crypto.VRFVerifier
	selID[0] = byte(index)
	selID[1] = byte(index >> 8)
	return selID
}

// TestWeightSuccess tests that Weight returns the correct weight on success.
func TestWeightSuccess(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	testAddr := makeTestAddress(42)
	testSelID := makeTestSelectionID(99)
	testRound := basics.Round(1000)

	server := newTestServerWithPath(t, func(path string, req map[string]interface{}) interface{} {
		require.Equal(t, "/weight", path)
		require.Equal(t, testAddr.String(), req["address"])
		require.Equal(t, hex.EncodeToString(testSelID[:]), req["selection_id"])
		require.Equal(t, "1000", req["balance_round"])

		return map[string]interface{}{
			"weight": "123456789",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	weight, err := client.Weight(testRound, testAddr, testSelID)
	require.NoError(t, err)
	require.Equal(t, uint64(123456789), weight)
}

// TestWeightZero tests that Weight correctly returns zero weight.
func TestWeightZero(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	server := newTestServerWithPath(t, func(path string, req map[string]interface{}) interface{} {
		require.Equal(t, "/weight", path)
		return map[string]interface{}{
			"weight": "0",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	weight, err := client.Weight(basics.Round(100), makeTestAddress(1), makeTestSelectionID(1))
	require.NoError(t, err)
	require.Equal(t, uint64(0), weight)
}

// TestWeightLargeValue tests that Weight handles large uint64 values correctly.
func TestWeightLargeValue(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	// Test with max uint64 value
	maxUint64 := "18446744073709551615"

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		return map[string]interface{}{
			"weight": maxUint64,
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	weight, err := client.Weight(basics.Round(100), makeTestAddress(1), makeTestSelectionID(1))
	require.NoError(t, err)
	require.Equal(t, uint64(18446744073709551615), weight)
}

// TestWeightDaemonError tests that Weight returns a DaemonError when the daemon
// returns an error response.
func TestWeightDaemonError(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	server := newTestServerWithPath(t, func(path string, req map[string]interface{}) interface{} {
		require.Equal(t, "/weight", path)
		return map[string]interface{}{
			"error": "account not found",
			"code":  "not_found",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	weight, err := client.Weight(basics.Round(100), makeTestAddress(1), makeTestSelectionID(1))
	require.Error(t, err)
	require.Equal(t, uint64(0), weight)

	// Verify it's a DaemonError and can be extracted with errors.As
	var daemonErr *ledgercore.DaemonError
	require.ErrorAs(t, err, &daemonErr)
	require.Equal(t, "not_found", daemonErr.Code)
	require.Equal(t, "account not found", daemonErr.Msg)

	// Verify IsDaemonError helper works
	require.True(t, ledgercore.IsDaemonError(err, "not_found"))
	require.False(t, ledgercore.IsDaemonError(err, "internal"))
}

// TestWeightMissingField tests that Weight returns an error if the weight field is missing.
func TestWeightMissingField(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		return map[string]interface{}{
			"something": "else",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	_, err := client.Weight(basics.Round(100), makeTestAddress(1), makeTestSelectionID(1))
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing weight field")
}

// TestWeightInvalidValue tests that Weight returns an error for non-numeric weight.
func TestWeightInvalidValue(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		return map[string]interface{}{
			"weight": "not-a-number",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	_, err := client.Weight(basics.Round(100), makeTestAddress(1), makeTestSelectionID(1))
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid weight value")
}

// TestWeightNegativeValue tests that Weight returns an error for negative weight.
func TestWeightNegativeValue(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		return map[string]interface{}{
			"weight": "-100",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	_, err := client.Weight(basics.Round(100), makeTestAddress(1), makeTestSelectionID(1))
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid weight value")
}

// TestWeightCacheHit tests that repeated Weight queries with the same parameters
// return cached results and don't hit the daemon.
func TestWeightCacheHit(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	var queryCount atomic.Int32

	testAddr := makeTestAddress(42)
	testSelID := makeTestSelectionID(99)
	testRound := basics.Round(1000)

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		queryCount.Add(1)
		return map[string]interface{}{
			"weight": "123456",
		}
	})
	defer server.Close()

	client := NewClient(server.port)

	// First query - should hit daemon
	weight1, err := client.Weight(testRound, testAddr, testSelID)
	require.NoError(t, err)
	require.Equal(t, uint64(123456), weight1)
	require.Equal(t, int32(1), queryCount.Load())

	// Second query with same parameters - should hit cache
	weight2, err := client.Weight(testRound, testAddr, testSelID)
	require.NoError(t, err)
	require.Equal(t, uint64(123456), weight2)
	require.Equal(t, int32(1), queryCount.Load()) // Still 1, no additional query
}

// TestWeightCacheMiss tests that Weight queries with different parameters
// don't share cache entries.
func TestWeightCacheMiss(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	var queryCount atomic.Int32

	testAddr := makeTestAddress(42)
	testSelID := makeTestSelectionID(99)

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		queryCount.Add(1)
		// Return different weights based on round
		round := req["balance_round"].(string)
		if round == "100" {
			return map[string]interface{}{"weight": "100"}
		}
		return map[string]interface{}{"weight": "200"}
	})
	defer server.Close()

	client := NewClient(server.port)

	// Query for round 100
	weight1, err := client.Weight(basics.Round(100), testAddr, testSelID)
	require.NoError(t, err)
	require.Equal(t, uint64(100), weight1)
	require.Equal(t, int32(1), queryCount.Load())

	// Query for round 200 - different round means cache miss
	weight2, err := client.Weight(basics.Round(200), testAddr, testSelID)
	require.NoError(t, err)
	require.Equal(t, uint64(200), weight2)
	require.Equal(t, int32(2), queryCount.Load())

	// Query for round 100 again - cache hit
	weight3, err := client.Weight(basics.Round(100), testAddr, testSelID)
	require.NoError(t, err)
	require.Equal(t, uint64(100), weight3)
	require.Equal(t, int32(2), queryCount.Load()) // Still 2
}

// TestWeightCacheDifferentKeys tests that cache entries are keyed by all parameters.
func TestWeightCacheDifferentKeys(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	var queryCount atomic.Int32

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		queryCount.Add(1)
		return map[string]interface{}{"weight": "1000"}
	})
	defer server.Close()

	client := NewClient(server.port)

	addr1 := makeTestAddress(1)
	addr2 := makeTestAddress(2)
	selID1 := makeTestSelectionID(1)
	selID2 := makeTestSelectionID(2)
	round := basics.Round(100)

	// All these should be cache misses (different combinations)
	_, err := client.Weight(round, addr1, selID1)
	require.NoError(t, err)
	require.Equal(t, int32(1), queryCount.Load())

	_, err = client.Weight(round, addr2, selID1) // Different address
	require.NoError(t, err)
	require.Equal(t, int32(2), queryCount.Load())

	_, err = client.Weight(round, addr1, selID2) // Different selectionID
	require.NoError(t, err)
	require.Equal(t, int32(3), queryCount.Load())

	// These should be cache hits
	_, err = client.Weight(round, addr1, selID1)
	require.NoError(t, err)
	require.Equal(t, int32(3), queryCount.Load())

	_, err = client.Weight(round, addr2, selID1)
	require.NoError(t, err)
	require.Equal(t, int32(3), queryCount.Load())
}

// TestWeightConcurrent tests that multiple concurrent Weight requests work correctly.
func TestWeightConcurrent(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		// Small delay to increase contention
		time.Sleep(1 * time.Millisecond)
		return map[string]interface{}{
			"weight": "999",
		}
	})
	defer server.Close()

	client := NewClient(server.port)

	const numRequests = 20
	var wg sync.WaitGroup
	wg.Add(numRequests)
	errs := make(chan error, numRequests)
	weights := make(chan uint64, numRequests)

	for i := 0; i < numRequests; i++ {
		go func(idx int) {
			defer wg.Done()
			// Use different addresses to avoid cache hits, testing true concurrency
			weight, err := client.Weight(basics.Round(100), makeTestAddress(idx), makeTestSelectionID(idx))
			if err != nil {
				errs <- err
				return
			}
			weights <- weight
		}(i)
	}

	wg.Wait()
	close(errs)
	close(weights)

	for err := range errs {
		t.Errorf("unexpected error: %v", err)
	}

	for weight := range weights {
		require.Equal(t, uint64(999), weight)
	}
}

// TestWeightWireFormat tests the exact wire format sent to the daemon.
func TestWeightWireFormat(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	// Create a specific address and selection ID with known encoding
	var addr basics.Address
	for i := range addr {
		addr[i] = byte(i)
	}

	var selID crypto.VRFVerifier
	for i := range selID {
		selID[i] = byte(i + 100)
	}

	expectedAddrStr := addr.String() // Base32 encoded with checksum
	expectedSelIDStr := hex.EncodeToString(selID[:])

	server := newTestServerWithPath(t, func(path string, req map[string]interface{}) interface{} {
		// Verify wire format
		require.Equal(t, "/weight", path)
		require.Equal(t, expectedAddrStr, req["address"])
		require.Equal(t, expectedSelIDStr, req["selection_id"])
		require.Equal(t, "12345", req["balance_round"])

		return map[string]interface{}{
			"weight": "42",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	weight, err := client.Weight(basics.Round(12345), addr, selID)
	require.NoError(t, err)
	require.Equal(t, uint64(42), weight)
}

// ============================================================================
// TotalWeight Tests
// ============================================================================

// TestTotalWeightSuccess tests that TotalWeight returns the correct total weight on success.
func TestTotalWeightSuccess(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	testBalanceRound := basics.Round(1000)
	testVoteRound := basics.Round(1001)

	server := newTestServerWithPath(t, func(path string, req map[string]interface{}) interface{} {
		require.Equal(t, "/total_weight", path)
		require.Equal(t, "1000", req["balance_round"])
		require.Equal(t, "1001", req["vote_round"])

		return map[string]interface{}{
			"total_weight": "9999999999",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	totalWeight, err := client.TotalWeight(testBalanceRound, testVoteRound)
	require.NoError(t, err)
	require.Equal(t, uint64(9999999999), totalWeight)
}

// TestTotalWeightZero tests that TotalWeight correctly returns zero.
func TestTotalWeightZero(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	server := newTestServerWithPath(t, func(path string, req map[string]interface{}) interface{} {
		require.Equal(t, "/total_weight", path)
		return map[string]interface{}{
			"total_weight": "0",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	totalWeight, err := client.TotalWeight(basics.Round(100), basics.Round(101))
	require.NoError(t, err)
	require.Equal(t, uint64(0), totalWeight)
}

// TestTotalWeightLargeValue tests that TotalWeight handles max uint64 value correctly.
func TestTotalWeightLargeValue(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	maxUint64 := "18446744073709551615"

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		return map[string]interface{}{
			"total_weight": maxUint64,
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	totalWeight, err := client.TotalWeight(basics.Round(100), basics.Round(101))
	require.NoError(t, err)
	require.Equal(t, uint64(18446744073709551615), totalWeight)
}

// TestTotalWeightDaemonError tests that TotalWeight returns a DaemonError when the daemon
// returns an error response.
func TestTotalWeightDaemonError(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	server := newTestServerWithPath(t, func(path string, req map[string]interface{}) interface{} {
		require.Equal(t, "/total_weight", path)
		return map[string]interface{}{
			"error": "round not available",
			"code":  "not_found",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	totalWeight, err := client.TotalWeight(basics.Round(100), basics.Round(101))
	require.Error(t, err)
	require.Equal(t, uint64(0), totalWeight)

	// Verify it's a DaemonError and can be extracted with errors.As
	var daemonErr *ledgercore.DaemonError
	require.ErrorAs(t, err, &daemonErr)
	require.Equal(t, "not_found", daemonErr.Code)
	require.Equal(t, "round not available", daemonErr.Msg)

	// Verify IsDaemonError helper works
	require.True(t, ledgercore.IsDaemonError(err, "not_found"))
	require.False(t, ledgercore.IsDaemonError(err, "internal"))
}

// TestTotalWeightMissingField tests that TotalWeight returns an error if the field is missing.
func TestTotalWeightMissingField(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		return map[string]interface{}{
			"something": "else",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	_, err := client.TotalWeight(basics.Round(100), basics.Round(101))
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing total_weight field")
}

// TestTotalWeightInvalidValue tests that TotalWeight returns an error for non-numeric value.
func TestTotalWeightInvalidValue(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		return map[string]interface{}{
			"total_weight": "not-a-number",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	_, err := client.TotalWeight(basics.Round(100), basics.Round(101))
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid total_weight value")
}

// TestTotalWeightNegativeValue tests that TotalWeight returns an error for negative value.
func TestTotalWeightNegativeValue(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		return map[string]interface{}{
			"total_weight": "-100",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	_, err := client.TotalWeight(basics.Round(100), basics.Round(101))
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid total_weight value")
}

// TestTotalWeightCacheHit tests that repeated TotalWeight queries with the same parameters
// return cached results and don't hit the daemon.
func TestTotalWeightCacheHit(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	var queryCount atomic.Int32

	testBalanceRound := basics.Round(1000)
	testVoteRound := basics.Round(1001)

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		queryCount.Add(1)
		return map[string]interface{}{
			"total_weight": "123456",
		}
	})
	defer server.Close()

	client := NewClient(server.port)

	// First query - should hit daemon
	totalWeight1, err := client.TotalWeight(testBalanceRound, testVoteRound)
	require.NoError(t, err)
	require.Equal(t, uint64(123456), totalWeight1)
	require.Equal(t, int32(1), queryCount.Load())

	// Second query with same parameters - should hit cache
	totalWeight2, err := client.TotalWeight(testBalanceRound, testVoteRound)
	require.NoError(t, err)
	require.Equal(t, uint64(123456), totalWeight2)
	require.Equal(t, int32(1), queryCount.Load()) // Still 1, no additional query
}

// TestTotalWeightCacheMiss tests that TotalWeight queries with different parameters
// don't share cache entries.
func TestTotalWeightCacheMiss(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	var queryCount atomic.Int32

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		queryCount.Add(1)
		// Return different total weights based on balance_round
		balanceRound := req["balance_round"].(string)
		if balanceRound == "100" {
			return map[string]interface{}{"total_weight": "100"}
		}
		return map[string]interface{}{"total_weight": "200"}
	})
	defer server.Close()

	client := NewClient(server.port)

	// Query for balance round 100
	totalWeight1, err := client.TotalWeight(basics.Round(100), basics.Round(101))
	require.NoError(t, err)
	require.Equal(t, uint64(100), totalWeight1)
	require.Equal(t, int32(1), queryCount.Load())

	// Query for balance round 200 - different round means cache miss
	totalWeight2, err := client.TotalWeight(basics.Round(200), basics.Round(201))
	require.NoError(t, err)
	require.Equal(t, uint64(200), totalWeight2)
	require.Equal(t, int32(2), queryCount.Load())

	// Query for balance round 100 again - cache hit
	totalWeight3, err := client.TotalWeight(basics.Round(100), basics.Round(101))
	require.NoError(t, err)
	require.Equal(t, uint64(100), totalWeight3)
	require.Equal(t, int32(2), queryCount.Load()) // Still 2
}

// TestTotalWeightCacheDifferentKeys tests that cache entries are keyed by both parameters.
func TestTotalWeightCacheDifferentKeys(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	var queryCount atomic.Int32

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		queryCount.Add(1)
		return map[string]interface{}{"total_weight": "1000"}
	})
	defer server.Close()

	client := NewClient(server.port)

	// All these should be cache misses (different combinations)
	_, err := client.TotalWeight(basics.Round(100), basics.Round(101))
	require.NoError(t, err)
	require.Equal(t, int32(1), queryCount.Load())

	_, err = client.TotalWeight(basics.Round(100), basics.Round(102)) // Different voteRound
	require.NoError(t, err)
	require.Equal(t, int32(2), queryCount.Load())

	_, err = client.TotalWeight(basics.Round(200), basics.Round(101)) // Different balanceRound
	require.NoError(t, err)
	require.Equal(t, int32(3), queryCount.Load())

	// These should be cache hits
	_, err = client.TotalWeight(basics.Round(100), basics.Round(101))
	require.NoError(t, err)
	require.Equal(t, int32(3), queryCount.Load())

	_, err = client.TotalWeight(basics.Round(100), basics.Round(102))
	require.NoError(t, err)
	require.Equal(t, int32(3), queryCount.Load())
}

// TestTotalWeightConcurrent tests that multiple concurrent TotalWeight requests work correctly.
func TestTotalWeightConcurrent(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		// Small delay to increase contention
		time.Sleep(1 * time.Millisecond)
		return map[string]interface{}{
			"total_weight": "999",
		}
	})
	defer server.Close()

	client := NewClient(server.port)

	const numRequests = 20
	var wg sync.WaitGroup
	wg.Add(numRequests)
	errs := make(chan error, numRequests)
	weights := make(chan uint64, numRequests)

	for i := 0; i < numRequests; i++ {
		go func(idx int) {
			defer wg.Done()
			// Use different rounds to avoid cache hits, testing true concurrency
			totalWeight, err := client.TotalWeight(basics.Round(idx), basics.Round(idx+1))
			if err != nil {
				errs <- err
				return
			}
			weights <- totalWeight
		}(i)
	}

	wg.Wait()
	close(errs)
	close(weights)

	for err := range errs {
		t.Errorf("unexpected error: %v", err)
	}

	for weight := range weights {
		require.Equal(t, uint64(999), weight)
	}
}

// TestTotalWeightWireFormat tests the exact wire format sent to the daemon.
func TestTotalWeightWireFormat(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	server := newTestServerWithPath(t, func(path string, req map[string]interface{}) interface{} {
		// Verify wire format
		require.Equal(t, "/total_weight", path)
		require.Equal(t, "12345", req["balance_round"])
		require.Equal(t, "12346", req["vote_round"])

		return map[string]interface{}{
			"total_weight": "42",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	totalWeight, err := client.TotalWeight(basics.Round(12345), basics.Round(12346))
	require.NoError(t, err)
	require.Equal(t, uint64(42), totalWeight)
}

// ============================================================================
// Identity Tests
// ============================================================================

// makeTestGenesisHash creates a deterministic test genesis hash.
func makeTestGenesisHash() crypto.Digest {
	var hash crypto.Digest
	for i := range hash {
		hash[i] = byte(i)
	}
	return hash
}

// TestIdentitySuccess tests that Identity returns correct identity information.
func TestIdentitySuccess(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	testHash := makeTestGenesisHash()
	testHashBase64 := base64.StdEncoding.EncodeToString(testHash[:])

	server := newTestServerWithPath(t, func(path string, req map[string]interface{}) interface{} {
		require.Equal(t, "/identity", path)

		return map[string]interface{}{
			"genesis_hash":      testHashBase64,
			"protocol_version":  "1.0",
			"algorithm_version": "1.0",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	identity, err := client.Identity()
	require.NoError(t, err)
	require.Equal(t, testHash, identity.GenesisHash)
	require.Equal(t, "1.0", identity.WeightProtocolVersion)
	require.Equal(t, "1.0", identity.WeightAlgorithmVersion)
}

// TestIdentityDaemonError tests that Identity returns a DaemonError when the daemon
// returns an error response.
func TestIdentityDaemonError(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	server := newTestServerWithPath(t, func(path string, req map[string]interface{}) interface{} {
		require.Equal(t, "/identity", path)
		return map[string]interface{}{
			"error": "not configured",
			"code":  "internal",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	_, err := client.Identity()
	require.Error(t, err)

	// Verify it's a DaemonError and can be extracted with errors.As
	var daemonErr *ledgercore.DaemonError
	require.ErrorAs(t, err, &daemonErr)
	require.Equal(t, "internal", daemonErr.Code)
	require.Equal(t, "not configured", daemonErr.Msg)

	// Verify IsDaemonError helper works
	require.True(t, ledgercore.IsDaemonError(err, "internal"))
	require.False(t, ledgercore.IsDaemonError(err, "not_found"))
}

// TestIdentityMissingGenesisHash tests that Identity returns an error if genesis_hash is missing.
func TestIdentityMissingGenesisHash(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		return map[string]interface{}{
			"protocol_version":  "1.0",
			"algorithm_version": "1.0",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	_, err := client.Identity()
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing genesis_hash field")
}

// TestIdentityMissingProtocolVersion tests that Identity returns an error if protocol_version is missing.
func TestIdentityMissingProtocolVersion(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	testHash := makeTestGenesisHash()
	testHashBase64 := base64.StdEncoding.EncodeToString(testHash[:])

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		return map[string]interface{}{
			"genesis_hash":      testHashBase64,
			"algorithm_version": "1.0",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	_, err := client.Identity()
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing protocol_version field")
}

// TestIdentityMissingAlgorithmVersion tests that Identity returns an error if algorithm_version is missing.
func TestIdentityMissingAlgorithmVersion(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	testHash := makeTestGenesisHash()
	testHashBase64 := base64.StdEncoding.EncodeToString(testHash[:])

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		return map[string]interface{}{
			"genesis_hash":     testHashBase64,
			"protocol_version": "1.0",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	_, err := client.Identity()
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing algorithm_version field")
}

// TestIdentityInvalidBase64 tests that Identity returns an error for invalid base64 encoding.
func TestIdentityInvalidBase64(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		return map[string]interface{}{
			"genesis_hash":      "not-valid-base64!!!",
			"protocol_version":  "1.0",
			"algorithm_version": "1.0",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	_, err := client.Identity()
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid genesis_hash base64 encoding")
}

// TestIdentityInvalidHashLength tests that Identity returns an error for wrong hash length.
func TestIdentityInvalidHashLength(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	// Create a hash that's too short (only 16 bytes instead of 32)
	shortHash := make([]byte, 16)
	for i := range shortHash {
		shortHash[i] = byte(i)
	}
	shortHashBase64 := base64.StdEncoding.EncodeToString(shortHash)

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		return map[string]interface{}{
			"genesis_hash":      shortHashBase64,
			"protocol_version":  "1.0",
			"algorithm_version": "1.0",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	_, err := client.Identity()
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid genesis_hash length")
	require.Contains(t, err.Error(), "expected 32 bytes")
	require.Contains(t, err.Error(), "got 16")
}

// TestIdentityHashTooLong tests that Identity returns an error for hash that's too long.
func TestIdentityHashTooLong(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	// Create a hash that's too long (64 bytes instead of 32)
	longHash := make([]byte, 64)
	for i := range longHash {
		longHash[i] = byte(i)
	}
	longHashBase64 := base64.StdEncoding.EncodeToString(longHash)

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		return map[string]interface{}{
			"genesis_hash":      longHashBase64,
			"protocol_version":  "1.0",
			"algorithm_version": "1.0",
		}
	})
	defer server.Close()

	client := NewClient(server.port)
	_, err := client.Identity()
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid genesis_hash length")
	require.Contains(t, err.Error(), "expected 32 bytes")
	require.Contains(t, err.Error(), "got 64")
}

// TestIdentityConcurrent tests that multiple concurrent Identity requests work correctly.
func TestIdentityConcurrent(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	testHash := makeTestGenesisHash()
	testHashBase64 := base64.StdEncoding.EncodeToString(testHash[:])

	server := newTestServer(t, func(req map[string]interface{}) interface{} {
		// Small delay to increase contention
		time.Sleep(1 * time.Millisecond)
		return map[string]interface{}{
			"genesis_hash":      testHashBase64,
			"protocol_version":  "1.0",
			"algorithm_version": "1.0",
		}
	})
	defer server.Close()

	client := NewClient(server.port)

	const numRequests = 10
	var wg sync.WaitGroup
	wg.Add(numRequests)
	errs := make(chan error, numRequests)
	results := make(chan ledgercore.DaemonIdentity, numRequests)

	for i := 0; i < numRequests; i++ {
		go func() {
			defer wg.Done()
			identity, err := client.Identity()
			if err != nil {
				errs <- err
				return
			}
			results <- identity
		}()
	}

	wg.Wait()
	close(errs)
	close(results)

	for err := range errs {
		t.Errorf("unexpected error: %v", err)
	}

	for identity := range results {
		require.Equal(t, testHash, identity.GenesisHash)
		require.Equal(t, "1.0", identity.WeightProtocolVersion)
		require.Equal(t, "1.0", identity.WeightAlgorithmVersion)
	}
}
