# Technical Specification: Upgrade Weight Daemon Communication to REST

## Overview

Upgrade the weight oracle client from raw JSON-over-TCP to HTTP REST using Go's `net/http` package. This change leverages Go's built-in HTTP client connection pooling for better performance through TCP connection reuse.

## Difficulty Assessment: **Medium**

- The API surface is well-defined (4 endpoints)
- Changes are localized to two main files (Go client + Python daemon)
- Existing tests provide a safety net
- The main complexity is ensuring wire format compatibility and proper connection pool usage

## Technical Context

### Language & Dependencies
- **Go**: Standard library only (`net/http`, `encoding/json`)
- **Python**: Standard library only (`http.server`, `json`)
- No new dependencies required

### Current Implementation

The Go client (`node/weightoracle/client.go`) communicates with an external weight daemon over raw TCP:

1. Opens a new TCP connection for each request via `net.DialTimeout()`
2. Sends JSON request using `json.NewEncoder(conn).Encode()`
3. Reads JSON response using `json.NewDecoder(conn).Decode()`
4. Closes connection after each request

The Python daemon (`node/weightoracle/testdaemon/daemon.py`) uses raw TCP sockets with one JSON message per connection.

### Problems with Current Approach
- **No connection reuse**: Each query opens and closes a TCP connection
- **No HTTP benefits**: Missing keep-alive, proper error codes, content-type negotiation
- **Custom framing**: JSON detection relies on finding `}` character

## Implementation Approach

### Go Client Changes

Replace raw TCP with Go's `http.Client`, which provides:
- **Automatic connection pooling**: Connections are kept alive and reused
- **HTTP/1.1 keep-alive**: Persistent connections managed transparently
- **Proper timeouts**: Both dial and request timeouts handled correctly

Key design decisions:

1. **Single `http.Client` instance per `Client`**: The `http.Client` maintains an internal connection pool. Creating a single instance and reusing it is essential for connection pooling to work.

2. **Use POST for all endpoints**: All requests carry a JSON body, making POST semantically appropriate.

3. **Endpoint design**:
   - `POST /ping` - Health check
   - `POST /identity` - Get daemon identity
   - `POST /weight` - Query individual account weight
   - `POST /total_weight` - Query total network weight

4. **JSON body format**: Maintain same JSON structure as current implementation for easy migration.

5. **Response handling**: Ensure response body is fully read and closed to return connection to pool.

### Python Daemon Changes

Replace raw TCP socket server with Python's `http.server`:

1. Use `http.server.HTTPServer` with `BaseHTTPRequestHandler`
2. Handle POST requests to each endpoint
3. Parse JSON from request body, return JSON responses
4. Proper HTTP status codes (200 for success, 400 for bad request, 404 for not found)

## Source Code Changes

### Files to Modify

| File | Change Type | Description |
|------|-------------|-------------|
| `node/weightoracle/client.go` | Modify | Replace TCP with HTTP client |
| `node/weightoracle/client_test.go` | Modify | Update test server to HTTP |
| `node/weightoracle/testdaemon/daemon.py` | Modify | Replace TCP with HTTP server |

### Files That Remain Unchanged

| File | Reason |
|------|--------|
| `ledger/ledgercore/weightoracle.go` | Interface and types unchanged |
| `node/weightoracle/lru.go` | Caching layer unchanged |
| `test/e2e-go/features/weightoracle/weighted_consensus_test.go` | Uses daemon.py; will work with HTTP automatically |

## API / Wire Protocol Changes

### Current Protocol (TCP + JSON)

```
Client → Daemon: {"type":"ping"}\n
Daemon → Client: {"pong":true}\n
[connection closed]
```

### New Protocol (HTTP/REST)

```
POST /ping HTTP/1.1
Host: 127.0.0.1:9876
Content-Type: application/json

{}

HTTP/1.1 200 OK
Content-Type: application/json

{"pong":true}
```

### Endpoint Mapping

