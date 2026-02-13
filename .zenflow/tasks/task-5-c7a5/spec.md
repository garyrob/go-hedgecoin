# Technical Specification: Task 5 — Modify `membership()` in Agreement

## Difficulty Assessment: Medium

This task involves:
- Modifying a single function in the agreement package
- Adding type assertions to access an existing interface
- Implementing error handling logic with panic/error distinction
- Adding imports and key-eligibility gating logic
- Writing unit tests with mock implementations

Edge cases and caveats require careful handling (error classification, key validity gating), but the scope is well-defined with clear guidance from the design document.

---

## Technical Context

**Language:** Go
**Target File:** `agreement/selector.go`
**Dependencies:**
- `ledger/ledgercore` - Contains `ExternalWeighter` interface, `DaemonError` type
- `data/committee` - Contains `Membership` struct (already updated with weight fields)
- `logging` - For panic handling

**Prior Tasks Completed:**
- Task 1: Core types and interfaces in `ledgercore/weightoracle.go` and `ledgercore/externalweighter.go`
- Task 3: `Membership` struct updated with `ExternalWeight` and `TotalExternalWeight` fields

---

## Implementation Approach

### Overview

Modify the `membership()` function in `agreement/selector.go` to:
1. Gate weight queries on vote-key validity (prevents network-triggerable panics)
2. Type-assert `LedgerReader` to `ExternalWeighter` interface
3. Fetch and validate external weights for eligible accounts
4. Implement proper error classification (panic vs. return error)

### Key Design Decisions (from DD §3.2 and §4.7)

1. **Key-Eligibility Gating**: The `membership()` function is called BEFORE vote-key validity checks in `vote.go`. Without gating, queries for accounts with expired/invalid keys could cause panics on valid daemon responses.

2. **Interface Access via Type Assertion**: The `LedgerReader` interface is NOT modified. `ExternalWeighter` is accessed via type assertion. Failure indicates a startup configuration error (fatal).

3. **Error Classification**:
   - `DaemonError` with `Code != "internal"` → panic (invariant violation)
   - `DaemonError` with `Code == "internal"` → return error (operational)
   - Network/timeout errors → return error (operational)

---

## Source Code Changes

### File: `agreement/selector.go`

**Changes Required:**

1. **Add imports to existing import block** (only these 3 are new):
   ```go
   "errors"

   "github.com/algorand/go-algorand/ledger/ledgercore"
   "github.com/algorand/go-algorand/logging"
   ```
   Note: `fmt`, `config`, `basics`, `committee`, and `protocol` are already imported.

2. **Modify `membership()` function** (lines ~68-98):
   - After existing `LookupAgreement` / `Circulation` / `Seed` calls
   - Add key-eligibility gating
   - Add `ExternalWeighter` type assertion
   - Fetch and validate weight fields
   - Implement error classification

### Error Message Prefix Change (Intentional)

The DD reference implementation uses `"membership (r=%d): ..."` error message prefixes, which differs from the existing code's `"Service.initializeVote (r=%d): ..."`. This change is **intentional** per the DD and provides clearer context that the error originates from the `membership()` function.

### Detailed Implementation

Replace the `membership()` function (starting at line 68) with:

