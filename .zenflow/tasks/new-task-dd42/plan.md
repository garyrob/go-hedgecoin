# Spec and build

## Configuration
- **Artifacts Path**: {@artifacts_path} → `.zenflow/tasks/{task_id}`

---

## Agent Instructions

Ask the user questions when anything is unclear or needs their input. This includes:
- Ambiguous or incomplete requirements
- Technical decisions that affect architecture or user experience
- Trade-offs that require business context

Do not make assumptions on important decisions — get clarification first.

---

## Workflow Steps

### [x] Step: Technical Specification

**Difficulty:** Medium

Created technical specification in `spec.md` covering:
- TCP/JSON client implementing `ledgercore.WeightOracle` interface
- Generic bounded LRU cache for weight and total weight queries
- Wire format details (decimal strings, Base32 addresses, hex selection IDs)
- Error handling with `*DaemonError` propagation
- Python mock daemon for integration testing

---

### [ ] Step: LRU Cache Implementation

Create the generic bounded LRU cache in `node/weightoracle/lru.go`:
- Doubly-linked list with hash map for O(1) operations
- `Get()` moves accessed node to front (requires `sync.Mutex`, not `RWMutex`)
- `Put()` evicts tail when at capacity
- Comprehensive unit tests in `node/weightoracle/lru_test.go`

Verification: `go test -v -race ./node/weightoracle/...`

---

### [ ] Step: Client Core and Ping Implementation

Create `node/weightoracle/client.go` with:
- `Client` struct with port, timeout, and cache fields
- `NewClient()` constructor with functional options
- `query()` helper for TCP connect, JSON encode/decode, timeout handling
- `Ping()` implementation as simplest query type
- Error handling: network errors vs `*DaemonError`

Unit tests for:
- Ping query encoding and response parsing
- Timeout handling
- Connection errors

Verification: `go test -v -race ./node/weightoracle/...`

---

### [ ] Step: Weight Query Implementation

Implement `Weight()` in `client.go`:
- Wire format encoding (address as Base32, selectionID as hex, round as decimal string)
- Response parsing with decimal string to uint64 conversion
- `*DaemonError` handling for error responses
- LRU cache integration

Unit tests for:
- Weight query wire format
- Success response parsing
- Error response handling (`not_found`, `internal`, `bad_request`)
- Cache hit/miss behavior
- `errors.As` extraction of `DaemonError`

Verification: `go test -v -race ./node/weightoracle/...`

---

### [ ] Step: Total Weight and Identity Implementation

Implement remaining methods in `client.go`:
- `TotalWeight()` with caching and two-round parameters
- `Identity()` with base64 genesis hash decoding and length validation

Unit tests for:
- Total weight query wire format and response parsing
- Total weight cache behavior
- Identity query encoding and response parsing
- Base64 decoding success and failure cases
- Genesis hash length validation

Verification: `go test -v -race ./node/weightoracle/...`

---

### [ ] Step: Python Mock Daemon

Create `test/testdata/weightdaemon/daemon.py`:
- TCP server accepting JSON-over-TCP connections
- Support all four query types: weight, total_weight, ping, identity
- Configuration via command line: `--port`, `--latency`, `--weights`, `--genesis-hash`
- Concurrent connection support
- Error injection capabilities for testing
- PID file and "READY" output for test lifecycle management

Manual verification:
```bash
python test/testdata/weightdaemon/daemon.py --port 9999 &
echo '{"type": "ping"}' | nc localhost 9999
```

---

### [ ] Step: Final Verification and Report

Run full verification suite:
- `make build` — ensure clean compilation
- `make lint` — pass all lint checks
- `go test -v -race ./node/weightoracle/...` — all tests pass

Write completion report to `report.md`:
- What was implemented
- How the solution was tested
- Challenges encountered
