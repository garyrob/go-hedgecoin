# Technical Specification: Task 2 — Oracle Client and Test Daemon

## Difficulty Assessment: **Medium**

This task involves building a TCP/JSON client with caching, proper error handling, and concurrent access support. It also requires creating a Python mock daemon for integration testing. The complexity comes from:
- Wire format correctness (decimal strings, address/hex encoding)
- Thread-safe LRU caching with bounded size
- Proper typed error handling (`*DaemonError` propagation)
- Timeout handling for network operations
- Creating a reusable Python test fixture

---

## Technical Context

- **Language:** Go 1.21+ (client), Python 3.x (mock daemon)
- **Codebase:** go-algorand (forked as go-hedgecoin)
- **Target Package:** `node/weightoracle/`
- **Design Document:** DD 4.5 (see `/Users/garyrob/Source/go-hedgecoin/DD_4_5.md`)
- **Dependencies:**
  - `github.com/algorand/go-algorand/ledger/ledgercore` (for `WeightOracle`, `DaemonIdentity`, `DaemonError`)
  - `github.com/algorand/go-algorand/crypto` (for `crypto.Digest`, `crypto.VRFVerifier`)
  - `github.com/algorand/go-algorand/data/basics` (for `basics.Round`, `basics.Address`)
  - Standard library: `net`, `encoding/json`, `encoding/hex`, `encoding/base64`, `sync`, `time`, `fmt`, `strconv`

---

## Implementation Approach

### Design Principles (from DD 4.5 §1.3)

1. **TCP/JSON Protocol:** Each request is a single JSON object over a new TCP connection to `127.0.0.1:<port>`; the daemon replies with a single JSON object and the client closes the connection. The daemon always runs on localhost.

2. **Wire Format:**
   - `address`: Base32 Algorand address string (standard encoding with checksum)
   - `selection_id`: Hex-encoded 32-byte VRF public key (lowercase)
   - All uint64 values (`balance_round`, `vote_round`, `weight`, `total_weight`): Decimal strings
   - `genesis_hash`: Base64-encoded 32-byte hash (standard base64, not URL-safe)

3. **Error Handling:**
   - Daemon error responses return `*ledgercore.DaemonError` with preserved `Code`
   - Network/JSON errors return standard Go errors (not `DaemonError`)
   - Callers distinguish error types via `errors.As`

4. **Caching:**
   - Bounded LRU cache for weight queries (keyed by `(addr, selectionID, balanceRound)`)
   - Bounded LRU cache for total weight queries (keyed by `(balanceRound, voteRound)`)
   - The `lruCache` is internally thread-safe with its own mutex
   - Configurable maximum cache sizes

5. **Timeouts:** Configurable query timeout for all operations.

6. **Identity Validation:** The `Identity()` method returns the daemon's identity data. Validation against expected values (`ExpectedWeightAlgorithmVersion`, `ExpectedWeightProtocolVersion`, genesis hash) is the caller's responsibility (performed in Task 7 during node startup).

---

## Source Code Structure Changes

### New Files

#### 1. `node/weightoracle/client.go`

The main `Client` struct implementing `ledgercore.WeightOracle`:

```go
// Error code constants for daemon responses
const (
    ErrCodeNotFound    = "not_found"
    ErrCodeBadRequest  = "bad_request"
    ErrCodeInternal    = "internal"
    ErrCodeUnsupported = "unsupported"
)

// Default configuration values
const (
    defaultQueryTimeout        = 5 * time.Second
    defaultWeightCacheSize     = 10000
    defaultTotalWeightCacheSize = 1000
)

type Client struct {
    port         uint16
    queryTimeout time.Duration
    weightCache  *lruCache[weightCacheKey, uint64]
    totalCache   *lruCache[totalCacheKey, uint64]
}

type weightCacheKey struct {
    addr        basics.Address
    selectionID crypto.VRFVerifier
    round       basics.Round
}

type totalCacheKey struct {
    balanceRound basics.Round
    voteRound    basics.Round
}

func NewClient(port uint16, opts ...ClientOption) *Client
func (c *Client) Weight(balanceRound basics.Round, addr basics.Address, selectionID crypto.VRFVerifier) (uint64, error)
func (c *Client) TotalWeight(balanceRound basics.Round, voteRound basics.Round) (uint64, error)
func (c *Client) Ping() error
func (c *Client) Identity() (ledgercore.DaemonIdentity, error)
func (c *Client) ClearCache()  // For testing and restart scenarios
```

