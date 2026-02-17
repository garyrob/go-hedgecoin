# Task 6: Weight-Based Absenteeism - Technical Specification

## Overview

**Difficulty:** Medium

Task 6 modifies the absenteeism detection logic in `ledger/eval/` to use external weight values instead of stake values for determining expected proposal intervals. This affects both block generation (`generateKnockOfflineAccountsList`) and block validation (`validateAbsentOnlineAccounts`) paths.

## Technical Context

- **Language:** Go (1.21+)
- **Package:** `ledger/eval`
- **Dependencies:**
  - `ledger/ledgercore` (already implemented in Task 1)
    - `AbsenteeismMultiplier` constant (= 20)
    - `ExternalWeighter` interface
    - `DaemonError` type
  - `agreement` package for `BalanceRound`
  - `data/basics` for `Muldiv` function

## Prerequisite Verification

Task 1 has been completed and provides:
- `ledgercore.AbsenteeismMultiplier uint64 = 20` (in `weightoracle.go:39`)
- `ledgercore.ExternalWeighter` interface (in `externalweighter.go:29-37`)
  - `ExternalWeight(balanceRound basics.Round, addr basics.Address, selectionID crypto.VRFVerifier) (uint64, error)`
  - `TotalExternalWeight(balanceRound basics.Round, voteRound basics.Round) (uint64, error)`
- `ledgercore.DaemonError` type with `Code` field

## Implementation Approach

### Phase 1: Expose `balanceRound()` via `roundCowParent` Interface

**Files:** `ledger/eval/cow.go`, `ledger/eval/applications.go`

The existing `balanceRound()` method is defined on `roundCowBase` (in `eval.go:212`) but is NOT part of the `roundCowParent` interface. Since `roundCowState.lookupParent` is typed as `roundCowParent`, we cannot call `eval.state.balanceRound()` without adding it to the interface.

**Changes:**

1. Add `balanceRound() (basics.Round, error)` to the `roundCowParent` interface in `cow.go:42-73`
2. Add delegation method on `roundCowState` in `applications.go` (following the pattern of `onlineStake()`):
   ```go
   func (cs *roundCowState) balanceRound() (basics.Round, error) {
       return cs.lookupParent.balanceRound()
   }
   ```

### Phase 2: Create `isAbsentByWeight()` Function

**File:** `ledger/eval/eval.go`

Add a new function that uses weight values instead of stake:

```go
// isAbsentByWeight checks if an account should be considered absent using
// weight-based expected proposal intervals instead of stake-based intervals.
//
// Callers MUST enforce acctWeight > 0 before calling. The acctWeight == 0
// guard below is a defensive fallback matching the existing isAbsent behavior;
// it should never be reached in correct operation.
func isAbsentByWeight(totalWeight uint64, acctWeight uint64, lastSeen basics.Round, current basics.Round) bool {
    // Don't consider accounts that were online when payouts went into effect as
    // absent. They get noticed the next time they propose or keyreg.
    if lastSeen == 0 || acctWeight == 0 {
        return false
    }
    // See if the account has exceeded their expected observation interval.
    // allowableLag = AbsenteeismMultiplier * totalWeight / acctWeight
    allowableLag, o := basics.Muldiv(ledgercore.AbsenteeismMultiplier, totalWeight, acctWeight)
    // Return false for overflow or a huge allowableLag.
    if o || allowableLag > math.MaxUint32 {
        return false
    }

    return lastSeen+basics.Round(allowableLag) < current
}
```

**Constant consolidation:** The existing `const absentFactor = 20` will be kept but with a compile-time assertion added to ensure it equals `ledgercore.AbsenteeismMultiplier`:

```go
// Compile-time check that absentFactor matches ledgercore.AbsenteeismMultiplier
var _ = [1]int{}[absentFactor-ledgercore.AbsenteeismMultiplier]
```

### Phase 3: Modify `generateKnockOfflineAccountsList()`

**File:** `ledger/eval/eval.go:1637`

After getting `onlineStake`, add weight retrieval and use it for absence detection:

1. Type-assert `eval.l.(ledgercore.ExternalWeighter)` (panic if fails)
2. Compute `balanceRound` via `eval.state.balanceRound()`
3. Fetch `totalWeight` via `ew.TotalExternalWeight(balanceRound, current)`
4. Handle errors:
   - `DaemonError` with `Code != "internal"` → panic
   - `DaemonError` with `Code == "internal"` → log error and return (no knockoffs)
   - Other errors → log error and return
