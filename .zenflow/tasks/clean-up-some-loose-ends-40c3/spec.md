# Technical Specification: Clean Up Loose Ends

## Task Overview

This task addresses two changes to the weighted consensus test infrastructure:

1. **Relay node weight daemon**: Remove the requirement for relay nodes to have a weight daemon, since relays don't participate in consensus
2. **Configurable test duration**: Allow the test duration to be specified via command line

## Technical Context

- **Language**: Go 1.21+
- **Test Location**: `test/e2e-go/features/weightoracle/weighted_consensus_test.go`
- **Node Implementation**: `node/node.go`
- **Network Template**: `test/testdata/nettemplates/FiveNodesWeighted.json`

## Difficulty Assessment: Medium

- Moderate complexity with some edge cases to consider
- Requires understanding of node startup sequence and consensus architecture
- Changes affect critical consensus initialization path

---

## Part 1: Relay Node Weight Daemon Removal

### Current Behavior

Currently, ALL nodes (including relay nodes) must:
1. Have `ExternalWeightOraclePort` configured (non-zero)
2. Have a reachable weight daemon at startup
3. Pass identity validation with the daemon

This is enforced in `node/node.go:initializeWeightOracle()` (lines 1114-1172).

### Problem Analysis

Relay nodes:
- Don't have participation keys (they don't participate in consensus)
- Don't propose blocks or vote
- Only relay messages between participating nodes

However, relay nodes still:
- Run the agreement service (validates incoming messages)
- Run ledger evaluation (which may call `ExternalWeight`/`TotalExternalWeight`)
- Need to validate blocks from other nodes

The key insight is that the weight oracle validation in `validateParticipationKeyWeights()` already handles the case where there are no participation keys - it returns early with success.

### Implementation Approach

Modify `initializeWeightOracle()` to check if the node has any participation keys before requiring the weight daemon:

```go
func (node *AlgorandFullNode) initializeWeightOracle() error {
    // Check if this node has any participation keys
    records := node.accountManager.Registry().GetAll()
    hasParticipationKeys := len(records) > 0

    port := node.config.ExternalWeightOraclePort

    // If no participation keys, weight oracle is optional
    if !hasParticipationKeys {
        if port == 0 {
            node.log.Infof("No participation keys and no weight oracle configured; skipping weight oracle initialization")
            return nil
        }
        // If port is configured but node has no keys, we can still connect
        // but validation is unnecessary
    }

    // For participating nodes, weight oracle is required
    if port == 0 {
        return fmt.Errorf("ExternalWeightOraclePort must be configured (required for weighted consensus)")
    }

    // ... rest of validation ...
}
```

### Challenge: Ledger Still Needs Weight Oracle

The challenge is that the ledger's `ExternalWeight()` and `TotalExternalWeight()` methods will panic if no oracle is configured. These methods are called by:
- `agreement/selector.go`: When verifying credentials from other nodes
- `ledger/eval/eval.go`: When generating/validating absent online account lists

**Solution Options**:

1. **Option A**: Relay nodes still need weight oracle for message validation
   - The relay node validates incoming votes/proposals from other nodes
   - This requires looking up the weight of those accounts
   - **Verdict**: Relay nodes DO need weight oracle

2. **Option B**: Modify ledger to handle missing oracle gracefully
   - Change panic to error return
   - Would require changes throughout agreement code
   - **Verdict**: Too invasive

3. **Option C**: Skip weight oracle for relay nodes but set a no-op oracle
   - Create a "pass-through" oracle that uses on-chain stake
   - **Verdict**: Would break weighted consensus invariants

### Conclusion for Part 1

After deeper analysis, **relay nodes DO need the weight oracle** for the following reasons:

1. Relays validate incoming consensus messages (votes, proposals)
2. Credential verification uses `ExternalWeight` to compute committee membership
3. Block evaluation uses `TotalExternalWeight` for absent account handling

The current behavior is correct - all nodes need the weight oracle when running with `ConsensusFuture` (which enables external weights).

**Recommendation**: Document this requirement clearly in the README and test comments, but DO NOT remove the weight daemon requirement for relay nodes.

---

## Part 2: Configurable Test Duration

### Current Behavior

The test uses `testing.Short()` to switch between two hardcoded durations:
- Short mode (`-short` flag): 5 minutes (single checkpoint)
- Full mode: 60 minutes (7 checkpoints)

### Implementation Approach

Add environment variable support for custom test duration:

```go
const (
    // Environment variable to override test duration
    testDurationEnvVar = "WEIGHT_TEST_DURATION"
)

// getTestDuration returns the test duration based on:
// 1. WEIGHT_TEST_DURATION environment variable (if set)
// 2. testing.Short() flag (5 minutes)
// 3. Default full test (60 minutes)
func getTestDuration() time.Duration {
    if envDuration := os.Getenv(testDurationEnvVar); envDuration != "" {
        duration, err := time.ParseDuration(envDuration)
        if err == nil && duration > 0 {
            return duration
        }
        // Log warning but don't fail - fall back to default
    }
    if testing.Short() {
        return 5 * time.Minute
    }
    return 60 * time.Minute
}

// getCheckpoints returns checkpoint intervals for the given duration
func getCheckpoints(totalDuration time.Duration) []time.Duration {
    if totalDuration <= 5*time.Minute {
        return []time.Duration{totalDuration}
    }

    // Generate checkpoints at regular intervals
    // For durations > 5 min, create checkpoints at 5, 10, 20, 30, ... minutes
    var checkpoints []time.Duration
    intervals := []time.Duration{5 * time.Minute}

    // Add 10 minute mark if duration allows
    if totalDuration >= 10*time.Minute {
        intervals = append(intervals, 10*time.Minute)
    }

    // Add 10-minute intervals after that
    for t := 20 * time.Minute; t <= totalDuration; t += 10 * time.Minute {
        intervals = append(intervals, t)
    }

    // Ensure final checkpoint is at totalDuration
    if intervals[len(intervals)-1] != totalDuration {
        intervals = append(intervals, totalDuration)
    }

    return intervals
}
```

### Files to Modify

1. **`test/e2e-go/features/weightoracle/weighted_consensus_test.go`**:
   - Add `getTestDuration()` function
   - Add `getCheckpoints()` function
   - Replace hardcoded checkpoint selection with dynamic generation
   - Update `TestWeightedConsensus` to use these functions

2. **`test/e2e-go/features/weightoracle/README.md`**:
   - Document the new `WEIGHT_TEST_DURATION` environment variable
   - Add usage examples

### Usage Examples

```bash
# Use default (60 minutes)
go test -v ./test/e2e-go/features/weightoracle -run TestWeightedConsensus

# Use short mode (5 minutes)
go test -v ./test/e2e-go/features/weightoracle -run TestWeightedConsensus -short

# Use custom duration (15 minutes)
WEIGHT_TEST_DURATION=15m go test -v ./test/e2e-go/features/weightoracle -run TestWeightedConsensus

# Use custom duration (2 hours)
WEIGHT_TEST_DURATION=2h go test -v ./test/e2e-go/features/weightoracle -run TestWeightedConsensus -timeout 130m
```

---

## Implementation Tasks

### Task 1: Update Test Duration Handling

**File**: `test/e2e-go/features/weightoracle/weighted_consensus_test.go`

**Changes**:
1. Add `const testDurationEnvVar = "WEIGHT_TEST_DURATION"`
2. Add `getTestDuration()` function to parse environment variable
3. Add `getCheckpoints(totalDuration time.Duration)` function
4. Update `TestWeightedConsensus` to use dynamic checkpoints
5. Add logging to show configured duration source

**Tests**:
- Verify default behavior (60 min) unchanged
- Verify `-short` flag still works (5 min)
- Verify custom duration via environment variable
- Verify invalid duration falls back to default

### Task 2: Update Documentation

**File**: `test/e2e-go/features/weightoracle/README.md`

**Changes**:
1. Add section on custom test duration
2. Document `WEIGHT_TEST_DURATION` environment variable
3. Add usage examples
4. Update architecture notes if needed

### Task 3: Document Relay Node Requirements (Instead of Code Change)

**File**: `test/e2e-go/features/weightoracle/README.md`

**Changes**:
1. Add clear documentation explaining why relay nodes need weight daemon
2. Update architecture diagram comment to remove "for protocol compliance"
3. Replace with more accurate explanation about credential validation

---

## Verification Approach

1. **Unit tests**: N/A (changes are in E2E test infrastructure)

2. **Manual testing**:
   ```bash
   # Test default duration
   go test -v ./test/e2e-go/features/weightoracle -run TestWeightedConsensus -timeout 70m

   # Test short duration
   go test -v ./test/e2e-go/features/weightoracle -run TestWeightedConsensus -short

   # Test custom duration (2 minutes for quick verification)
   WEIGHT_TEST_DURATION=2m go test -v ./test/e2e-go/features/weightoracle -run TestWeightedConsensus -timeout 5m
   ```

3. **Verify checkpoints**:
   - Confirm correct number of checkpoints are generated for each duration
   - Verify final checkpoint matches requested duration

---

## Summary of Changes

| Change | File | Description |
|--------|------|-------------|
| Add duration env var | `weighted_consensus_test.go` | Support `WEIGHT_TEST_DURATION` |
| Dynamic checkpoints | `weighted_consensus_test.go` | Generate checkpoints based on duration |
| Documentation | `README.md` | Document custom duration and relay requirements |

## Non-Changes (After Analysis)

| Originally Proposed | Reason Not Changed |
|---------------------|-------------------|
| Remove relay weight daemon | Relay nodes need weight oracle to validate incoming consensus messages |