**Note on mutex architecture:** The `Client` struct does not have its own mutex. The `lruCache` is internally thread-safe (has its own mutex). Since cache operations are atomic (get or put), no additional synchronization is needed at the `Client` level.

**Configuration options:**
- `WithQueryTimeout(d time.Duration)` — default 5 seconds
- `WithWeightCacheSize(n int)` — default 10,000 entries
- `WithTotalWeightCacheSize(n int)` — default 1,000 entries

**Internal helpers:**
- `query(req interface{}) (map[string]interface{}, error)` — TCP connect to `127.0.0.1:port`, JSON encode/decode, timeout handling
- `parseUint64(v interface{}) (uint64, error)` — extract string from interface{}, parse decimal to uint64
- `formatRound(r basics.Round) string` — round to decimal string

**Typed request structs** (for clean JSON marshaling):
```go
type weightRequest struct {
    Type        string `json:"type"`
    Address     string `json:"address"`
    SelectionID string `json:"selection_id"`
    BalanceRound string `json:"balance_round"`
}

type totalWeightRequest struct {
    Type         string `json:"type"`
    BalanceRound string `json:"balance_round"`
    VoteRound    string `json:"vote_round"`
}

type simpleRequest struct {
    Type string `json:"type"`
}
```

#### 2. `node/weightoracle/lru.go`

A generic bounded LRU cache (internally thread-safe):

```go
type lruCache[K comparable, V any] struct {
    maxSize int
    mu      sync.Mutex  // Protects all fields below
    items   map[K]*lruNode[K, V]
    head    *lruNode[K, V]  // Most recently used
    tail    *lruNode[K, V]  // Least recently used
}

type lruNode[K comparable, V any] struct {
    key   K
    value V
    prev  *lruNode[K, V]
    next  *lruNode[K, V]
}

func newLRUCache[K comparable, V any](maxSize int) *lruCache[K, V]
func (c *lruCache[K, V]) Get(key K) (V, bool)    // Thread-safe, moves to front
func (c *lruCache[K, V]) Put(key K, value V)     // Thread-safe, evicts LRU if at capacity
func (c *lruCache[K, V]) Clear()                  // Thread-safe, clears all entries
func (c *lruCache[K, V]) Len() int               // Thread-safe, returns current size
```

The LRU implementation uses a custom doubly-linked list (not `container/list`) with a hash map for O(1) operations. Using a custom implementation allows for type safety with generics and avoids interface{} boxing.

**Note:** `sync.Mutex` is used (not `RWMutex`) because `Get()` mutates the list order via move-to-front.

#### 3. `node/weightoracle/lru_test.go`

Unit tests for the LRU cache:

- `TestLRUBasicOperations` — get/put, cache hit/miss
- `TestLRUEviction` — insert `maxSize + 1` entries, verify oldest evicted
- `TestLRUAccessOrder` — access pattern affects eviction order
- `TestLRUClear` — verify Clear() removes all entries
- `TestLRUConcurrency` — parallel goroutines with `-race`

#### 4. `node/weightoracle/client_test.go`

Unit tests using Go test TCP servers:

**Wire format tests:**
- `TestWeightQueryEncoding` — verify JSON request structure for weight query (including lowercase hex for selection_id)
- `TestTotalWeightQueryEncoding` — verify JSON request structure for total_weight query
- `TestPingQueryEncoding` — verify JSON request structure for ping
- `TestIdentityQueryEncoding` — verify JSON request structure for identity