| Current `type` | HTTP Endpoint | HTTP Method |
|----------------|---------------|-------------|
| `ping` | `/ping` | POST |
| `identity` | `/identity` | POST |
| `weight` | `/weight` | POST |
| `total_weight` | `/total_weight` | POST |

### Request/Response Bodies (Unchanged)

The JSON structure of requests and responses remains identical:

**Ping**
```json
// Request body (can be empty or {})
{}
// Response
{"pong": true}
```

**Identity**
```json
// Request body (can be empty or {})
{}
// Response
{"genesis_hash": "<base64>", "protocol_version": "1.0", "algorithm_version": "1.0"}
```

**Weight**
```json
// Request
{"address": "<base32>", "selection_id": "<hex64>", "balance_round": "<decimal>"}
// Response
{"weight": "<decimal>"}
```

**Total Weight**
```json
// Request
{"balance_round": "<decimal>", "vote_round": "<decimal>"}
// Response
{"total_weight": "<decimal>"}
```

**Error Response (any endpoint)**
```json
{"error": "<message>", "code": "<code>"}
```

### HTTP Status Codes

| Scenario | Status Code |
|----------|-------------|
| Success | 200 OK |
| Invalid JSON | 400 Bad Request |
| Missing required fields | 400 Bad Request |
| Not found (e.g., account not in table) | 404 Not Found |
| Internal error | 500 Internal Server Error |
| Unknown endpoint | 404 Not Found |

## Go Client Implementation Details

### Client Struct Changes

```go
type Client struct {
    baseURL      string
    httpClient   *http.Client  // NEW: replaces port field
    queryTimeout time.Duration
    weightCache  *lruCache[weightCacheKey, uint64]
    totalWeightCache *lruCache[totalWeightCacheKey, uint64]
}
```

### Constructor Changes

```go
func NewClient(port uint16) *Client {
    return &Client{
        baseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
        httpClient: &http.Client{
            Timeout: DefaultQueryTimeout,
            Transport: &http.Transport{
                MaxIdleConns:        10,
                MaxIdleConnsPerHost: 10,
                IdleConnTimeout:     90 * time.Second,
            },
        },
        queryTimeout:     DefaultQueryTimeout,
        weightCache:      newLRUCache[weightCacheKey, uint64](WeightCacheCapacity),
        totalWeightCache: newLRUCache[totalWeightCacheKey, uint64](TotalWeightCacheCapacity),
    }
}
```

### Query Method Changes

Replace `query()` with HTTP-based implementation:

```go
func (c *Client) doRequest(endpoint string, reqBody interface{}, result interface{}) error {
    // Marshal request body
    bodyBytes, err := json.Marshal(reqBody)
    if err != nil {
        return fmt.Errorf("failed to marshal request: %w", err)
    }

    // Create HTTP request
    req, err := http.NewRequest("POST", c.baseURL+endpoint, bytes.NewReader(bodyBytes))
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

    // CRITICAL: Read full body to enable connection reuse
    // Decode response
    if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
        return fmt.Errorf("failed to read response: %w", err)
    }

    return nil
}
```

### Key Implementation Notes

1. **Always close response body**: Use `defer resp.Body.Close()` immediately after checking error
2. **Read body fully**: `json.Decoder.Decode()` reads the full body, enabling connection reuse
3. **Reuse http.Client**: Create once in constructor, reuse for all requests
4. **Configure Transport**: Set reasonable idle connection limits

## Python Daemon Implementation Details

### Server Class Changes

Replace `socket` server with `http.server`:

```python
from http.server import HTTPServer, BaseHTTPRequestHandler
import json

class WeightDaemonHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        # Read request body
        content_length = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(content_length)

        try:
            request = json.loads(body) if body else {}
        except json.JSONDecodeError as e:
            self.send_error(400, f"Invalid JSON: {e}")
            return

        # Route to handler based on path
        if self.path == '/ping':
            response = self.server.daemon.handle_ping()
        elif self.path == '/identity':
            response = self.server.daemon.handle_identity()
        elif self.path == '/weight':
            response = self.server.daemon.handle_weight(request)
        elif self.path == '/total_weight':
            response = self.server.daemon.handle_total_weight(request)
        else:
            self.send_error(404, f"Unknown endpoint: {self.path}")
            return

        # Send response
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(json.dumps(response).encode())
```

