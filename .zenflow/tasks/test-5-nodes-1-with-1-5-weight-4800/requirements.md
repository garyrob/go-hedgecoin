# Product Requirements Document: Weighted Consensus Multi-Node Test

## 1. Overview

### 1.1 Purpose
Create an end-to-end test that validates the external weight oracle feature by demonstrating that nodes with different consensus weights earn block rewards (and thus propose blocks) proportional to their assigned weights.

### 1.2 Background
The go-algorand codebase has been modified to support external weight oracles - daemons that supply voting weights to nodes for consensus participation. This test validates that the weight system works correctly by running a multi-node network where one node has 1.5x the weight of others.

### 1.3 Location
The test will be created in: `test/e2e-go/features/weightoracle/`

### 1.4 Relationship Between Blocks Proposed and Coins Earned
In Algorand consensus, block proposers earn rewards. The probability of proposing a block is proportional to the node's consensus weight. Therefore:
- **Blocks proposed** is a direct measure of consensus participation
- **Coins earned** is proportional to blocks proposed (via block rewards/bonuses)

This test measures **blocks proposed** as the primary metric, which directly validates the weighted consensus mechanism. Coins earned would follow proportionally but adds complexity (fee sink state, bonus amounts, etc.) without additional signal.

## 2. Functional Requirements

### 2.1 Network Configuration

**FR-1**: The test SHALL run 5 algod nodes participating in consensus, plus 1 relay node for network connectivity.

**FR-2**: The test SHALL run 5 weight oracle daemons, one per participating algod node. The relay node does not require a weight daemon.

**FR-3**: Each participating algod node SHALL be configured to connect to exactly one weight daemon via the `ExternalWeightOraclePort` configuration parameter.

**FR-4**: Four (4) weight daemons SHALL return a weight of `1000000` (representing normalized weight 1.0) for their associated node's participation key.

**FR-5**: One (1) weight daemon SHALL return a weight of `1500000` (representing normalized weight 1.5) for its associated node's participation key.

**FR-6**: All weight daemons SHALL return a total weight of `5500000`.
- Calculation: 4 nodes × 1,000,000 + 1 node × 1,500,000 = 5,500,000
- In normalized terms: 4 × 1.0 + 1 × 1.5 = 5.5

**FR-7**: Each participating node SHALL have equal stake in the genesis configuration (20% each). The external weights (not the stake) determine consensus voting power in weighted consensus mode.

### 2.2 Test Duration and Checkpoints

**FR-8**: The test SHALL run for up to 60 minutes.

**FR-9**: The test SHALL record statistics at the following intervals:
- 5 minutes (early validation per original task)
- 10 minutes
- 20 minutes
- 30 minutes
- 40 minutes
- 50 minutes
- 60 minutes (final)

**FR-10**: At each checkpoint, the test SHALL output:
- Current round number
- Number of blocks proposed by each node
- Ratio of blocks proposed by the 1.5-weight node to the average blocks proposed by each 1.0-weight node

### 2.3 Weight Daemon Configuration

**FR-11**: Weight daemons SHALL use the Python test daemon at `node/weightoracle/testdaemon/daemon.py`.

**FR-12**: Each weight daemon SHALL be configured with the correct genesis hash from the test network.

**FR-13**: Each weight daemon SHALL be configured with protocol version "1.0" and algorithm version "1.0".

**FR-14**: Weight daemons SHALL be configured with a weight file (JSON) that specifies weights for all possible address/selection_id combinations. Since the daemon uses key-based lookup with fallback to address hashing, the test SHALL either:
- **(Option A - Preferred)**: Modify `daemon.py` to accept a `--default-weight` parameter that returns this weight for ALL queries, bypassing the weight table lookup. This is a prerequisite task.
- **(Option B)**: Pre-populate the weight table with wildcard or pattern-based matching.
- **(Option C)**: Accept that the address-hash fallback (`sum(ord(c) for c in address) % 1000000`) will produce different weights per address - but this defeats the purpose of controlled weight testing.

