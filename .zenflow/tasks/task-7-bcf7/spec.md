# Task 7: Ledger Wiring, Startup Validation, and Integration

## Technical Specification

### Task Difficulty: **HARD**

This task is complex because:
- It requires implementing the weight oracle client (Task 2 was not implemented)
- It involves critical startup validation that affects consensus safety
- It requires integration testing with a Python mock daemon
- The startup sequence must be carefully ordered to ensure oracle is validated before consensus starts
- Participation key weight validation has multiple edge cases (eligibility gating, round computation)

---

## Technical Context

### Language & Dependencies
- **Language**: Go 1.21+
- **Key Packages**:
  - `github.com/algorand/go-algorand/node` - Node startup
  - `github.com/algorand/go-algorand/ledger` - Ledger ExternalWeighter implementation
  - `github.com/algorand/go-algorand/ledger/ledgercore` - WeightOracle interface
  - `github.com/algorand/go-algorand/data/account` - Participation registry
  - `github.com/algorand/go-algorand/agreement` - BalanceRound/ParamsRound computation
- **External**: Python 3.x for test daemon

### Prerequisites Verification

**Completed in prior tasks:**
- [x] Task 1: `ledgercore.WeightOracle`, `DaemonError`, `DaemonIdentity`, `ExternalWeighter` interfaces
- [x] Task 3: `committee.Membership` has `ExternalWeight`/`TotalExternalWeight` fields; config has `ExternalWeightOraclePort`
- [x] Task 4: Credential verification uses weight-based sortition
- [x] Task 5: `agreement/selector.go::membership()` queries ExternalWeighter
- [x] Task 6: Absenteeism uses weight-based thresholds

**NOT completed (must be implemented in Task 7):**
- [ ] Task 2: Oracle client (`node/weightoracle/client.go`) - **CRITICAL DEPENDENCY**
- [ ] Task 2: Python test daemon (`testdaemon/daemon.py`)

**Note on Task 2:** Git history shows that Task 2 (commit `32486d0e`) only completed the
Technical Specification step. The plan.md from that task shows all implementation steps
(`[ ] LRU Cache Implementation`, `[ ] Client Core`, etc.) were never marked complete.
No `node/weightoracle/` directory exists in the codebase. The Oracle Client implementation
must therefore be done as part of Task 7.

**Existing partial implementation:**
- [x] Ledger has `weightOracle` field and `SetWeightOracle()`/`WeightOracle()` methods
- [x] Ledger implements `ExternalWeight()`/`TotalExternalWeight()` with fallback to stake
- [x] Compile-time interface check exists: `var _ ledgercore.ExternalWeighter = (*Ledger)(nil)`

**IMPORTANT - Ledger fallback behavior must be changed:**

The current `ledger/ledger.go` implementation (lines 756-794) has a **fallback to stake** when
`weightOracle == nil`. However, `implementation_tasks.md` specifies it should **panic**:

```go
if l.weightOracle == nil {
    logging.Base().Panicf("ExternalWeight called but no oracle configured")
}
```

The fallback was likely added to keep existing tests working during Tasks 5-6 implementation.
Now that Task 7 adds startup validation to ensure the oracle is always configured, the Ledger
implementation should be changed to panic per the design document. This is safe because:
- Agreement tests use `testLedger` or `mockLedgerReaderWithWeights` (mocks) which implement `ExternalWeighter` directly
- Eval tests use `evalTestLedger` (mock) which implements `ExternalWeighter` directly
- Ledgercore tests use `mockWeighter` (mock) for interface verification
- Production code will always have the oracle configured via startup validation

**No existing tests need to be modified or replaced.** All tests that exercise weight functionality
already use mock implementations that don't depend on the real Ledger's `ExternalWeight`/`TotalExternalWeight`.

---

## Implementation Approach

### Phase 1: Oracle Client Implementation (from Task 2)

Since Task 2 was not implemented, we must implement the oracle client first.

**New file:** `node/weightoracle/client.go`

```go
// Client implements ledgercore.WeightOracle
type Client struct {
    port uint16

    // Bounded LRU caches (sync.Mutex, not RWMutex - LRU get() mutates)
    weightMu    sync.Mutex
    weightCache *lruCache[cacheKey, uint64]

    totalMu    sync.Mutex
    totalCache *lruCache[totalCacheKey, uint64]
}
```