## Verification Approach

### Unit Tests

1. **Modify test server in `client_test.go`**:
   - Replace `testServer` (TCP) with HTTP-based test server using `httptest.Server`
   - All existing test cases should pass with minimal changes

2. **Test connection reuse**:
   - Add test that makes multiple sequential requests and verifies connection pooling
   - Can use metrics or connection counting in test server

3. **Test concurrent requests**:
   - Existing `TestPingConcurrent`, `TestWeightConcurrent`, etc. should verify pooling under load

### Integration Tests

1. **Run existing E2E test**:
   ```bash
   export NODEBINDIR=~/go/bin
   export TESTDATADIR=$(pwd)/test/testdata
   export TESTDIR=/tmp
   go test ./test/e2e-go/features/weightoracle -run TestWeightedConsensus -v -timeout=10m
   ```

2. **Test with modified daemon**:
   - Start Python daemon manually
   - Use `curl` to verify endpoints work

### Manual Verification

```bash
# Start daemon
python3 node/weightoracle/testdaemon/daemon.py --port 9876

# Test ping
curl -X POST http://localhost:9876/ping -H "Content-Type: application/json" -d '{}'

# Test identity
curl -X POST http://localhost:9876/identity -H "Content-Type: application/json" -d '{}'

# Test weight
curl -X POST http://localhost:9876/weight -H "Content-Type: application/json" \
  -d '{"address":"AAAA","selection_id":"00","balance_round":"100"}'
```

## Implementation Plan

### [ ] Step 1: Update Go Client

1. Modify `node/weightoracle/client.go`:
   - Add `http.Client` field to `Client` struct
   - Update `NewClient()` to initialize HTTP client with transport config
   - Replace `query()` method with `doRequest()` using HTTP POST
   - Update `Ping()`, `Weight()`, `TotalWeight()`, `Identity()` to use new method
   - Update `SetTimeouts()` to configure HTTP client timeout

2. Verify with unit tests:
   ```bash
   go test -v ./node/weightoracle/
   ```
   (Tests will fail until test server is updated)

### [ ] Step 2: Update Go Test Server

1. Modify `node/weightoracle/client_test.go`:
   - Replace `testServer` with `httptest.Server`
   - Replace `slowTestServer` with slow HTTP test server
   - Update handler functions to use HTTP request/response patterns
   - All existing tests should pass

2. Run tests:
   ```bash
   go test -v ./node/weightoracle/
   ```

### [ ] Step 3: Update Python Daemon

1. Modify `node/weightoracle/testdaemon/daemon.py`:
   - Replace `socket` server with `http.server.HTTPServer`
   - Create `BaseHTTPRequestHandler` subclass for routing
   - Implement POST handlers for `/ping`, `/identity`, `/weight`, `/total_weight`
   - Maintain backward compatibility with command-line arguments

2. Test manually:
   ```bash
   python3 node/weightoracle/testdaemon/daemon.py --port 9876
   curl -X POST http://localhost:9876/ping
   ```

### [ ] Step 4: Run E2E Tests

1. Build binaries:
   ```bash
   make install
   ```

2. Run weighted consensus test:
   ```bash
   export NODEBINDIR=~/go/bin
   export TESTDATADIR=$(pwd)/test/testdata
   export TESTDIR=/tmp
   go test ./test/e2e-go/features/weightoracle -run TestWeightedConsensus -v -timeout=15m
   ```

### [ ] Step 5: Code Quality Checks

1. Run sanity checks:
   ```bash
   make sanity
   ```

2. Fix any formatting or linting issues