**Response parsing tests:**
- `TestWeightResponseParsing` — success response with decimal string
- `TestTotalWeightResponseParsing` — success response
- `TestIdentityResponseParsing` — base64 genesis hash, version strings
- `TestErrorResponseParsing` — error response with code preservation

**DaemonError propagation tests:**
- `TestDaemonErrorNotFound` — verify `*DaemonError` with `Code: "not_found"`
- `TestDaemonErrorInternal` — verify `*DaemonError` with `Code: "internal"`
- `TestDaemonErrorBadRequest` — verify `*DaemonError` with `Code: "bad_request"`
- `TestDaemonErrorUnsupported` — verify `*DaemonError` with `Code: "unsupported"`
- `TestErrorsAsExtraction` — verify `errors.As` extracts `DaemonError`

**Cache behavior tests:**
- `TestCacheHit` — cached value returned without network call (verify via call count)
- `TestCacheMiss` — cache miss triggers network call
- `TestCacheEviction` — insert `maxSize + 1` entries, verify oldest evicted
- `TestCacheLRUOrder` — access pattern affects eviction order

**Timeout tests:**
- `TestQueryTimeout` — server sleeps past timeout, client returns error
- `TestTimeoutIsNotDaemonError` — verify timeout error is standard Go error

**Concurrency tests:**
- `TestConcurrentQueries` — parallel goroutines, run with `-race`

**Identity validation tests:**
- `TestIdentityBase64Decoding` — valid base64 genesis hash decoded to 32 bytes
- `TestIdentityBase64Invalid` — invalid base64 → error
- `TestIdentityBase64WrongLength` — genesis hash wrong length → error

**Connection tests:**
- `TestConnectionRefused` — daemon not running → error
- `TestMalformedJSON` — daemon returns invalid JSON → error

#### 5. `test/testdata/weightdaemon/daemon.py`

A Python mock weight daemon for integration testing. Located in `test/testdata/` to follow Go conventions (this directory is ignored by Go tools).

```python
#!/usr/bin/env python3
"""
Mock weight daemon for go-algorand integration testing.

Usage:
    python daemon.py --port 9999
    python daemon.py --port 9999 --latency 100  # 100ms response delay
    python daemon.py --port 9999 --weights weights.json

Supports all four query types: weight, total_weight, ping, identity.
"""
```

**Features:**
- TCP server accepting JSON-over-TCP connections (one request per connection)
- Configurable port (`--port`, required)
- Optional latency injection (`--latency` in milliseconds)
- Configurable weight table (`--weights` JSON file or `--default-weight`)
- Configurable identity response (`--genesis-hash`, `--algorithm-version`, `--protocol-version`)
- Concurrent connection support (using `threading`)
- Graceful shutdown on SIGINT/SIGTERM
- PID file output (`--pidfile`) for test lifecycle management

**Weight table format (JSON):**
```json
{
  "weights": {
    "<address>:<selectionID>:<round>": "<weight>",
    ...
  },
  "total_weights": {
    "<balanceRound>:<voteRound>": "<totalWeight>",
    ...
  },
  "default_weight": "1000",
  "default_total_weight": "1000000"
}
```

**Error injection:**
- `--error-on-address <addr>` — return `not_found` for specific address
- `--error-internal-rate <0.0-1.0>` — random internal errors at given rate

**Test lifecycle management:**
- `--pidfile <path>` — write PID to file for cleanup
- Prints "READY" to stdout when listening (for test synchronization)

---

## Data Model / API / Interface Changes

### New Types