5. Cross-check: `!onlineStake.IsZero() && totalWeight == 0` → panic
6. In loop: fetch `accountWeight` via `ew.ExternalWeight(balanceRound, accountAddr, oad.SelectionID)`
7. Enforce `accountWeight > 0` (panic on violation)
8. Replace `isAbsent(...)` call with `isAbsentByWeight(totalWeight, accountWeight, lastSeen, current)`

### Phase 4: Modify `validateAbsentOnlineAccounts()`

**File:** `ledger/eval/eval.go:1832`

Identical weight-based logic as generation path, but with error returns instead of panics:

1. Type-assert `eval.l.(ledgercore.ExternalWeighter)` (return error if fails)
2. Compute `balanceRound` via `eval.state.balanceRound()`
3. Fetch `totalWeight` via `ew.TotalExternalWeight(balanceRound, eval.Round())`
4. Cross-check: `!totalOnlineStake.IsZero() && totalWeight == 0` → return error
5. Add `_ = totalOnlineStake` to prevent unused variable error if cross-check is removed
6. In loop: fetch `accountWeight` and enforce `accountWeight > 0` (return error on violation)
7. Replace `isAbsent(...)` call with `isAbsentByWeight(...)`

## Source Code Structure Changes

### Files Modified

| File | Changes |
|------|---------|
| `ledger/eval/cow.go` | Add `balanceRound() (basics.Round, error)` to `roundCowParent` interface |
| `ledger/eval/applications.go` | Add `balanceRound()` delegation method on `roundCowState` |
| `ledger/eval/eval.go` | Add `isAbsentByWeight()` function; modify `generateKnockOfflineAccountsList()` and `validateAbsentOnlineAccounts()` |
| `ledger/eval/eval_test.go` | Add tests for `isAbsentByWeight()` and weight-based absenteeism |

### New Files

None.

## Interface Changes

### `roundCowParent` Interface (cow.go)

Add method:
```go
balanceRound() (basics.Round, error)
```

## Verification Approach

### Unit Tests for `isAbsentByWeight()`

1. **Known intervals:** `totalWeight=1000, acctWeight=100` → `allowableLag = 200`, absent if `current - lastSeen > 200`
2. **Boundary:** Exact threshold round (test `<` vs `<=`)
3. **`lastSeen == 0`:** Should return false (not absent)
4. **`acctWeight == 0`:** Should return false (defensive)
5. **Overflow:** `totalWeight = math.MaxUint64, acctWeight = 1` → not absent (overflow)
6. **Large but non-overflowing values:** Correct threshold

### Generation/Validation Path Tests

1. **Mock `ExternalWeighter`:** Create mock that satisfies both `LedgerForEvaluator` and `ExternalWeighter`
2. **Generation and validation produce identical absent lists** for same inputs
3. **Cross-check fires:** `onlineStake > 0` but `totalWeight == 0` → panic (gen) / error (val)
4. **Zero-weight account in candidates:** → panic (gen) / error (val)
5. **Empty candidate list:** No panics, empty result
6. **`DaemonError{Code: "internal"}`:** Generation logs error and returns empty; validation returns error
7. **`DaemonError{Code: "not_found"}`:** → panic

### Existing Tests

Run existing `TestIsAbsent` and `TestAbsenteeChecks` to ensure they still pass (they test the original stake-based function).

## Build/Lint Commands

```bash
# Run unit tests for eval package
go test -v ./ledger/eval/...

# Run with race detection
go test -race ./ledger/eval/...

# Run specific tests
go test -v -run TestIsAbsentByWeight ./ledger/eval/...
go test -v -run TestAbsentee ./ledger/eval/...

# Lint
make lint

# Full sanity check
make sanity
```

## Risk Assessment

### Consensus Safety

This is a **consensus-critical change**. Both generation and validation paths must use identical weight-based absence criteria. Key safety measures:

1. Both paths use the same `isAbsentByWeight()` function
2. Both paths compute `balanceRound` using the same method (`eval.state.balanceRound()`)
3. Cross-checks catch daemon/ledger population mismatches
4. Overflow handling matches existing `isAbsent()` behavior

### Backward Compatibility

This change modifies consensus behavior. All nodes on the network must upgrade simultaneously. There is no backward compatibility requirement since this is a protocol upgrade.

## Estimated Code Size

- ~100 lines added/changed in Go (excluding tests)
- ~80-100 lines of test code
