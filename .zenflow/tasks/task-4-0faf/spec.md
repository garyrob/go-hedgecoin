# Technical Specification: Task 4 - Modify Credential Verification

## Overview

**Task**: Modify `UnauthenticatedCredential.Verify()` in `data/committee/credential.go` to use external weight instead of stake for committee sortition.

**Difficulty**: Easy-Medium
- The core change is straightforward (replace stake-based sortition inputs with weight-based inputs)
- Key considerations: maintaining panic semantics for invariant violations, statistical validation for tests

## Technical Context

### Language & Dependencies
- **Language**: Go 1.21+
- **Key Dependencies**:
  - `github.com/algorand/sortition` - External sortition package (unchanged)
  - `github.com/algorand/go-algorand/logging` - For panic logging
  - Standard library only otherwise

### Prerequisites (Completed in Earlier Tasks)
- **Task 1**: `ledger/ledgercore/weightoracle.go` - Defines `WeightOracle` interface, `DaemonError`, constants
- **Task 1**: `ledger/ledgercore/externalweighter.go` - Defines `ExternalWeighter` interface
- **Task 3**: `data/committee/committee.go` - Added `ExternalWeight` and `TotalExternalWeight` fields to `Membership` struct

## Implementation Approach

### Core Change

Replace the current stake-based sortition call in `credential.go`:

**Current code (lines 102-108)**:
```go
if m.TotalMoney.Raw < userMoney.Raw {
    logging.Base().Panicf("UnauthenticatedCredential.Verify: total money = %v, but user money = %v", m.TotalMoney, userMoney)
} else if m.TotalMoney.IsZero() || expectedSelection == 0 || expectedSelection > float64(m.TotalMoney.Raw) {
    logging.Base().Panicf("UnauthenticatedCredential.Verify: m.TotalMoney %v, expectedSelection %v", m.TotalMoney.Raw, expectedSelection)
} else if !userMoney.IsZero() {
    weight = sortition.Select(userMoney.Raw, m.TotalMoney.Raw, expectedSelection, sortition.Digest(h))
}
```

**New code**:
```go
// Stake is no longer used for gating or selection; suppress unused-variable error
_ = userMoney

// Weight determines both eligibility and selection probability.
// ExternalWeight == 0 means either:
//   (a) The account had invalid vote keys (membership() left weights at zero), or
//   (b) An invariant violation (should have been caught in membership()).
// In case (a), vote.verify rejects the message immediately afterward.
if m.ExternalWeight > 0 {
    // Population alignment check: TotalExternalWeight must be >= ExternalWeight
    // Note: This also catches TotalExternalWeight == 0 when ExternalWeight > 0
    if m.TotalExternalWeight < m.ExternalWeight {
        logging.Base().Panicf("UnauthenticatedCredential.Verify: TotalExternalWeight %d < ExternalWeight %d (population alignment violated)",
            m.TotalExternalWeight, m.ExternalWeight)
    }

    // Validate sortition parameters (expectedSelection bounds)
    if expectedSelection == 0 || expectedSelection > float64(m.TotalExternalWeight) {
        logging.Base().Panicf("UnauthenticatedCredential.Verify: TotalExternalWeight %d, expectedSelection %v",
            m.TotalExternalWeight, expectedSelection)
    }

    // Weight passed directly to sortition.Select
    weight = sortition.Select(m.ExternalWeight, m.TotalExternalWeight, expectedSelection, sortition.Digest(h))
}
```

**Note on panic logic simplification**: The check `m.TotalExternalWeight == 0` is redundant when inside the `m.ExternalWeight > 0` block because the preceding `m.TotalExternalWeight < m.ExternalWeight` check would already panic if `TotalExternalWeight` is 0 and `ExternalWeight` is positive. The code above reflects this simplification.

### Key Behavioral Changes

1. **Eligibility gate**: `!userMoney.IsZero()` (stake-based) → `m.ExternalWeight > 0` (weight-based)
2. **Invariant checks**: `TotalMoney`-based checks → `TotalExternalWeight`-based checks
3. **Sortition inputs**: `(userMoney.Raw, m.TotalMoney.Raw)` → `(m.ExternalWeight, m.TotalExternalWeight)`
4. **Stake**: `userMoney` value is assigned but not used; marked with `_ = userMoney` to suppress compiler error

### Panic Semantics Preserved

The new code maintains the same panic behavior for invariant violations:
- `TotalExternalWeight < ExternalWeight` → panic (population alignment; also catches `TotalExternalWeight == 0` when `ExternalWeight > 0`)
- `expectedSelection > float64(TotalExternalWeight)` → panic
- `expectedSelection == 0` → panic

## Source Code Structure Changes

### Files Modified

| File | Change Description |
|------|-------------------|
| `data/committee/credential.go` | Replace stake-based sortition with weight-based sortition (~30 lines changed) |
| `data/committee/credential_test.go` | Add new weight-based tests, update existing tests including `BenchmarkSortition` (~150 lines added/modified) |