**FR-15**: The test SHALL implement Option A by adding a `--default-weight` parameter to `daemon.py` as a prerequisite task.

### 2.4 Node Configuration

**FR-16**: Each participating node SHALL have its own data directory.

**FR-17**: Each participating node SHALL be configured with `ExternalWeightOraclePort` pointing to its designated weight daemon.

**FR-18**: All 5 participating nodes SHALL participate in consensus (online with participation keys).

**FR-19**: The relay node provides network connectivity but does not participate in consensus.

**FR-20**: The network SHALL use fast consensus parameters for timely test execution (targeting ~1 round per 400ms).

### 2.5 Data Collection

**FR-21**: The test SHALL track which node proposed each block by examining the block's `Proposer` field.

**FR-22**: The test SHALL maintain a count of blocks proposed by each node.

**FR-23**: The test SHALL compute the ratio: `blocks_proposed_by_weighted_node / average(blocks_proposed_by_each_normal_node)`.

### 2.6 Success Criteria

**FR-24**: After 5 minutes, the ratio SHOULD show the weighted node proposing more blocks than the average of other nodes (ratio > 1.0).

**FR-25**: After 60 minutes, the ratio of blocks proposed by the 1.5-weight node compared to the average of 1.0-weight nodes SHOULD be between 1.35 and 1.65 (within 10% of the expected 1.5 ratio).

**FR-26**: The test SHALL NOT fail if the ratio is outside this range, but SHALL clearly report the observed ratio for manual analysis. This is because consensus is probabilistic and variance is expected.

**FR-27**: The test SHALL output a summary table at the end showing the progression of ratios over time.

## 3. Technical Requirements

### 3.1 Dependencies

**TR-1**: The test requires Python 3 to run the weight daemon.

**TR-2**: The test requires the `node/weightoracle/testdaemon/daemon.py` script.

**TR-3**: The test uses the go-algorand test framework (`test/framework/fixtures`).

**TR-4**: **PREREQUISITE**: The `daemon.py` script must be modified to accept a `--default-weight` parameter that overrides the address-hash fallback behavior (see FR-15).

### 3.2 Port Allocation

**TR-5**: Weight daemons SHALL use ports 19001-19005 to avoid conflicts with other services.

**TR-6**: Node REST API and other ports SHALL be dynamically allocated by the test framework (standard behavior).

### 3.3 Process Management

**TR-7**: The test SHALL start all 5 weight daemons before starting the algod nodes.

**TR-8**: The test SHALL verify each daemon is healthy (responds to ping) before proceeding with node startup.

**TR-9**: The test SHALL gracefully shut down all weight daemons on test completion (success or failure).

**TR-10**: The test SHALL use Go's `os/exec` package to manage daemon processes.

### 3.4 Genesis Configuration

**TR-11**: The test SHALL create a custom network template with:
- 1 relay node (no wallet, no participation)
- 5 participating nodes with equal stake (20% each)
- All participating wallets online from genesis

**TR-12**: The network template SHALL be created as `test/testdata/nettemplates/FiveNodesWeighted.json`.

### 3.5 Consensus Parameters

**TR-13**: The test SHALL use accelerated consensus parameters (via `fixture.FasterConsensus()`) to generate blocks faster than production.

**TR-14**: Target block time SHALL be approximately 400ms to achieve ~750 blocks in 5 minutes.

### 3.6 Error Handling

**TR-15**: The test SHALL fail with a clear error message if any weight daemon fails to start.

**TR-16**: The test SHALL fail with a clear error message if any node fails to connect to its weight daemon.

**TR-17**: The test SHALL continue running even if individual round data collection fails, logging warnings.

## 4. Output Format

### 4.1 Progress Output

