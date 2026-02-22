# Implementation Task Breakdown for DD 4.4

## Overview

Seven tasks, ordered so each builds on prior work and each is independently testable. A shared Python mock weight daemon is built once in Task 2 and reused where helpful.

### Dependency graph

```
Task 1  (types & interfaces)
  ├── Task 2  (oracle client + Python mock daemon)
  ├── Task 3  (Membership struct + config)
  │     └── Task 4  (credential verification)
  │
  │   ┌─────────────────────────────────┐
  │   │ Tasks 5 and 6 are independent   │
  │   │ of each other; may run parallel │
  │   └─────────────────────────────────┘
  ├── Task 5  (membership() in agreement)  — needs Tasks 1, 3
  ├── Task 6  (absenteeism in eval)        — needs Task 1
  │
  └── Task 7  (Ledger wiring + startup)    — needs Tasks 1, 2, 3; integrates all
```

Tasks 5 and 6 test against Go interface mocks, not the real Ledger. Task 7 is the integration point that wires the real oracle client into the Ledger and validates the full chain end-to-end.

---

## Task 1 — Core Types and Interfaces

**Package:** `ledger/ledgercore/`

**New files:**
- `weightoracle.go` — `WeightOracle` interface, `DaemonIdentity` struct, `DaemonError` type, `IsDaemonError()` helper, version constants (`ExpectedWeightAlgorithmVersion`, `ExpectedWeightProtocolVersion`), `AbsenteeismMultiplier`
- `externalweighter.go` — `ExternalWeighter` interface

**What this produces:** The two foundational interfaces that every subsequent task depends on. No behavioral code — just type definitions and simple helpers.

**Unit tests** (`weightoracle_test.go`, `externalweighter_test.go`):
- `DaemonError.Error()` formatting
- `IsDaemonError()` matches correct code, rejects wrong code, returns false for non-`DaemonError` errors
- `errors.As` unwrapping through wrapped errors
- Compile-time interface satisfaction checks (e.g., `var _ WeightOracle = (*mockOracle)(nil)`)

**No daemon needed.** Pure Go.

---

## Task 2 — Oracle Client and Test Daemon

**Package:** `node/weightoracle/`

**New files:**
- `client.go` — `Client` struct implementing `WeightOracle` (TCP/JSON client, bounded LRU caches, timeouts)
- `lru.go` — Generic bounded LRU cache (or inlined in `client.go`)
- `client_test.go` — Unit tests using Go test TCP servers
- `testdaemon/daemon.py` — Shared Python mock weight daemon for integration testing

**Implementation notes:**
- The LRU `get()` calls `MoveToFront()`, so `sync.Mutex` is correct (not `RWMutex`)
- All numeric wire values are decimal strings (not JSON numbers) per §1.3
- `*ledgercore.DaemonError` must be returned for daemon error responses so callers can inspect `Code` via `errors.As`

**Unit tests** (Go test TCP servers in `client_test.go`):
- Wire format: verify JSON request encoding for all four query types (weight, total_weight, ping, identity)
- Response parsing: success responses, error responses with each code
- `*DaemonError` propagation: verify error type and `Code` field are preserved
- LRU behavior: cache hit, cache miss, eviction at capacity boundary
- Bounding: insert `maxWeightCacheSize + 1` entries, verify oldest is evicted
- Timeout: test server that sleeps past `queryTimeout` → client returns error
- Concurrent queries: parallel goroutines, no races (run with `-race`)
- Identity: base64 genesis hash decoding, length validation

Go test TCP servers are more practical here than the Python daemon because they're in-process, fast, and run under `go test -race`. The Python daemon is built as a reusable fixture for later integration work.

**Python mock daemon** (`testdaemon/daemon.py`):

A minimal TCP server that:
- Accepts JSON-over-TCP connections (one request per connection)
- Returns configurable responses for each query type
- Supports concurrent connections (threaded or async)
- Has a `--port` flag and optional `--latency` flag for timeout testing
- Can be loaded with a weight table (JSON file or in-memory dictionary)
- Validates the protocol handshake (identity query with configurable genesis hash, versions)

This daemon is reused in Tasks 5, 6, and 7 for integration tests.

---

## Task 3 — Extend Membership Struct and Config

**Files modified:**
- `data/committee/committee.go` — add `ExternalWeight uint64` and `TotalExternalWeight uint64` to `Membership`
- `config/localTemplate.go` — add `ExternalWeightOraclePort uint16` with version tag **39**
- `config/local_defaults.go` — update `Version: 39`

**Critical prerequisite — Membership construction audit:**

Before this task can be considered complete, search the **entire codebase** for every site that constructs a `Membership` literal:

```bash
# Must search for BOTH forms (package-qualified and unqualified in tests):
grep -rn 'committee\.Membership{' .
grep -rn 'Membership{' data/committee/ agreement/
```