### No New Files Created

This task modifies existing files only.

## Data Model / API / Interface Changes

### No Interface Changes

The `UnauthenticatedCredential.Verify()` method signature remains unchanged:
```go
func (cred UnauthenticatedCredential) Verify(proto config.ConsensusParams, m Membership) (res Credential, err error)
```

### Behavioral Contract Change

| Condition | Before (Stake-based) | After (Weight-based) |
|-----------|---------------------|---------------------|
| `userMoney.IsZero()` | Returns weight=0, error | N/A (ignored) |
| `m.ExternalWeight == 0` | N/A | Returns weight=0, error |
| Selection probability | Proportional to stake | Proportional to weight |

## Verification Approach

### Unit Tests to Add

1. **Non-zero weight → sortition runs**: Construct `Membership` with `ExternalWeight > 0`, verify credential returned with weight > 0

2. **Zero weight → error returned**: Construct `Membership` with `ExternalWeight == 0`, verify error "credential has weight 0"

3. **`TotalExternalWeight < ExternalWeight` → panic**: Use `require.Panics` to verify population alignment check

4. **`TotalExternalWeight == 0` with `ExternalWeight > 0` → panic**: Verify sortition parameter check

5. **`expectedSelection > float64(TotalExternalWeight)` → panic**: Verify sortition parameter check

6. **Stake value is irrelevant**: Run sortition twice with identical weight values but different stake values (e.g., stake=0 vs stake=1000000), verify identical credential outcomes. This confirms stake is no longer a factor.

7. **Statistical validation**: Run 10,000+ trials with known weight ratios (e.g., 50% weight ratio), verify selection frequencies converge to expected proportions within ±20% tolerance (matching existing test patterns like `committee < uint64(0.8*float64(step.CommitteeSize(proto)))`)

### Existing Tests to Update

The existing tests in `credential_test.go` construct `Membership` structs. Since Task 3 added zero-valued `ExternalWeight` and `TotalExternalWeight` fields, these tests will fail with the new weight-based code (weight=0 returns error).

**Strategy**: Update existing tests to set `ExternalWeight` and `TotalExternalWeight` fields appropriately. The existing tests validate stake-based sortition; we can either:
- (A) Keep them passing with weight values matching the stake values they test, OR
- (B) Rewrite them to be weight-focused

**Recommended**: Option (A) - set `ExternalWeight = userMoney.Raw` and `TotalExternalWeight = TotalMoney.Raw` in existing tests. This preserves test coverage for sortition behavior while making them weight-based.

**Important**: By using Option (A), the existing **deterministic pinned outputs** (e.g., `assert.EqualValues(t, 17, leaders)` in `TestAccountSelected`) will remain unchanged because the same numeric values flow to `sortition.Select()`. This preserves test stability and verifies the migration didn't alter sortition behavior.

### Commands to Run

```bash
# Run unit tests for the committee package
go test -v ./data/committee/...

# Run with race detection (REQUIRED)
go test -race -v ./data/committee/...

# Run specific credential tests
go test -v -run TestCredential ./data/committee/

# Run benchmark to verify it still works
go test -bench=BenchmarkSortition ./data/committee/

# Run full test suite to catch any regressions
make shorttest
```

### Test Update Locations

The following test functions construct `Membership` structs and need weight fields added:

1. `TestAccountSelected` - lines 53-57, 77-82
2. `TestRichAccountSelected` - lines 117-121, 137-141
3. `TestPoorAccountSelectedLeaders` - lines 178-182
4. `TestPoorAccountSelectedCommittee` - lines 228-232
5. `TestNoMoneyAccountNotSelected` - lines 269-273
6. `TestLeadersSelected` - lines 303-307
7. `TestCommitteeSelected` - lines 335-339
8. `TestAccountNotSelected` - lines 363-367
9. `BenchmarkSortition` - lines 406-410

## Risk Assessment

### Low Risk
- The sortition algorithm is unchanged (same `sortition.Select` function)
- Weight values go through the same mathematical path as stake values did
- Existing panic semantics preserved

### Considerations
- Existing tests must be updated to provide weight values
- Statistical properties of selection should remain unchanged (just different input source)

## Implementation Plan

Since this is a focused, single-file change with clear specification, a single implementation step is appropriate:

### Implementation Step
1. Modify `credential.go` to use weight-based sortition (per specification above)
2. Update existing tests in `credential_test.go` to provide weight values (see "Test Update Locations" above)
3. Update `BenchmarkSortition` to provide weight values
4. Add new test cases for weight-specific scenarios (zero weight, panic conditions)
5. Add statistical validation test with ±20% tolerance
6. Add test to verify stake value is irrelevant
7. Run tests with race detection: `go test -race -v ./data/committee/...`
8. Run benchmark: `go test -bench=BenchmarkSortition ./data/committee/`
9. Run `make shorttest` to verify no regressions
10. Verify deterministic pinned outputs remain unchanged
