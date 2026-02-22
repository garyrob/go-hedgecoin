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
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/algorand/go-algorand/crypto"
	"github.com/algorand/go-algorand/data/basics"
	"github.com/algorand/go-algorand/ledger/ledgercore"
)

const (
	// DefaultDialTimeout is the timeout for establishing a TCP connection to the daemon.
	DefaultDialTimeout = 5 * time.Second

	// DefaultQueryTimeout is the timeout for a complete query (send request + receive response).
	DefaultQueryTimeout = 10 * time.Second

	// WeightCacheCapacity is the maximum number of weight query results to cache.
	WeightCacheCapacity = 10000

	// TotalWeightCacheCapacity is the maximum number of total weight query results to cache.
	TotalWeightCacheCapacity = 1000
)

// weightCacheKey is the key for the weight LRU cache.
// It combines all parameters that uniquely identify a weight query.
type weightCacheKey struct {
	balanceRound basics.Round
	addr         basics.Address
	selectionID  crypto.VRFVerifier
}

// totalWeightCacheKey is the key for the total weight LRU cache.
// It combines both round parameters that uniquely identify a total weight query.
type totalWeightCacheKey struct {
	balanceRound basics.Round
	voteRound    basics.Round
}

// Client implements ledgercore.WeightOracle by communicating with an external
// weight daemon over HTTP REST.
type Client struct {
	baseURL      string
	httpClient   *http.Client
	queryTimeout time.Duration

	// weightCache caches weight query results to reduce daemon queries.
	// Key: (balanceRound, addr, selectionID), Value: weight (uint64)
	weightCache *lruCache[weightCacheKey, uint64]

	// totalWeightCache caches total weight query results to reduce daemon queries.
	// Key: (balanceRound, voteRound), Value: totalWeight (uint64)
	totalWeightCache *lruCache[totalWeightCacheKey, uint64]
}

// Compile-time interface check
var _ ledgercore.WeightOracle = (*Client)(nil)

// NewClient creates a new weight oracle client that connects to the daemon
// at 127.0.0.1 on the specified port.
func NewClient(port uint16) *Client {
	return &Client{
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		httpClient: &http.Client{
			// Note: Timeout is not set here; we use per-request context for dynamic timeouts
			Transport: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
				DialContext: (&net.Dialer{
					Timeout: DefaultDialTimeout,
				}).DialContext,
			},
		},
		queryTimeout:     DefaultQueryTimeout,
		weightCache:      newLRUCache[weightCacheKey, uint64](WeightCacheCapacity),
		totalWeightCache: newLRUCache[totalWeightCacheKey, uint64](TotalWeightCacheCapacity),
	}
}

// SetTimeouts configures custom query timeout for the client.
// This is primarily intended for testing. Pass 0 to keep the current value.
// Note: dialTimeout parameter is accepted for backward compatibility but has no effect
// after client creation since it's baked into the HTTP Transport.
func (c *Client) SetTimeouts(dialTimeout, queryTimeout time.Duration) {
	// dialTimeout is ignored - it's fixed at construction time in the Transport
	_ = dialTimeout
	if queryTimeout > 0 {
		c.queryTimeout = queryTimeout
	}
}

// emptyRequest is used for endpoints that don't require request parameters.
type emptyRequest struct{}

// pingResponse is the expected response from a ping query.
type pingResponse struct {
	Pong  bool   `json:"pong,omitempty"`
	Error string `json:"error,omitempty"`
	Code  string `json:"code,omitempty"`
}

// weightRequest is the JSON structure sent for a weight query.
// The endpoint path (/weight) identifies the request type.
type weightRequest struct {
	Address      string `json:"address"`
	SelectionID  string `json:"selection_id"`
	BalanceRound string `json:"balance_round"`
}

// weightResponse is the expected response from a weight query.
type weightResponse struct {
	Weight string `json:"weight,omitempty"`
	Error  string `json:"error,omitempty"`
	Code   string `json:"code,omitempty"`
}

// totalWeightRequest is the JSON structure sent for a total_weight query.
// The endpoint path (/total_weight) identifies the request type.
type totalWeightRequest struct {
	BalanceRound string `json:"balance_round"`
	VoteRound    string `json:"vote_round"`
}

// totalWeightResponse is the expected response from a total_weight query.
type totalWeightResponse struct {
	TotalWeight string `json:"total_weight,omitempty"`
	Error       string `json:"error,omitempty"`
	Code        string `json:"code,omitempty"`
}

// identityResponse is the expected response from an identity query.
type identityResponse struct {
	GenesisHash      string `json:"genesis_hash,omitempty"`
	ProtocolVersion  string `json:"protocol_version,omitempty"`
	AlgorithmVersion string `json:"algorithm_version,omitempty"`
	Error            string `json:"error,omitempty"`
	Code             string `json:"code,omitempty"`
}

// doRequest sends an HTTP POST request to the daemon and decodes the response.
// It uses Go's http.Client which maintains a connection pool for efficiency.
// The response is decoded into the provided result struct.
func (c *Client) doRequest(endpoint string, reqBody interface{}, result interface{}) error {
	// Marshal request body
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request with timeout context
	ctx, cancel := context.WithTimeout(context.Background(), c.queryTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to weight daemon: %w", err)
	}
	defer resp.Body.Close()

	// Read full body to enable connection reuse (even for errors)
	bodyData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response from weight daemon: %w", err)
	}

	// Handle non-2xx status codes
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Try to parse JSON error from body
		var errResp struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		if json.Unmarshal(bodyData, &errResp) == nil && errResp.Error != "" {
			return &ledgercore.DaemonError{
				Code: errResp.Code,
				Msg:  errResp.Error,
			}
		}
		return fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(bodyData))
	}

	// Decode successful response
	if err := json.Unmarshal(bodyData, result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	return nil
}

