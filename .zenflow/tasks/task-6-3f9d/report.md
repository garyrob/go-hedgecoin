# Task 6: Weight-Based Absenteeism Detection Implementation Report

## Summary

This task modified the absenteeism detection system in `ledger/eval/` to use external weight values instead of stake for determining when accounts should be knocked offline. The implementation builds upon Task 1's `ExternalWeighter` interface and `AbsenteeismMultiplier` constant.

## What Was Implemented

### 1. Interface Extension (`ledger/eval/cow.go`)

Added `balanceRound() (basics.Round, error)` method to the `roundCowParent` interface at line 49, enabling weight lookups at the correct historical round.

### 2. Interface Delegation (`ledger/eval/applications.go`)

Added a delegation method `balanceRound()` on `roundCowState` at lines 59-61 that calls through to the underlying parent's implementation.

### 3. Core Weight-Based Logic (`ledger/eval/eval.go`)

**Compile-Time Safety:**
- Added assertion at line 1764: `var _ = [1]int{}[absentFactor-ledgercore.AbsenteeismMultiplier]`
- This ensures `absentFactor` (20) equals `ledgercore.AbsenteeismMultiplier` at compile time

**New Function - `isAbsentByWeight()` (lines 1786-1809):**
- Signature: `isAbsentByWeight(totalWeight, acctWeight uint64, lastSeen, current basics.Round) bool`
- Calculates known intervals using weight-based formula:
  - `known = (totalWeight / acctWeight) * absentFactor`
- Returns true if `current - lastSeen > known`
- Handles edge cases: `lastSeen == 0`, `acctWeight == 0`, overflow protection

**Modified `generateKnockOfflineAccountsList()` (lines 1648-1685):**
- Type assertion for `ExternalWeighter` interface
- Computes `balanceRound` via `eval.state.balanceRound()`
- Fetches `totalWeight` via `ew.TotalExternalWeight()`
- Cross-check: panics if `onlineStake > 0` but `totalWeight == 0` (invariant violation)
- Per-account: fetches weight via `ew.ExternalWeight()`, panics if zero
- Replaced `isAbsent()` with `isAbsentByWeight()`

**Modified `validateAbsentOnlineAccounts()` (lines 1926-1960):**
- Same weight-based logic as generation path
- Returns errors instead of panics (appropriate for validation)
- Validates that proposed absent list matches calculated absent list

### 4. Test Mocks Updated

**`ledger/eval/appcow_test.go`:**
- Added `balanceRound()` method to `emptyLedger` mock

**`ledger/eval/cow_test.go`:**
- Added `balanceRound()` method to `mockLedger` mock

### 5. Comprehensive Unit Tests (`ledger/eval/eval_test.go`)

**Test Ledger Enhancement:**
- Added `ExternalWeighter` implementation to `evalTestLedger`:
  - `accountWeights`, `totalWeights` fields for custom weight configuration
  - `totalWeightError`, `externalWeightError`, `externalWeightErrorByAddr` fields for error injection
  - `ExternalWeight()` and `TotalExternalWeight()` methods
  - Compile-time interface check

**New Tests:**

1. **`TestIsAbsentByWeight`**: Core function tests covering:
   - Known intervals calculation
   - Boundary conditions
   - `lastSeen == 0` case
   - `acctWeight == 0` defensive guard
   - Overflow handling
   - Large non-overflowing values

2. **`TestWeightBasedAbsenteeismCompileTimeCheck`**: Verifies:
   - `absentFactor` equals `ledgercore.AbsenteeismMultiplier`
   - `evalTestLedger` implements `ExternalWeighter` correctly

3. **`TestWeightAbsenteeismCrossCheckPanic`**: Verifies generation path panics when `onlineStake > 0` but `totalWeight == 0`

4. **`TestWeightAbsenteeismZeroWeightAccount`**: Verifies generation path panics when account has zero external weight

5. **`TestWeightAbsenteeismDaemonErrorInternal`**: Verifies `DaemonError{Code: "internal"}` results in empty absent list (graceful degradation)

6. **`TestWeightAbsenteeismDaemonErrorNotFound`**: Verifies `DaemonError{Code: "not_found"}` causes panic (invariant violation)

7. **`TestWeightAbsenteeismEmptyCandidateList`**: Verifies empty candidate list is handled gracefully

8. **`TestWeightAbsenteeismValidationCrossCheckError`**: Verifies validation path returns error (not panic) when `onlineStake > 0` but `totalWeight == 0`

9. **`TestWeightAbsenteeismValidationZeroWeightError`**: Verifies validation path returns error (not panic) when account has zero external weight

10. **`TestWeightAbsenteeismGenerationValidationConsistency`**: Verifies generation and validation produce identical absent lists for same inputs (consensus-critical test)

## Files Modified

| File | Changes |
|------|---------|
| `ledger/eval/cow.go` | Added `balanceRound()` to interface |
| `ledger/eval/applications.go` | Added delegation method |
| `ledger/eval/eval.go` | Added `isAbsentByWeight()`, modified generation/validation functions |
| `ledger/eval/eval_test.go` | Added comprehensive tests and enhanced test ledger |
| `ledger/eval/appcow_test.go` | Updated mock |
| `ledger/eval/cow_test.go` | Updated mock |

## Testing

### Commands Run

```bash
# Package-specific tests
go test -v -run TestIsAbsent ./ledger/eval/...        # PASS
go test -v -run TestAbsent ./ledger/eval/...          # PASS
go test -v -run TestWeightAbsenteeism ./ledger/eval/... # PASS
go test -race ./ledger/eval/...                        # PASS (66.2% coverage)

# Full test suite
make test    # PASS (exit code 0)
make sanity  # PASS (0 issues)
```

### Key Test Results

- All `ledger/eval` tests pass with race detection enabled
- Package coverage: 66.2% of statements
- No lint issues
- No formatting issues

## Key Design Decisions

1. **Panic vs Error**: Generation path uses panics for invariant violations (should never happen in production); validation path uses errors for graceful rejection of invalid blocks.

2. **DaemonError Handling**:
   - `"internal"` errors (daemon connectivity issues) result in empty absent list
   - `"not_found"` errors for accounts with stake are treated as invariant violations (panic/error)

3. **Cross-Check**: Added safety check that panics if `onlineStake > 0` but `totalWeight == 0` - this would indicate a serious system misconfiguration.

4. **Formula**: Uses same multiplier (`absentFactor = 20`) as stake-based approach, but with weight instead of microAlgos.

## Challenges Encountered

1. **Interface Threading**: The `balanceRound()` method needed to be threaded through the `roundCowParent` interface and multiple implementation layers.

2. **Mock Updates**: Multiple test mocks needed to be updated to implement the extended interface.

3. **Error Injection**: Test ledger needed enhancement to support error injection for comprehensive edge case testing.

## Conclusion

Task 6 successfully implements weight-based absenteeism detection. The implementation:
- Maintains the same absenteeism formula structure (using multiplier of 20)
- Replaces stake with external weight values
- Includes comprehensive error handling and safety checks
- Has thorough test coverage for all edge cases
- Passes all existing tests and lint checks