At each checkpoint:
```
=== Checkpoint at T+5m (Round ~750) ===
Node1 (weight=1.0): 135 blocks proposed
Node2 (weight=1.0): 142 blocks proposed
Node3 (weight=1.0): 128 blocks proposed
Node4 (weight=1.0): 140 blocks proposed
Node5 (weight=1.5): 205 blocks proposed

Average (weight=1.0 nodes): 136.25 blocks
Weighted node (Node5): 205 blocks
Ratio: 1.50
```

### 4.2 Final Summary

```
=== Final Summary ===
Time     | Rounds  | Node1 | Node2 | Node3 | Node4 | Node5 | Ratio
---------|---------|-------|-------|-------|-------|-------|------
5min     |     750 |   135 |   142 |   128 |   140 |   205 |  1.50
10min    |    1500 |   270 |   284 |   256 |   280 |   410 |  1.51
20min    |    3000 |   540 |   568 |   512 |   560 |   820 |  1.51
30min    |    4500 |   810 |   852 |   768 |   840 |  1230 |  1.50
40min    |    6000 |  1080 |  1136 |  1024 |  1120 |  1640 |  1.50
50min    |    7500 |  1350 |  1420 |  1280 |  1400 |  2050 |  1.50
60min    |    9000 |  1620 |  1704 |  1536 |  1680 |  2460 |  1.50

Expected ratio: 1.50
Observed final ratio: 1.50
Deviation: +0.0%
```

## 5. Implementation Notes

### 5.1 Weight Daemon Modification (Prerequisite Task)

The `daemon.py` script needs a `--default-weight` parameter. When provided:
1. Skip the weight table lookup entirely for weight queries
2. Return the specified default weight for ALL weight queries
3. Total weight should still be configurable via `--total-weight`

Example usage:
```bash
python daemon.py --port 19001 --default-weight 1000000 --total-weight 5500000 --genesis-hash <hash>
```

This modification is straightforward (~10 lines) and should be implemented as part of the test setup tasks.

### 5.2 Measuring Block Proposals

Use `fixture.WithEveryBlock()` or iterate blocks periodically to count proposers. The `block.Proposer()` field identifies which account proposed each block. Map accounts back to nodes via the network template.

### 5.3 Node Directory Structure

```
/tmp/TestWeightedConsensus/
├── Relay/             # Relay node (no weight daemon)
├── Node1/             # Participating node with weight=1.0
├── Node2/             # Participating node with weight=1.0
├── Node3/             # Participating node with weight=1.0
├── Node4/             # Participating node with weight=1.0
├── Node5/             # Participating node with weight=1.5
└── genesis.json       # Shared genesis file
```

Weight daemons run as separate processes, not in the test directory.

## 6. Risks and Mitigations

### 6.1 Statistical Variance
**Risk**: Due to the probabilistic nature of consensus, the actual ratio may deviate from 1.5.
**Mitigation**:
- Run for 60 minutes to collect ~9000 samples
- Use 10% tolerance at final checkpoint
- Use 5-minute early checkpoint for directional validation only

### 6.2 Weight Daemon Startup Timing
**Risk**: Nodes may start before their weight daemon is ready.
**Mitigation**: Ping each daemon and wait for success before starting nodes.

### 6.3 Port Conflicts
**Risk**: Weight daemon ports may conflict with other processes.
**Mitigation**: Use ports in high range (19001+) and check availability.

### 6.4 Test Duration
**Risk**: 60-minute test may be too long for CI.
**Mitigation**: Make the test skippable with `-short` flag. Add a 5-minute smoke test variant.

## 7. Prerequisite Tasks

Before the main test can be implemented, these tasks must be completed:

1. **Modify `daemon.py`**: Add `--default-weight` parameter (TR-4, FR-15)
2. **Create network template**: `FiveNodesWeighted.json` (TR-12)

## 8. Future Enhancements

1. Add negative tests (weight daemon returns 0, weight daemon unavailable)
2. Add dynamic weight change tests
3. Add tests with varying network conditions (latency, partitions)
4. Statistical analysis with confidence intervals