// Ping checks if the daemon is reachable and healthy.
func (c *Client) Ping() error {
	req := emptyRequest{}
	var resp pingResponse

	if err := c.doRequest("/ping", req, &resp); err != nil {
		return err
	}

	// Check for error response
	if resp.Error != "" {
		return &ledgercore.DaemonError{
			Code: resp.Code,
			Msg:  resp.Error,
		}
	}

	// Verify we got a pong
	if !resp.Pong {
		return fmt.Errorf("unexpected ping response: pong field is false or missing")
	}

	return nil
}

// Weight returns the consensus weight for the given account at the specified balance round.
// Results are cached using an LRU cache to reduce daemon queries.
func (c *Client) Weight(balanceRound basics.Round, addr basics.Address, selectionID crypto.VRFVerifier) (uint64, error) {
	// Check cache first
	cacheKey := weightCacheKey{
		balanceRound: balanceRound,
		addr:         addr,
		selectionID:  selectionID,
	}
	if weight, ok := c.weightCache.Get(cacheKey); ok {
		return weight, nil
	}

	// Build request with wire format:
	// - address: Base32 encoded (using addr.String())
	// - selection_id: hex-encoded (32 bytes = 64 hex chars)
	// - balance_round: decimal string
	req := weightRequest{
		Address:      addr.String(),
		SelectionID:  hex.EncodeToString(selectionID[:]),
		BalanceRound: strconv.FormatUint(uint64(balanceRound), 10),
	}

	var resp weightResponse
	if err := c.doRequest("/weight", req, &resp); err != nil {
		return 0, err
	}

	// Check for error response
	if resp.Error != "" {
		return 0, &ledgercore.DaemonError{
			Code: resp.Code,
			Msg:  resp.Error,
		}
	}

	// Parse weight as decimal string
	if resp.Weight == "" {
		return 0, fmt.Errorf("weight response missing weight field")
	}
	weight, err := strconv.ParseUint(resp.Weight, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid weight value %q: %w", resp.Weight, err)
	}

	// Cache the result
	c.weightCache.Put(cacheKey, weight)

	return weight, nil
}

// TotalWeight returns the total consensus weight at the specified balance round for voting
// in the given vote round. Results are cached using an LRU cache to reduce daemon queries.
func (c *Client) TotalWeight(balanceRound basics.Round, voteRound basics.Round) (uint64, error) {
	// Check cache first
	cacheKey := totalWeightCacheKey{
		balanceRound: balanceRound,
		voteRound:    voteRound,
	}
	if totalWeight, ok := c.totalWeightCache.Get(cacheKey); ok {
		return totalWeight, nil
	}

	// Build request with wire format:
	// - balance_round: decimal string
	// - vote_round: decimal string
	req := totalWeightRequest{
		BalanceRound: strconv.FormatUint(uint64(balanceRound), 10),
		VoteRound:    strconv.FormatUint(uint64(voteRound), 10),
	}

	var resp totalWeightResponse
	if err := c.doRequest("/total_weight", req, &resp); err != nil {
		return 0, err
	}

	// Check for error response
	if resp.Error != "" {
		return 0, &ledgercore.DaemonError{
			Code: resp.Code,
			Msg:  resp.Error,
		}
	}

	// Parse total_weight as decimal string
	if resp.TotalWeight == "" {
		return 0, fmt.Errorf("total_weight response missing total_weight field")
	}
	totalWeight, err := strconv.ParseUint(resp.TotalWeight, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid total_weight value %q: %w", resp.TotalWeight, err)
	}

	// Cache the result
	c.totalWeightCache.Put(cacheKey, totalWeight)

	return totalWeight, nil
}

// Identity returns metadata about the daemon including genesis hash and version information.
// The genesis hash is returned as base64-encoded in the wire protocol and decoded to a crypto.Digest.
func (c *Client) Identity() (ledgercore.DaemonIdentity, error) {
	req := emptyRequest{}
	var resp identityResponse

	if err := c.doRequest("/identity", req, &resp); err != nil {
		return ledgercore.DaemonIdentity{}, err
	}

	// Check for error response
	if resp.Error != "" {
		return ledgercore.DaemonIdentity{}, &ledgercore.DaemonError{
			Code: resp.Code,
			Msg:  resp.Error,
		}
	}

	// Validate required fields are present
	if resp.GenesisHash == "" {
		return ledgercore.DaemonIdentity{}, fmt.Errorf("identity response missing genesis_hash field")
	}
	if resp.ProtocolVersion == "" {
		return ledgercore.DaemonIdentity{}, fmt.Errorf("identity response missing protocol_version field")
	}
	if resp.AlgorithmVersion == "" {
		return ledgercore.DaemonIdentity{}, fmt.Errorf("identity response missing algorithm_version field")
	}

	// Decode base64 genesis hash
	genesisBytes, err := base64.StdEncoding.DecodeString(resp.GenesisHash)
	if err != nil {
		return ledgercore.DaemonIdentity{}, fmt.Errorf("invalid genesis_hash base64 encoding: %w", err)
	}

	// Validate genesis hash length (crypto.Digest is 32 bytes)
	if len(genesisBytes) != crypto.DigestSize {
		return ledgercore.DaemonIdentity{}, fmt.Errorf("invalid genesis_hash length: expected %d bytes, got %d", crypto.DigestSize, len(genesisBytes))
	}

	var genesisHash crypto.Digest
	copy(genesisHash[:], genesisBytes)

	return ledgercore.DaemonIdentity{
		GenesisHash:            genesisHash,
		WeightAlgorithmVersion: resp.AlgorithmVersion,
		WeightProtocolVersion:  resp.ProtocolVersion,
	}, nil
}
