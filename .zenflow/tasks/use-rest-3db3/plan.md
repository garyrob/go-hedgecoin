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

### [ ] Step: Update Go Client and Test Server

Update both together since they must match:

**client.go changes:**
- Add `http.Client` field with connection pool configuration
- Remove `Type` field from request structs (endpoint path replaces it)
- Replace raw TCP `query()` method with HTTP POST-based `doRequest()` with proper status code handling
- Use per-request context for dynamic timeouts
- Update all methods (Ping, Weight, TotalWeight, Identity) with endpoint paths

**client_test.go changes:**
- Replace `testServer` (TCP) with `httptest.Server`
- Replace `slowTestServer` with slow HTTP test server
- Update handler functions to route by URL path instead of `type` field
- Run unit tests: `go test -v ./node/weightoracle/`

---

### [ ] Step: Update Python Daemon

Modify `node/weightoracle/testdaemon/daemon.py`:
- Replace `socket` server with `http.server.HTTPServer`
- Create `BaseHTTPRequestHandler` subclass for routing
- Implement POST handlers for `/ping`, `/identity`, `/weight`, `/total_weight`
- Always return JSON for errors (not HTML)
- Use `HTTPServer.shutdown()` for graceful termination
- Manual test with `curl`

---

### [ ] Step: Run E2E Tests and Code Quality

- Build binaries: `make install`
- Run E2E test: `go test ./test/e2e-go/features/weightoracle -run TestWeightedConsensus -v -timeout=15m`
- Run sanity checks: `make sanity`
- Write report to `report.md`
