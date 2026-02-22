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

If you are blocked and need user clarification, mark the current step with `[!]` in plan.md before stopping.

---

## Workflow Steps

### [x] Step: Technical Specification

Created `spec.md` with:
- Difficulty assessment: **Medium**
- Implementation approach using Go's `net/http` package with connection pooling
- Files to modify: `client.go`, `client_test.go`, `daemon.py`
- HTTP REST API design with 4 endpoints
- Detailed implementation plan broken into 5 steps

---

### [ ] Step: Update Go Client

Modify `node/weightoracle/client.go`:
- Add `http.Client` field with connection pool configuration
- Replace raw TCP `query()` method with HTTP POST-based `doRequest()`
- Update all methods (Ping, Weight, TotalWeight, Identity) to use HTTP
- Update `SetTimeouts()` for HTTP client

---

### [ ] Step: Update Go Test Server

Modify `node/weightoracle/client_test.go`:
- Replace `testServer` (TCP) with `httptest.Server`
- Replace `slowTestServer` with slow HTTP test server
- Update all handler functions for HTTP request/response patterns
- Run unit tests: `go test -v ./node/weightoracle/`

---

### [ ] Step: Update Python Daemon

Modify `node/weightoracle/testdaemon/daemon.py`:
- Replace `socket` server with `http.server.HTTPServer`
- Create `BaseHTTPRequestHandler` subclass for routing
- Implement POST handlers for `/ping`, `/identity`, `/weight`, `/total_weight`
- Manual test with `curl`

---

### [ ] Step: Run E2E Tests and Code Quality

- Build binaries: `make install`
- Run E2E test: `go test ./test/e2e-go/features/weightoracle -run TestWeightedConsensus -v -timeout=15m`
- Run sanity checks: `make sanity`
- Write report to `report.md`