| Type | Package | Description |
|------|---------|-------------|
| `Client` | `node/weightoracle` | TCP/JSON client implementing `WeightOracle` |
| `ClientOption` | `node/weightoracle` | Functional option for client configuration |
| `lruCache[K,V]` | `node/weightoracle` | Generic bounded LRU cache (internal) |
| `weightCacheKey` | `node/weightoracle` | Cache key for per-account weights (internal) |
| `totalCacheKey` | `node/weightoracle` | Cache key for total weights (internal) |

### Interface Implementation

The `Client` type implements `ledgercore.WeightOracle`:
```go
var _ ledgercore.WeightOracle = (*Client)(nil)
```

### Constants

| Constant | Value | Description |
|----------|-------|-------------|
| `ErrCodeNotFound` | `"not_found"` | Error code for account/round not found |
| `ErrCodeBadRequest` | `"bad_request"` | Error code for malformed request |
| `ErrCodeInternal` | `"internal"` | Error code for internal daemon error |
| `ErrCodeUnsupported` | `"unsupported"` | Error code for unsupported request |
| `defaultQueryTimeout` | `5 * time.Second` | Default TCP query timeout |
| `defaultWeightCacheSize` | `10000` | Default max entries in weight cache |
| `defaultTotalWeightCacheSize` | `1000` | Default max entries in total weight cache |

---

## Wire Protocol Details

### Request Formats

**Weight query:**
```json
{
  "type": "weight",
  "address": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAY5HFKQ",
  "selection_id": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "balance_round": "12345"
}
```

**Total weight query:**
```json
{
  "type": "total_weight",
  "balance_round": "12345",
  "vote_round": "12400"
}
```

**Identity query:**
```json
{"type": "identity"}
```

**Ping query:**
```json
{"type": "ping"}
```

### Response Formats

**Weight success:**
```json
{"weight": "1000000"}
```

**Total weight success:**
```json
{"total_weight": "50000000000"}
```

**Identity success:**
```json
{
  "genesis_hash": "SGO1GKSzyE7IEPItTxCByw9x8FmnrCDexi9/cOUJOiI=",
  "protocol_version": "1.0",
  "algorithm_version": "1.0"
}
```

**Ping success:**
```json
{"pong": true}
```

**Error response (any query type):**
```json
{"error": "account not found", "code": "not_found"}
```

### Response Parsing Notes

The `query()` helper unmarshals into `map[string]interface{}`. JSON types map as follows:
- Strings → `string`
- Numbers → `float64` (but we expect decimal strings for uint64 values)
- Booleans → `bool`
- Objects → `map[string]interface{}`

For responses:
- `"weight"` and `"total_weight"` are expected as strings (decimal encoded)
- `"pong"` is expected as boolean `true`
- `"genesis_hash"`, `"protocol_version"`, `"algorithm_version"` are expected as strings
- `"error"` and `"code"` are expected as strings

---

## Verification Approach

### Build Verification
```bash
make build
```
Must compile cleanly with no errors.

### Lint Verification
```bash
make lint
```
Must pass all lint checks.

### Unit Test Verification
```bash
go test -v -race ./node/weightoracle/...
```

### Test Coverage Requirements

1. **Wire format correctness:**
   - Address encoded as Base32 with checksum
   - SelectionID encoded as lowercase hex (64 chars) — explicitly verify lowercase, not uppercase
   - Rounds encoded as decimal strings
   - Weights encoded as decimal strings

2. **Response parsing:**
   - Success responses parsed correctly
   - Error responses return `*DaemonError` with correct `Code`
   - Invalid JSON → standard Go error (not `DaemonError`)
   - Truncated response → standard Go error

3. **Cache behavior:**
   - Cache hit returns value without network call
   - Cache miss triggers network call and caches result
   - Eviction at capacity boundary removes LRU entry
   - Access order determines eviction priority

4. **Timeout handling:**
   - Slow server triggers timeout error
   - Timeout error is not `*DaemonError`

5. **Concurrency:**
   - Parallel access with `-race` flag detects no races
   - Concurrent cache access is safe

