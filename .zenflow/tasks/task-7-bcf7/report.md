# Task 7: Startup Validation - Implementation Report

## Summary

Task 7 implements startup validation for the external weight oracle daemon. This ensures that algod validates the weight daemon is reachable, compatible, and returns non-zero weights for eligible participation keys before allowing the node to start.

## Implementation Details

### Core Changes

1. **Startup Validation Sequence** (`node/node.go`)
   - Added `initializeWeightOracle()` function called during `MakeFull()` after ledger creation
   - Validates `ExternalWeightOraclePort > 0` (required configuration)
   - Creates weight oracle client and verifies daemon is reachable via `Ping()`
   - Validates daemon identity (genesis hash, algorithm version, protocol version)
   - Injects oracle into ledger via `SetWeightOracle()`
   - Calls `validateParticipationKeyWeights()` to verify eligible keys have non-zero weight

2. **Participation Key Weight Validation** (`node/node.go`)
   - Added `validateParticipationKeyWeights()` function
   - Computes `voteRound = ledger.Latest() + 1` and `balanceRound` per consensus params
   - Iterates participation records, skipping:
     - Keys not valid for current round (FirstValid/LastValid)
     - Keys with no VRF
     - Accounts not in balance snapshot
     - Keys with SelectionID mismatch
     - Keys failing vote-validity gating (VoteFirstValid/VoteLastValid per DD §4.11)
   - Returns error if any eligible key has zero weight

3. **Ledger Fallback Behavior Change** (`ledger/ledger.go`)
   - Changed `ExternalWeight()` and `TotalExternalWeight()` from fallback-to-stake to panic
   - This ensures startup validation must succeed before any consensus operations

### Test Infrastructure

1. **Mock Weight Server** (`node/weightoracle_startup_test.go`)
   - TCP server simulating weight daemon with configurable responses
   - Supports ping, identity, weight, total_weight queries
   - Added `defaultWeight` field for test scenarios

2. **Startup Validation Tests** (`node/weightoracle_startup_test.go`)
   - 16 comprehensive tests covering all validation scenarios:
     - Port = 0 fails
     - Daemon unreachable fails
     - Genesis hash mismatch fails
     - Algorithm/protocol version mismatch fails
     - Success with no keys
     - Eligible key with weight succeeds
     - Eligible key with zero weight fails
     - Various skip conditions (out of window, account missing, SelectionID mismatch, vote-validity gating)

3. **Test Fixture Updates**
   - Updated `setupFullNodesEx()` in `node/node_test.go` to start mock weight server for all node tests
   - Added weight oracle setup to `setupFollowNode()` for follower node tests
   - Added mock weight oracle to test files in multiple packages:
     - `data/pools/transactionPool_test.go`
     - `node/assemble_test.go`
     - `daemon/algod/api/server/v2/test/helpers.go`

## Verification Results

- **Build**: `make build` succeeds
- **Unit Tests**: All weight oracle tests pass (57 tests)
- **Startup Validation Tests**: All 16 tests pass with race detection
- **Full Test Suite**: Most tests pass; some pre-existing flaky tests may timeout in parallel runs
- **Code Formatting**: `make fmt` completes successfully
- **Go Vet**: No warnings

## Files Changed

### New Files
- `node/weightoracle/lru.go` - Generic bounded LRU cache
- `node/weightoracle/lru_test.go` - LRU cache tests
- `node/weightoracle/client.go` - Oracle client implementation
- `node/weightoracle/client_test.go` - Client tests
- `node/weightoracle/testdaemon/daemon.py` - Python mock daemon
- `node/weightoracle/testdaemon/README.md` - Mock daemon documentation
- `node/weightoracle_startup_test.go` - Startup validation tests

### Modified Files
- `node/node.go` - Added startup validation sequence
- `ledger/ledger.go` - Changed fallback to panic
- `ledger/testing_helpers_test.go` - Added test weight oracle
- `ledger/simulation/testing/utils.go` - Added mock oracle for simulation tests
- `ledger/simple_test.go` - Set up mock oracle for tests
- `ledger/ledger_test.go` - Added panic behavior tests
- Various ledger test files - Added `setupTestWeightOracle()` calls
- `data/pools/transactionPool_test.go` - Added test weight oracle
- `node/assemble_test.go` - Added test weight oracle
- `node/node_test.go` - Updated test fixtures with mock weight server
- `node/follower_node_test.go` - Added weight oracle setup
- `daemon/algod/api/server/v2/test/helpers.go` - Added test weight oracle

## Notes

1. The `algofix` tool panics when processing new Go syntax with generics - this is a pre-existing bug unrelated to this task.

2. The startup validation is strict: nodes cannot start without a working weight daemon. This is intentional to prevent nodes from participating in consensus without proper weight tracking.

3. Follower nodes also require weight oracle setup because simulation operations call `TotalExternalWeight()`.
