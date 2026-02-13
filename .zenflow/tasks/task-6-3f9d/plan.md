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
<!-- chat-id: 91605451-0887-476f-8292-27e0cc449702 -->

**Completed.** See `spec.md` for the full technical specification.

**Summary:**
- **Difficulty:** Medium
- **Task:** Modify absenteeism detection in `ledger/eval/` to use external weight values instead of stake
- **Prerequisite:** Task 1 implemented `ledgercore.AbsenteeismMultiplier` and `ExternalWeighter` interface

---

### [x] Step: Expose balanceRound() via roundCowParent interface
<!-- chat-id: 52bbfd09-8673-498a-9f22-4f10cdffcac8 -->

Modify `cow.go` and `applications.go` to expose `balanceRound()` method via the `roundCowParent` interface.

**Files to modify:**
- `ledger/eval/cow.go` - Add `balanceRound() (basics.Round, error)` to `roundCowParent` interface
- `ledger/eval/applications.go` - Add delegation method on `roundCowState`

**Verification:**
- `go build ./ledger/eval/...` compiles successfully
- `go test ./ledger/eval/...` passes

**Completed.** Changes made:
- Added `balanceRound() (basics.Round, error)` to `roundCowParent` interface in `cow.go:49`
- Added delegation method `balanceRound()` on `roundCowState` in `applications.go:59-61`
- Updated test mocks (`emptyLedger` in `appcow_test.go`, `mockLedger` in `cow_test.go`) to implement the new interface method

---

### [x] Step: Implement isAbsentByWeight() function and update absenteeism paths
<!-- chat-id: b186424d-fc0c-4e86-bc14-aae533ddd7ad -->

Implement the core weight-based absenteeism logic:

1. Add `isAbsentByWeight()` function to `eval.go`
2. Add compile-time assertion that `absentFactor` equals `ledgercore.AbsenteeismMultiplier`
3. Modify `generateKnockOfflineAccountsList()` to use weight-based absence detection
4. Modify `validateAbsentOnlineAccounts()` to use weight-based absence detection
5. Add unit tests for `isAbsentByWeight()` in `eval_test.go`
6. Add unit tests for the modified generation/validation paths

**Files to modify:**
- `ledger/eval/eval.go` - Add `isAbsentByWeight()`, modify both absenteeism functions
- `ledger/eval/eval_test.go` - Add comprehensive unit tests

**Verification:**
- `go test -v -run TestIsAbsentByWeight ./ledger/eval/...`
- `go test -v -run TestAbsent ./ledger/eval/...`
- `go test -race ./ledger/eval/...`
- `make sanity` passes

**Completed.** Changes made:

1. **`ledger/eval/eval.go`:**
   - Added compile-time assertion `var _ = [1]int{}[absentFactor-ledgercore.AbsenteeismMultiplier]` at line 1764 to ensure `absentFactor` equals `ledgercore.AbsenteeismMultiplier`
   - Added `isAbsentByWeight(totalWeight, acctWeight uint64, lastSeen, current basics.Round) bool` function at lines 1786-1809
   - Modified `generateKnockOfflineAccountsList()` (lines 1648-1685):
     - Added type assertion for `ExternalWeighter` interface
     - Computes `balanceRound` via `eval.state.balanceRound()`
     - Fetches `totalWeight` via `ew.TotalExternalWeight()`
     - Added cross-check: panics if `onlineStake > 0` but `totalWeight == 0`
     - In loop: fetches `accountWeight` via `ew.ExternalWeight()`, panics if zero
     - Replaced `isAbsent()` with `isAbsentByWeight()`
   - Modified `validateAbsentOnlineAccounts()` (lines 1926-1960):
     - Same weight-based logic as generation path
     - Returns errors instead of panics (appropriate for validation path)

2. **`ledger/eval/eval_test.go`:**
   - Added `ExternalWeighter` implementation to `evalTestLedger`:
     - Added `accountWeights`, `totalWeights` fields for custom weight configuration
     - Added `totalWeightError`, `externalWeightError`, `externalWeightErrorByAddr` fields for error injection
     - Added `ExternalWeight()` and `TotalExternalWeight()` methods with error injection support
     - Added compile-time interface check
   - Added `TestIsAbsentByWeight` test covering:
     - Known intervals calculation
     - Boundary conditions
     - `lastSeen == 0` case
     - `acctWeight == 0` defensive guard
     - Overflow handling
     - Large non-overflowing values
   - Added `TestWeightBasedAbsenteeismCompileTimeCheck` test verifying:
     - `absentFactor` equals `ledgercore.AbsenteeismMultiplier`
     - `evalTestLedger` implements `ExternalWeighter` correctly
   - Added edge case tests per spec requirements:
     - `TestWeightAbsenteeismCrossCheckPanic`: Verifies panic when `onlineStake > 0` but `totalWeight == 0`
     - `TestWeightAbsenteeismZeroWeightAccount`: Verifies panic when account has zero external weight
     - `TestWeightAbsenteeismDaemonErrorInternal`: Verifies `DaemonError{Code: "internal"}` results in empty absent list (no panic)
     - `TestWeightAbsenteeismDaemonErrorNotFound`: Verifies `DaemonError{Code: "not_found"}` causes panic (invariant violation)
     - `TestWeightAbsenteeismEmptyCandidateList`: Verifies empty candidate list is handled gracefully

**All tests pass:**
- `go test -v -run TestIsAbsent ./ledger/eval/...` ✓
- `go test -v -run TestAbsent ./ledger/eval/...` ✓
- `go test -v -run TestWeightAbsenteeism ./ledger/eval/...` ✓
- `go test -race ./ledger/eval/...` ✓
- `make sanity` ✓

---

### [x] Step: Final verification and report
<!-- chat-id: 5954b0a8-5c3b-493b-9054-df60d31ca71c -->

Run full test suite and lint checks, then write implementation report.

**Verification:**
- `make test` passes
- `make sanity` passes
- All existing tests continue to pass

**Deliverable:**
- Write `report.md` with:
  - What was implemented
  - How the solution was tested
  - Any challenges encountered

**Completed.** All verification steps passed:
- `make test` - PASS (exit code 0, `ledger/eval` 66.2% coverage)
- `make sanity` - PASS (0 issues)
- All existing tests continue to pass
- See `report.md` for full implementation summary