```go
// membership obtains membership verification parameters for the given address and round.
func membership(l LedgerReader, addr basics.Address, r basics.Round, p period, s step) (m committee.Membership, err error) {
    cparams, err := l.ConsensusParams(ParamsRound(r))
    if err != nil {
        return
    }
    balanceRound := BalanceRound(r, cparams)
    seedRound := seedRound(r, cparams)

    record, err := l.LookupAgreement(balanceRound, addr)
    if err != nil {
        err = fmt.Errorf("membership (r=%d): Failed to obtain balance record for address %v in round %d: %w", r, addr, balanceRound, err)
        return
    }

    total, err := l.Circulation(balanceRound, r)
    if err != nil {
        err = fmt.Errorf("membership (r=%d): Failed to obtain total circulation in round %d: %v", r, balanceRound, err)
        return
    }

    seed, err := l.Seed(seedRound)
    if err != nil {
        err = fmt.Errorf("membership (r=%d): Failed to obtain seed in round %d: %v", r, seedRound, err)
        return
    }

    m.Record = committee.BalanceRecord{OnlineAccountData: record, Addr: addr}
    m.Selector = selector{Seed: seed, Round: r, Period: p, Step: s}
    m.TotalMoney = total

    // CRITICAL: Gate weight queries on vote-key validity (see DD §3.2).
    // membership() is called BEFORE vote-key validity checks in vote.go,
    // so we may receive messages from accounts with expired/invalid keys.
    // Without this check, we would panic on valid daemon responses for ineligible accounts.
    keyEligible := (r >= record.VoteFirstValid) && (record.VoteLastValid == 0 || r <= record.VoteLastValid)

    if !keyEligible {
        // Leave ExternalWeight and TotalExternalWeight as zero.
        // vote.verify will reject this message immediately afterward
        // based on the same key validity check.
        return m, nil
    }

    // Fetch external weights - REQUIRED for this weighted-selection network.
    // Only reached for accounts with valid vote keys at round r.
    ew, ok := l.(ledgercore.ExternalWeighter)
    if !ok {
        // This is a local invariant violation: startup should have validated oracle configuration.
        logging.Base().Panicf("membership (r=%d): weighted network requires ExternalWeighter support", r)
    }

    m.ExternalWeight, err = ew.ExternalWeight(balanceRound, addr, record.SelectionID)
    if err != nil {
        // Check error type: not_found/bad_request/unsupported are invariant violations
        // (we only query for key-eligible participants per §3.2), internal is operational
        var de *ledgercore.DaemonError
        if errors.As(err, &de) && de.Code != "internal" {
            // not_found, bad_request, unsupported → invariant violation
            logging.Base().Panicf("membership (r=%d): daemon invariant violation for addr %v: %v", r, addr, err)
        }
        // internal or network error → return error for operational handling
        err = fmt.Errorf("membership (r=%d): Failed to obtain external weight for address %v: %w", r, addr, err)
        return
    }

    m.TotalExternalWeight, err = ew.TotalExternalWeight(balanceRound, r)
    if err != nil {
        var de *ledgercore.DaemonError
        if errors.As(err, &de) && de.Code != "internal" {
            logging.Base().Panicf("membership (r=%d): daemon invariant violation for total weight: %v", r, err)
        }
        err = fmt.Errorf("membership (r=%d): Failed to obtain total external weight: %w", r, err)
        return
    }

    // Validate non-zero weight requirements per protocol spec.
    if m.ExternalWeight == 0 {
        logging.Base().Panicf("membership (r=%d): eligible participant %v has zero weight (invalid daemon state)", r, addr)
    }
    if m.TotalExternalWeight == 0 {
        logging.Base().Panicf("membership (r=%d): total weight is zero (invalid daemon state)", r)
    }

    // Validate population alignment: total must include this account's weight
    if m.TotalExternalWeight < m.ExternalWeight {
        logging.Base().Panicf("membership (r=%d): TotalExternalWeight %d < ExternalWeight %d (population alignment violated)",
            r, m.TotalExternalWeight, m.ExternalWeight)
    }

    return m, nil
}
```

---

## Test Strategy

### New Test File: `agreement/selector_test.go`

There is no existing `selector_test.go` file, and no existing tests for `membership()`. The existing `testLedger` mock in `common_test.go` implements `LedgerReader` but NOT `ExternalWeighter`, so we'll create a lightweight mock specifically for these tests.

Create comprehensive unit tests using a mock that satisfies both `LedgerReader` and `ExternalWeighter`:

#### Test Cases (per DD Task 5 requirements):

1. **Eligible account**: Weight fields populated correctly
2. **Ineligible account (expired keys, `r > VoteLastValid`)**: Weight fields left at zero, **no daemon query made** (verify mock was not called)
3. **Ineligible account (`r < VoteFirstValid`)**: Same behavior as #2
4. **Perpetual keys (`VoteLastValid == 0`)**: Always eligible
5. **`ExternalWeighter` assertion failure**: → panic
6. **Daemon returns zero weight**: → panic
7. **`TotalExternalWeight < ExternalWeight`**: → panic
8. **`DaemonError{Code: "not_found"}`**: → panic
9. **`DaemonError{Code: "internal"}`**: → error returned (not panic)
10. **Network timeout**: → error returned (not panic)

### Mock Implementation Structure

```go
type mockLedgerReaderWithWeights struct {
    // LedgerReader methods
    lookupAgreementFn   func(basics.Round, basics.Address) (basics.OnlineAccountData, error)
    circulationFn       func(basics.Round, basics.Round) (basics.MicroAlgos, error)
    seedFn              func(basics.Round) (committee.Seed, error)
    consensusParamsFn   func(basics.Round) (config.ConsensusParams, error)

    // ExternalWeighter methods (nil if not supported)
    externalWeightFn      func(basics.Round, basics.Address, crypto.VRFVerifier) (uint64, error)
    totalExternalWeightFn func(basics.Round, basics.Round) (uint64, error)

    // Tracking
    externalWeightCalled      bool
    totalExternalWeightCalled bool
}
```

---

## Verification Approach

1. **Compile check**: `go build ./agreement/...`
2. **Unit tests**: `go test -v ./agreement/... -run TestMembership`
3. **Race detection**: `go test -race ./agreement/...`
4. **Linting**: `make lint`
5. **Full test suite**: `make test`

---

## Risk Assessment

| Risk | Mitigation |
|------|------------|
| Key-eligibility gating logic incorrect | Mirror exact logic from DD §3.2 and `vote.go` vote-key validation |
| Panic handling style differs from codebase | Uses `logging.Base().Panicf()` per DD; this is consistent with existing agreement package patterns |
| Error classification logic wrong | Test all DaemonError codes explicitly |
| Type assertion panic leaks to production | Test panic paths are only hit for invariant violations |
| Import cycles | `ledgercore` chosen specifically to avoid cycles |

---

## Summary

**Files Modified:**
- `agreement/selector.go` - ~50 lines added/changed

**New Test File:**
- `agreement/selector_test.go` - Unit tests for membership() with mock LedgerReader+ExternalWeighter

**No daemon needed**: Tests use Go mocks that satisfy both interfaces.
