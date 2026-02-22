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

4. **JSON body format**: The request body structure is simplified since the endpoint path now determines the request type. The `type` field used in the current TCP protocol is **removed** from request bodies. The endpoint path replaces this routing mechanism.

5. **Response handling**: Ensure response body is fully read and closed to return connection to pool.

6. **HTTP status code handling**: The client must check HTTP status codes before attempting to parse JSON. Non-2xx responses should be handled appropriately, attempting to parse error JSON from the body when possible.

7. **Dynamic timeouts**: Use per-request context with deadline for timeout control, since `http.Client.Timeout` is set at construction time.

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

### Request/Response Bodies

**Note**: The `type` field is **removed** from request bodies since the HTTP endpoint path now determines the request type.

**Ping** (`POST /ping`)
```json
// Request body (empty or {})
{}
// Response
{"pong": true}
```

**Identity** (`POST /identity`)
```json
// Request body (empty or {})
{}
// Response
{"genesis_hash": "<base64>", "protocol_version": "1.0", "algorithm_version": "1.0"}
```

**Weight** (`POST /weight`)
```json
// Request (type field removed, only data fields)
{"address": "<base32>", "selection_id": "<hex64>", "balance_round": "<decimal>"}
// Response
{"weight": "<decimal>"}
```

**Total Weight** (`POST /total_weight`)
```json
// Request (type field removed, only data fields)
{"balance_round": "<decimal>", "vote_round": "<decimal>"}
// Response
{"total_weight": "<decimal>"}
```

**Error Response (any endpoint)**
```json
{"error": "<message>", "code": "<code>"}
```

Error responses are always JSON, regardless of HTTP status code.

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

### Request Struct Changes

Remove the `Type` field from request structs since the endpoint path now determines request type:

```go
// pingRequest is empty - the endpoint /ping identifies the request type
type pingRequest struct{}

// identityRequest is empty - the endpoint /identity identifies the request type
type identityRequest struct{}

// weightRequest no longer needs Type field
type weightRequest struct {
    Address      string `json:"address"`
    SelectionID  string `json:"selection_id"`
    BalanceRound string `json:"balance_round"`
}

// totalWeightRequest no longer needs Type field
type totalWeightRequest struct {
    BalanceRound string `json:"balance_round"`
    VoteRound    string `json:"vote_round"`
}
```

### Constructor Changes

```go
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
```

### Query Method Changes

Replace `query()` with HTTP-based implementation that handles status codes properly:

```go
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
        return fmt.Errorf("failed to read response body: %w", err)
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
```

### Key Implementation Notes

1. **Always close response body**: Use `defer resp.Body.Close()` immediately after checking error
2. **Read body fully with io.ReadAll**: Ensures connection can be reused, even for error responses
3. **Check HTTP status codes**: Handle non-2xx responses before attempting to parse success response
4. **Per-request timeout via context**: Use `http.NewRequestWithContext` with timeout context for dynamic timeout control
5. **Reuse http.Client**: Create once in constructor, reuse for all requests
6. **Configure Transport**: Set dial timeout and idle connection limits

## Python Daemon Implementation Details

### Server Class Changes

Replace `socket` server with `http.server`:

```python
from http.server import HTTPServer, BaseHTTPRequestHandler
import json

class WeightDaemonHandler(BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        # Suppress default HTTP logging to stderr (optional)
        pass

    def _send_json_response(self, status_code: int, response: dict) -> None:
        """Send a JSON response with the given status code."""
        self.send_response(status_code)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(json.dumps(response).encode())

    def _send_json_error(self, status_code: int, message: str, code: str) -> None:
        """Send a JSON error response (always JSON, not HTML)."""
        self._send_json_response(status_code, {"error": message, "code": code})

    def do_POST(self):
        # Read request body
        content_length = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(content_length)

        try:
            request = json.loads(body) if body else {}
        except json.JSONDecodeError as e:
            self._send_json_error(400, f"Invalid JSON: {e}", "bad_request")
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
            self._send_json_error(404, f"Unknown endpoint: {self.path}", "not_found")
            return

        # Check if handler returned an error response
        if "error" in response:
            # Map error codes to HTTP status codes
            code = response.get("code", "internal")
            status = 400 if code == "bad_request" else 404 if code == "not_found" else 500
            self._send_json_response(status, response)
        else:
            self._send_json_response(200, response)
```

### Graceful Shutdown

The daemon should use `HTTPServer.shutdown()` for clean termination:

```python
def start(self) -> None:
    """Start the daemon server."""
    self.server = HTTPServer(("127.0.0.1", self.port), WeightDaemonHandler)
    self.server.daemon = self  # Attach daemon instance for handlers to access
    print(f"Weight daemon listening on http://127.0.0.1:{self.port}", file=sys.stderr)
    self.server.serve_forever()

def stop(self) -> None:
    """Stop the daemon server gracefully."""
    if hasattr(self, 'server'):
        self.server.shutdown()
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

### [ ] Step 1: Update Go Client and Test Server Together

Since the client and test server must match, update both simultaneously:

1. Modify `node/weightoracle/client.go`:
   - Add `http.Client` field to `Client` struct
   - Remove `Type` field from request structs (endpoint path replaces it)
   - Update `NewClient()` to initialize HTTP client with transport config
   - Replace `query()` method with `doRequest()` using HTTP POST with proper status code handling
   - Update `Ping()`, `Weight()`, `TotalWeight()`, `Identity()` to use new method with correct endpoint paths
   - Update `SetTimeouts()` to modify `queryTimeout` (used in per-request context)

2. Modify `node/weightoracle/client_test.go`:
   - Replace `testServer` with `httptest.Server`
   - Replace `slowTestServer` with slow HTTP test server
   - Update handler functions to route by URL path instead of `type` field
   - Update assertions to account for JSON body changes (no `type` field)

3. Run tests to verify:
   ```bash
   go test -v ./node/weightoracle/
   ```

### [ ] Step 2: Update Python Daemon

1. Modify `node/weightoracle/testdaemon/daemon.py`:
   - Replace `socket` server with `http.server.HTTPServer`
   - Create `BaseHTTPRequestHandler` subclass for routing
   - Implement POST handlers for `/ping`, `/identity`, `/weight`, `/total_weight`
   - Always return JSON for error responses (not HTML)
   - Use `HTTPServer.shutdown()` for graceful termination
   - Maintain backward compatibility with command-line arguments

2. Test manually:
   ```bash
   python3 node/weightoracle/testdaemon/daemon.py --port 9876
   curl -X POST http://localhost:9876/ping -H "Content-Type: application/json" -d '{}'
   curl -X POST http://localhost:9876/identity -H "Content-Type: application/json" -d '{}'
   ```

### [ ] Step 3: Run E2E Tests

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

### [ ] Step 4: Code Quality Checks

1. Run sanity checks:
   ```bash
   make sanity
   ```

2. Fix any formatting or linting issues
