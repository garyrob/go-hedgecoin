# Weight Oracle Multi-Node Test

This directory contains end-to-end tests for the external weight oracle feature, specifically testing that nodes with different weights propose blocks (and earn rewards) proportional to their weights.

## Test Overview

The main test (`weighted_consensus_test.go`) runs a 6-node network where:
- 1 relay node provides network connectivity (with weight daemon for protocol compliance)
- 4 participating nodes have weight 1.0 (1,000,000 microalgos supplied by their weight daemon)
- 1 participating node has weight 1.5 (1,500,000 microalgos supplied by its weight daemon)

The test verifies that over time, the node with weight 1.5 proposes approximately 1.5x as many blocks as each of the other nodes.

## Architecture

```
                              Test Network
+--------------------------------------------------------------------------+
|                                                                           |
|   +------------+                                                          |
|   |   Relay    |  (with weight daemon for protocol compliance)            |
|   +------------+                                                          |
|         |                                                                 |
|    +----+----+----+----+----+                                             |
|    |    |    |    |    |    |                                             |
|    v    v    v    v    v    v                                             |
|  +----+ +----+ +----+ +----+ +----+                                       |
|  |Node| |Node| |Node| |Node| |Node|                                       |
|  | 1  | | 2  | | 3  | | 4  | | 5  |                                       |
|  +----+ +----+ +----+ +----+ +----+                                       |
|    ^      ^      ^      ^      ^                                          |
|    |      |      |      |      |                                          |
|  +----+ +----+ +----+ +----+ +----+                                       |
|  |Dmn | |Dmn | |Dmn | |Dmn | |Dmn |                                       |
|  |1.0M| |1.0M| |1.0M| |1.0M| |1.5M|  <-- Higher weight                    |
|  +----+ +----+ +----+ +----+ +----+                                       |
|                                                                           |
|   All daemons share the same address_weights.json file                    |
|   (CRITICAL for credential verification)                                  |
|                                                                           |
+--------------------------------------------------------------------------+
```

## Running the Test

```bash
# Set environment variables
export NODEBINDIR=$HOME/go/bin
export TESTDATADIR=$(pwd)/test/testdata
export TESTDIR=/tmp

# Full test (60 minutes)
go test -v ./test/e2e-go/features/weightoracle/... -run TestWeightedConsensus -timeout 70m

# Short version (5 minutes only)
go test -v ./test/e2e-go/features/weightoracle/... -run TestWeightedConsensus -short -timeout 10m
```

## Test Results

### Short Test (5 minutes)

The short test demonstrates the weighted consensus feature working:

| Elapsed | Round | Weighted | Normal Avg | Ratio |
|---------|-------|----------|------------|-------|
| 5m0s    | 107   | 29       | 19.5       | 1.487 |

With ~107 rounds in 5 minutes using production consensus timing, the ratio of 1.487 is very close to the expected 1.5.

### Full Test (60 minutes)

The full test demonstrates the weighted consensus feature converging toward the expected 1.5x ratio:

| Elapsed | Round | Weighted | Normal Avg | Ratio |
|---------|-------|----------|------------|-------|
| 5m0s    | 109   | 34       | 18.5       | 1.838 |
| 10m0s   | 227   | 68       | 39.5       | 1.722 |
| 20m0s   | 462   | 144      | 79.2       | 1.817 |
| 30m0s   | 697   | 214      | 120.5      | 1.776 |
| 40m0s   | 932   | 278      | 163.2      | 1.703 |
| 50m0s   | 1167  | 337      | 207.2      | 1.626 |
| 60m0s   | 1401  | 401      | 249.8      | 1.606 |

**Final ratio: 1.606** (expected: 1.5, tolerance: 1.35-1.65)
**Result: SUCCESS** - Ratio is within expected range

The ratio starts higher due to statistical variance and converges toward the expected 1.5 as more rounds complete.

## Implementation Notes

### Shared Address Weights

The key implementation detail is that **all weight daemons must share the same view of account weights**. This is critical because credential verification uses the receiver's view of stake - when Node A verifies a credential from Node B, Node A looks up Node B's weight using its own daemon.

The test achieves this by:
1. Extracting wallet addresses from genesis.json after network setup
2. Creating a shared `address_weights.json` file mapping each address to its weight
3. Starting all daemons with `--address-weights-file` pointing to this shared file

### Consensus Protocol

The test uses `ConsensusFuture` (which has external weight oracle support) with default consensus parameters. No custom timeouts or consensus modifications are needed - the standard production consensus timing works correctly.

## Known Issues

1. **High Variance with Small Samples**: With only ~100 rounds in the short test, the ratio between weighted and normal nodes can show variance. The expected 1.5x ratio becomes more accurate with longer test runs.

2. **Test Does Not Assert Ratio**: The test reports the ratio but does not fail if it's outside the expected range (1.35-1.65). This is intentional due to statistical variance.

## Expected Results

After running, the test outputs a table at checkpoints showing:
- Round number
- Blocks proposed by each node
- Ratio of Node5 proposals to average of other nodes

Expected ratios converge toward 1.5 as more rounds complete:
- At 5 minutes: ~1.3 - 1.7 (high variance)
- At 60 minutes: ~1.35 - 1.65 (within 10% of 1.5)

## Prerequisites

1. The test daemon (`node/weightoracle/testdaemon/daemon.py`) must support `--address-weights-file` parameter
2. Network template `test/testdata/nettemplates/FiveNodesWeighted.json` must exist
3. All binaries must be built and installed: `make install`

## Port Usage

Weight daemons use dynamically allocated ports based on PID to avoid conflicts:
- Base port: 19001 + (PID % 1000) * 10
- 6 ports total (5 participating nodes + 1 relay)