Every construction site must be updated to include the new fields (typically zero-valued if the site is in test code that doesn't exercise weight paths). Missed sites will silently produce zero-valued fields, which triggers the zero-weight panic in `Verify()` at runtime — a subtle, hard-to-diagnose failure.

**Post-modification steps:**
- Run `make generate` (updates msgp, config defaults, etc.)
- Update any test config fixtures that the config package documentation requires

**Tests:**
- Compilation test: the project builds cleanly (catches missed construction sites)
- Config round-trip: serialize/deserialize `Local` struct with new field
- Verify default `ExternalWeightOraclePort` is 0 (which correctly triggers startup failure)

**No daemon needed.** Pure Go structural changes.

---

## Task 4 — Modify Credential Verification

**File modified:** `data/committee/credential.go`

**Changes to `UnauthenticatedCredential.Verify()`:**
- Remove the `!userMoney.IsZero()` stake gate
- Remove the `m.TotalMoney`-based invariant checks
- Add `_ = userMoney` to suppress the unused-variable error (minimal diff approach)
- Gate on `m.ExternalWeight > 0`
- Validate: `TotalExternalWeight >= ExternalWeight` (panic)
- Validate: `TotalExternalWeight > 0`, `expectedSelection > 0`, `expectedSelection <= float64(TotalExternalWeight)` (panic)
- Call `sortition.Select(m.ExternalWeight, m.TotalExternalWeight, expectedSelection, sortition.Digest(h))`

**Key verification:** `expectedSelection` is still computed from `m.Selector.CommitteeSize(proto)`, which is independent of weight/stake. This does not change.

**Unit tests** (construct `Membership` values directly — no daemon):
- Non-zero weight → sortition runs, credential returned
- Zero weight → `weight == 0`, error returned ("credential has weight 0")
- `TotalExternalWeight < ExternalWeight` → panic
- `TotalExternalWeight == 0` with `ExternalWeight > 0` → panic
- `expectedSelection > float64(TotalExternalWeight)` → panic
- Stake value is irrelevant: identical outcomes whether `VotingStake()` returns 0 or 1,000,000
- Statistical validation: run 10,000+ trials with known weight ratios, verify selection frequencies converge to expected proportions

**No daemon needed.** The `Membership` struct is populated by hand in tests.

---

## Task 5 — Modify `membership()` in Agreement

**File modified:** `agreement/selector.go`

**Changes:**
- Add imports: `errors`, `ledgercore`, `logging`
- After existing `LookupAgreement` / `Circulation` / `Seed` calls, add key-eligibility gating:
  ```go
  keyEligible := (r >= record.VoteFirstValid) &&
                 (record.VoteLastValid == 0 || r <= record.VoteLastValid)
  ```
- If `!keyEligible`: return `m` with zero-valued weight fields (vote.verify rejects downstream)
- If `keyEligible`: type-assert `l.(ledgercore.ExternalWeighter)`, fetch `ExternalWeight` and `TotalExternalWeight`, validate non-zero and population alignment

**Error classification** (per §3.2 / §4.7):
- `DaemonError` with `Code != "internal"` → panic (invariant violation: we only query for circulation-population participants)
- `DaemonError` with `Code == "internal"` → return error (operational)
- Network/timeout errors → return error (operational)

**Design note:** The `LedgerReader` interface is NOT modified. `ExternalWeighter` is accessed via type assertion. This preserves the existing interface contract and minimizes changes to code that depends on `LedgerReader`.

**Unit tests** (Go mock that satisfies both `LedgerReader` and `ExternalWeighter`):
- Eligible account: weight fields populated correctly
- Ineligible account (expired keys, `r > VoteLastValid`): weight fields left at zero, **no daemon query made** (verify mock was not called)
- Ineligible account (`r < VoteFirstValid`): same behavior
- Perpetual keys (`VoteLastValid == 0`): always eligible
- `ExternalWeighter` assertion failure → panic
- Daemon returns zero weight → panic
- `TotalExternalWeight < ExternalWeight` → panic
- `DaemonError{Code: "not_found"}` → panic
- `DaemonError{Code: "internal"}` → error returned (not panic)
- Network timeout → error returned (not panic)

**Optional integration test** with Python mock daemon: validates the full `membership()` → Ledger → oracle client → daemon chain.

---

## Task 6 — Weight-Based Absenteeism

**Files modified:**
- `ledger/eval/eval.go` — new `isAbsentByWeight()`, modify `generateKnockOfflineAccountsList()` and `validateAbsentOnlineAccounts()`
- `ledger/eval/cow.go` — expose `balanceRound()` through `roundCowParent` interface

### DD correction: `eval.state.balanceRound()` does not compile

The DD (§4.9.4, §4.9.5) calls `eval.state.balanceRound()`. However, `eval.state` is `*roundCowState`, and `balanceRound()` is defined only on `*roundCowBase`. The `roundCowParent` interface (in `cow.go`) includes `lookupAgreement()` and `onlineStake()` but **not** `balanceRound()`. Since `roundCowState.lookupParent` is typed as `roundCowParent` (an interface field, not an embedded struct), `roundCowBase` methods are not promoted.

**Fix:** Add `balanceRound()` to the `roundCowParent` interface and add a delegation method on `roundCowState`:

```go
// cow.go — add to roundCowParent interface:
balanceRound() (basics.Round, error)

// cow.go — add delegation method:
func (cb *roundCowState) balanceRound() (basics.Round, error) {
    return cb.lookupParent.balanceRound()
}
```

This is a 4-line change. The existing `roundCowBase.balanceRound()` implementation (which looks up the protocol at `ParamsRound(current)` to handle upgrade boundaries) is already correct and is reused through delegation.

### Constant consolidation

The existing `const absentFactor = 20` in `eval.go` must either be replaced with an import of `ledgercore.AbsenteeismMultiplier` or have a compile-time assertion added to prevent drift.

### `isAbsentByWeight()` function

Signature: `func isAbsentByWeight(totalWeight uint64, acctWeight uint64, lastSeen basics.Round, current basics.Round) bool`

Logic:
- `lastSeen == 0 || acctWeight == 0` → return false (defensive; callers enforce `acctWeight > 0`)
- `allowableLag, overflow := basics.Muldiv(AbsenteeismMultiplier, totalWeight, acctWeight)`
- overflow or `allowableLag > math.MaxUint32` → return false
- return `lastSeen + Round(allowableLag) < current`

### Generation path (`generateKnockOfflineAccountsList`)

1. After `onlineStake`, type-assert `eval.l.(ExternalWeighter)` (panic if fails)
2. Compute `balanceRound` via `eval.state.balanceRound()` (now works after cow.go fix)
3. Fetch `totalWeight` via `ew.TotalExternalWeight(balanceRound, current)`
4. Cross-check: `!onlineStake.IsZero() && totalWeight == 0` → panic
5. In loop: fetch `accountWeight` via `ew.ExternalWeight(balanceRound, accountAddr, oad.SelectionID)`
6. Enforce `accountWeight > 0` (panic on violation)
7. Replace `isAbsent(...)` with `isAbsentByWeight(totalWeight, accountWeight, ...)`

### Validation path (`validateAbsentOnlineAccounts`)

Identical weight-based logic. **Both paths must produce identical results for the same inputs** — this is consensus-critical. The validation path returns errors (not panics) for the cross-check and zero-weight conditions, since validation errors propagate differently than generation failures.

### Note on `onlineStake` / `totalOnlineStake` retention

After switching to `isAbsentByWeight()`, the stake variables are no longer passed to the absence function. They are retained for the cross-check against `totalWeight` and to avoid Go "declared and not used" compilation errors. The cross-check catches daemon/ledger population mismatches early.

**Unit tests:**

`isAbsentByWeight()` pure function:
- Known intervals: `totalWeight=1000, acctWeight=100` → `allowableLag = 200`, absent if `current - lastSeen > 200`
- Boundary: exact threshold round (test `<` vs `<=`)
- `lastSeen == 0` → not absent
- `acctWeight == 0` → not absent (defensive)
- Overflow: `totalWeight = math.MaxUint64, acctWeight = 1` → not absent (overflow)
- Large but non-overflowing values → correct threshold

Generation/validation path tests (mock `ExternalWeighter`):
- Generation and validation produce identical absent lists for same inputs
- Cross-check fires: `onlineStake > 0` but `totalWeight == 0` → panic (gen) / error (val)
- Zero-weight account in candidates → panic (gen) / error (val)
- Empty candidate list → no panics, empty result
- `DaemonError{Code: "internal"}` for totalWeight → generation logs error and returns empty (no knockoffs); validation returns error
- `DaemonError{Code: "not_found"}` for totalWeight → panic

---

## Task 7 — Ledger Wiring, Startup Validation, and Integration

**Files modified:**
- `ledger/ledger.go` — `weightOracle` field, `SetWeightOracle()`, `WeightOracle()`, `ExternalWeight()`, `TotalExternalWeight()`
- `node/node.go` — oracle creation, ping, identity handshake, `validateParticipationKeyWeights()`, injection into ledger

### Ledger ExternalWeighter implementation

```go
func (l *Ledger) ExternalWeight(balanceRound basics.Round, addr basics.Address, selID crypto.VRFVerifier) (uint64, error) {
    if l.weightOracle == nil {
        logging.Base().Panicf("ExternalWeight called but no oracle configured")
    }
    return l.weightOracle.Weight(balanceRound, addr, selID)
}

func (l *Ledger) TotalExternalWeight(balanceRound basics.Round, voteRound basics.Round) (uint64, error) {
    if l.weightOracle == nil {
        logging.Base().Panicf("TotalExternalWeight called but no oracle configured")
    }
    return l.weightOracle.TotalWeight(balanceRound, voteRound)
}
```

Add a compile-time interface check: `var _ ledgercore.ExternalWeighter = (*Ledger)(nil)`

### Startup sequence (in `node.Start()`)

1. Check `ExternalWeightOraclePort > 0` (fatal if not)
2. Create `weightoracle.NewClient(port)`
3. `oracle.Ping()` (fatal if fails)
4. `oracle.Identity()` → validate genesis hash, algorithm version, protocol version (fatal on mismatch)
5. `node.ledger.SetWeightOracle(oracle)`
6. `node.validateParticipationKeyWeights(oracle)` (fatal if any eligible key has zero weight)

### `validateParticipationKeyWeights` details

This function uses the same round computation recipe as `agreement/selector.go::membership()`:
- `voteRound = node.ledger.Latest() + 1`
- `cparams = ConsensusParams(ParamsRound(voteRound))`
- `balanceRound = BalanceRound(voteRound, cparams)`

For each participation key from `ListParticipationKeys()`:
- Skip if `voteRound` outside `[record.FirstValid, record.LastValid]`
- Skip if `record.VRF == nil`
- Look up snapshot data via `LookupAgreement(balanceRound, record.Account)` — skip on error (account not online in snapshot)
- Skip if `snapshotData.SelectionID != record.VRF.PK` (key mismatch)
- Apply key-validity gating: skip if not `(voteRound >= snapshotData.VoteFirstValid) && (snapshotData.VoteLastValid == 0 || voteRound <= snapshotData.VoteLastValid)`
- Query `oracle.Weight(balanceRound, record.Account, snapshotData.SelectionID)` — fatal on error or zero weight

**API verification note** (from DD §4.11): Before implementing, verify the following field names/types against the actual codebase:
- `ParticipationRecord.Account` (vs `Parent` on the older `Participation` struct — the registry uses `Account`)
- `record.VRF` is `*crypto.VRFSecrets` (pointer — must nil-check)
- `record.VRF.PK` is `crypto.VrfPubkey` (which is `VRFVerifier`)
- `record.FirstValid` / `record.LastValid` are `basics.Round`

### Tests

**Unit tests** (Go mock oracle):
- Ledger: `ExternalWeight` / `TotalExternalWeight` forward correctly to mock oracle
- Ledger: nil oracle → panic
- Compile-time: `*Ledger` satisfies `ExternalWeighter`

**Startup tests** (Python mock daemon — this is where it shines):
- Port = 0 → startup fails with clear error
- Daemon unreachable → startup fails
- Ping succeeds but identity genesis hash wrong → startup fails
- Identity algorithm version mismatch → startup fails
- Identity protocol version mismatch → startup fails
- All checks pass, eligible key has weight → startup succeeds
- All checks pass, eligible key has zero weight → startup fails
- Participation key outside vote window → skipped (no query)
- Participation key present but account not in snapshot → skipped
- Participation key SelectionID doesn't match snapshot → skipped
- Snapshot key-validity window doesn't include voteRound → skipped

**Integration tests** (Python mock daemon):
- Full chain: node startup → oracle injection → `membership()` → credential verification with weight-based sortition
- Restart: clear client cache, verify re-queries daemon
- Verify cache bounds hold over many rounds of simulated queries

---

## Summary Table

| Task | Files | New Code (est.) | Key Test Approach |
|------|-------|-----------------|-------------------|
| 1 — Types & interfaces | `ledgercore/weightoracle.go`, `externalweighter.go` | ~100 lines | Pure Go unit tests |
| 2 — Oracle client + daemon | `node/weightoracle/client.go`, `testdaemon/daemon.py` | ~350 lines Go + ~150 lines Python | Go test TCP servers; Python daemon as reusable fixture |
| 3 — Membership + config | `committee/committee.go`, `config/localTemplate.go` | ~15 lines + codebase audit | Compilation; config round-trip |
| 4 — Credential verification | `committee/credential.go` | ~30 lines changed | Constructed Membership values; statistical sortition validation |
| 5 — membership() in agreement | `agreement/selector.go` | ~50 lines added | Go mock LedgerReader+ExternalWeighter |
| 6 — Absenteeism | `eval/eval.go`, `eval/cow.go` | ~100 lines added/changed | Pure function tests + mock ExternalWeighter |
| 7 — Ledger + startup + integration | `ledger/ledger.go`, `node/node.go` | ~120 lines | Go mocks for unit; Python daemon for integration |

**Total estimated new/changed code:** ~750 lines Go + ~150 lines Python (excluding tests).