**Key implementation details:**
1. TCP/JSON protocol per DD §1.3
2. All numeric wire values are decimal strings (not JSON numbers)
3. LRU caches with bounded size (10000 for weights, 1000 for totals)
4. `*ledgercore.DaemonError` returned for daemon error responses
5. Timeout handling (5s dial, 10s query)

**New file:** `node/weightoracle/lru.go`
- Generic bounded LRU cache implementation

**New file:** `node/weightoracle/client_test.go`
- Unit tests using Go test TCP servers

### Phase 2: Python Mock Daemon

**New file:** `node/weightoracle/testdaemon/daemon.py`

A minimal TCP server that:
- Accepts JSON-over-TCP connections
- Returns configurable responses for weight, total_weight, ping, identity
- Supports concurrent connections
- Has `--port` and `--latency` flags
- Can be loaded with a weight table

### Phase 3: Startup Sequence

**File modified:** `node/node.go`

Insert weight oracle initialization in `MakeFull()` or early `Start()`:

1. **Port validation:**
   ```go
   if node.config.ExternalWeightOraclePort == 0 {
       return nil, fmt.Errorf("ExternalWeightOraclePort must be configured (required for weighted consensus)")
   }
   ```

2. **Create oracle client:**
   ```go
   oracle := weightoracle.NewClient(node.config.ExternalWeightOraclePort)
   ```

3. **Ping check:**
   ```go
   if err := oracle.Ping(); err != nil {
       return nil, fmt.Errorf("weight daemon not reachable: %w", err)
   }
   ```

4. **Identity validation:**
   ```go
   identity, err := oracle.Identity()
   if err != nil {
       return nil, fmt.Errorf("weight daemon identity query failed: %w", err)
   }
   if identity.GenesisHash != node.genesisHash {
       return nil, fmt.Errorf("weight daemon genesis hash mismatch: got %v, expected %v",
           identity.GenesisHash, node.genesisHash)
   }
   if identity.WeightAlgorithmVersion != ledgercore.ExpectedWeightAlgorithmVersion {
       return nil, fmt.Errorf("weight daemon algorithm version mismatch: got %s, expected %s",
           identity.WeightAlgorithmVersion, ledgercore.ExpectedWeightAlgorithmVersion)
   }
   if identity.WeightProtocolVersion != ledgercore.ExpectedWeightProtocolVersion {
       return nil, fmt.Errorf("weight daemon protocol version mismatch: got %s, expected %s",
           identity.WeightProtocolVersion, ledgercore.ExpectedWeightProtocolVersion)
   }
   ```

5. **Inject oracle into ledger:**
   ```go
   node.ledger.Ledger.SetWeightOracle(oracle)
   ```

6. **Validate participation key weights:**
   ```go
   if err := node.validateParticipationKeyWeights(oracle); err != nil {
       return nil, fmt.Errorf("participation key weight validation failed: %w", err)
   }
   ```

### Phase 4: validateParticipationKeyWeights()

**New function in:** `node/node.go`

```go
func (node *AlgorandFullNode) validateParticipationKeyWeights(oracle ledgercore.WeightOracle) error
```

**Implementation logic:**

1. Compute `voteRound = node.ledger.Latest() + 1`
2. Get consensus params: `cparams, _ := node.ledger.ConsensusParams(agreement.ParamsRound(voteRound))`
3. Compute `balanceRound = agreement.BalanceRound(voteRound, cparams)`
4. Iterate participation keys from `node.accountManager.Registry().GetAll()`:

   For each `record`:
   - Skip if `voteRound < record.FirstValid || voteRound > record.LastValid` (key not valid for this round)
   - Skip if `record.VRF == nil` (no VRF key)
   - Look up snapshot: `snapshotData, err := node.ledger.LookupAgreement(balanceRound, record.Account)`
   - Skip on error (account not online in snapshot)
   - Skip if `snapshotData.SelectionID != record.VRF.PK` (key mismatch)
   - Apply key-validity gating:
     ```go
     keyEligible := (voteRound >= snapshotData.VoteFirstValid) &&
                    (snapshotData.VoteLastValid == 0 || voteRound <= snapshotData.VoteLastValid)
     ```
   - Skip if `!keyEligible`
   - Query weight: `weight, err := oracle.Weight(balanceRound, record.Account, snapshotData.SelectionID)`
   - Fatal error if err or weight == 0

---

## Source Code Structure Changes

### New Files
| File | Description |
|------|-------------|
| `node/weightoracle/client.go` | WeightOracle client implementation |
| `node/weightoracle/lru.go` | Generic bounded LRU cache |
| `node/weightoracle/client_test.go` | Unit tests with Go test TCP servers |
| `node/weightoracle/testdaemon/daemon.py` | Python mock weight daemon |