6. **Identity decoding:**
   - Valid base64 genesis hash decoded to 32 bytes
   - Invalid base64 → error
   - Wrong length → error

### Python Daemon Testing
```bash
# Start daemon
python test/testdata/weightdaemon/daemon.py --port 9999 &

# Test connectivity
echo '{"type": "ping"}' | nc localhost 9999

# Run integration tests (optional, for later tasks)
go test -v ./node/weightoracle/... -tags=integration
```

---

## Implementation Plan

Given the medium complexity, this task should be broken into the following steps:

### Step 1: LRU Cache Implementation
- Create `node/weightoracle/lru.go` with generic bounded LRU cache
- Create `node/weightoracle/lru_test.go` with comprehensive tests
- Verify with `go test -race`

### Step 2: Client Core Implementation
- Create `node/weightoracle/client.go` with basic structure
- Implement `query()` helper (TCP connect, JSON encode/decode)
- Implement `Ping()` as simplest query type
- Add timeout handling
- Write tests for ping and basic connectivity

### Step 3: Weight Query Implementation
- Implement `Weight()` with caching
- Implement wire format encoding (address, selectionID, round)
- Implement response parsing with `DaemonError` handling
- Write comprehensive unit tests

### Step 4: Total Weight and Identity Implementation
- Implement `TotalWeight()` with caching
- Implement `Identity()` with base64 decoding
- Write unit tests

### Step 5: Python Mock Daemon
- Create `test/testdata/weightdaemon/daemon.py`
- Implement all four query types
- Add configuration options (port, latency, weights file)
- Add error injection capabilities
- Add PID file and "READY" output for test lifecycle
- Test manually with netcat/telnet

### Step 6: Final Verification
- Run full test suite with race detector
- Run linter
- Verify build succeeds
- Manual integration test with Python daemon

---

## Files Summary

| File | Action | Lines (est.) |
|------|--------|--------------|
| `node/weightoracle/lru.go` | Create | ~100 |
| `node/weightoracle/lru_test.go` | Create | ~150 |
| `node/weightoracle/client.go` | Create | ~280 |
| `node/weightoracle/client_test.go` | Create | ~400 |
| `test/testdata/weightdaemon/daemon.py` | Create | ~250 |

**Total estimated new code:** ~1180 lines (Go + Python, including tests)

---

## Risk Considerations

1. **Wire format mismatch:** Careful attention to decimal string encoding for all uint64 values. JSON numbers would lose precision for large values.

2. **Address encoding:** Use `basics.Address.String()` for correct Base32 encoding with checksum.

3. **Hex encoding for SelectionID:** `crypto.VRFVerifier` is a 32-byte array; encode as lowercase hex using `encoding/hex.EncodeToString()`.

4. **Base64 decoding for genesis hash:** Standard base64 (not URL-safe). Validate decoded length is exactly 32 bytes.

5. **Cache key equality:** Ensure struct keys are comparable. `basics.Address` and `crypto.VRFVerifier` are fixed-size arrays, so they work as map keys.

6. **Connection per request:** The protocol specifies one request per TCP connection. Do not reuse connections.

7. **Localhost-only:** The daemon always runs on `127.0.0.1`. No host configuration is needed.

---

## Design Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Mutex architecture | `lruCache` internally thread-safe, no `Client.mu` | Simpler design; cache ops are atomic |
| Host configuration | Hardcoded `127.0.0.1` | Protocol requires localhost daemon |
| Response parsing | `map[string]interface{}` | Flexible for different response types |
| Identity validation | Caller's responsibility | Separation of concerns; validated in Task 7 |
| Python daemon location | `test/testdata/weightdaemon/` | Go tools ignore `testdata/` |
| LRU implementation | Custom doubly-linked list | Type safety with generics, no boxing |
| Error code constants | Defined in `client.go` | Centralized for consistency |
| Identity caching | Not cached | Called once at startup; caching not needed |