### Modified Files
| File | Changes |
|------|---------|
| `node/node.go` | Add startup validation sequence and `validateParticipationKeyWeights()` |
| `ledger/ledger.go` | Change `ExternalWeight`/`TotalExternalWeight` from fallback-to-stake to panic when `weightOracle == nil` |

---

## Data Model / API / Interface Changes

### No interface changes required
- `ledgercore.WeightOracle` already defined in Task 1
- `ledgercore.ExternalWeighter` already defined in Task 1
- Ledger already implements `SetWeightOracle()`, `WeightOracle()`, `ExternalWeight()`, `TotalExternalWeight()`

### Wire protocol (daemon communication, per DD §1.3)

**Connection semantics:** Each request is a single JSON object sent over a new TCP connection
to `127.0.0.1:<port>`. The daemon replies with a single JSON object and the client closes the
connection. The daemon MUST support concurrent connections.

**Request formats:**
```
weight query:       {"type":"weight","address":"<base32-addr>","selection_id":"<hex-32-bytes>","balance_round":"<decimal>"}
total_weight query: {"type":"total_weight","balance_round":"<decimal>","vote_round":"<decimal>"}
ping query:         {"type":"ping"}
identity query:     {"type":"identity"}
```

**Success responses:**
```
weight:       {"weight":"<decimal>"}
total_weight: {"total_weight":"<decimal>"}
ping:         {"pong":true}
identity:     {"genesis_hash":"<base64>","protocol_version":"<str>","algorithm_version":"<str>"}
```

**Error response:**
```
{"error":"<human-readable-msg>","code":"<code>"}
```
Error codes: `"not_found"`, `"bad_request"`, `"internal"`, `"unsupported"`

**Mutual exclusivity:** Success fields and error fields are mutually exclusive.
On success, error fields MUST be omitted. On error, success fields MUST be omitted.

---

## Verification Approach

### Unit Tests (Go)
1. **Oracle client tests** (`node/weightoracle/client_test.go`):
   - Wire format encoding/decoding for all query types
   - Response parsing (success and error cases)
   - `*DaemonError` propagation with correct Code field
   - LRU cache behavior: hit, miss, eviction at capacity
   - Timeout handling
   - Concurrent query safety (run with `-race`)

2. **Ledger forwarding tests** (existing in `ledger/ledger.go`):
   - Verify `ExternalWeight`/`TotalExternalWeight` forward to mock oracle
   - Verify nil oracle panic behavior

3. **Startup validation tests** (new in `node/node_test.go`):
   - Port = 0 fails with clear error
   - Daemon unreachable fails
   - Genesis hash mismatch fails
   - Algorithm version mismatch fails
   - Protocol version mismatch fails
   - All checks pass, eligible key with weight succeeds
   - Eligible key with zero weight fails
   - Various skip conditions (key out of window, account not in snapshot, etc.)

### Integration Tests (Python daemon)
- Full chain: node startup -> oracle injection -> membership() -> credential verification
- Restart: verify re-queries daemon after cache clear

### Commands to Run
```bash
# Unit tests
go test -v -race ./node/weightoracle/...
go test -v -race ./ledger/...
go test -v -race ./node/...

# Build verification
make build

# Full test suite
make test
```

---

## Risk Analysis

### High-Risk Areas
1. **Startup order**: Oracle must be validated BEFORE consensus services start
2. **Round computation**: Must use same recipe as `agreement/selector.go::membership()`
3. **Key-eligibility gating**: Must match the gating in `membership()` exactly
4. **Error handling**: Daemon errors vs network errors have different semantics

### Mitigations
- Reuse `agreement.ParamsRound()` and `agreement.BalanceRound()` for round computation
- Follow DD §4.11 exactly for participation key validation
- Comprehensive test coverage for all error paths

---

## Estimated Scope

| Component | Est. Lines | Notes |
|-----------|------------|-------|
| `client.go` | ~200 | TCP/JSON client, response parsing |
| `lru.go` | ~50 | Generic bounded LRU |
| `client_test.go` | ~300 | Comprehensive unit tests |
| `daemon.py` | ~150 | Python mock server |
| `node.go` changes | ~100 | Startup sequence + validateParticipationKeyWeights |
| `node_test.go` additions | ~200 | Startup validation tests |

**Total: ~1000 lines** (including tests)
