# Technical Specification: Weighted Consensus Multi-Node Test

## 1. Technical Context

### 1.1 Language & Dependencies
- **Primary Language**: Go 1.21+ (test code)
- **Secondary Language**: Python 3 (weight daemon)
- **Test Framework**: Go standard testing + `stretchr/testify`
- **Test Infrastructure**: `test/framework/fixtures` package
- **Consensus Version**: `protocol.ConsensusFuture`

### 1.2 Relevant Codebase Components

| Component | Location | Purpose |
|-----------|----------|---------|
| Weight Oracle Client | `node/weightoracle/client.go` | TCP client for weight daemon |
| Weight Oracle Interface | `ledger/ledgercore/weightoracle.go` | `WeightOracle` interface |
| Test Daemon | `node/weightoracle/testdaemon/daemon.py` | Mock weight server |
| Node Configuration | `config/localTemplate.go` | `ExternalWeightOraclePort` field |
| Node Initialization | `node/node.go:initializeWeightOracle()` | Weight oracle setup |
| Agreement Integration | `agreement/selector.go:membership()` | Weight-based selection |
| Test Fixtures | `test/framework/fixtures/` | E2E test infrastructure |
| Network Templates | `netdeploy/networkTemplate.go` | Network configuration |
| Template Override | `netdeploy/network.go:63-64` | Dynamic template modification |

### 1.3 Existing Patterns

**E2E Test Pattern** (from `test/e2e-go/features/incentives/payouts_test.go`):
```go
func TestBasicPayouts(t *testing.T) {
    partitiontest.PartitionTest(t)
    defer fixtures.ShutdownSynchronizedTest(t)
    t.Parallel()

    consensusVersion := protocol.ConsensusFuture
    var fixture fixtures.RestClientFixture
    const lookback = 32
    fixture.FasterConsensus(consensusVersion, time.Second, lookback)
    fixture.Setup(t, filepath.Join("nettemplates", "Template.json"))
    defer fixture.Shutdown()

    // Get block via client.BookkeepingBlock()
    block, err := client.BookkeepingBlock(status.LastRound)
    proposer := block.Proposer()
}
```

**Address-to-Node Mapping Pattern** (from `payouts_test.go`):
```go
addressToNode := make(map[string]string)
clientAndAccount := func(name string) (libgoal.Client, model.Account) {
    c := fixture.GetLibGoalClientForNamedNode(name)
    accounts, err := fixture.GetNodeWalletsSortedByBalance(c)
    a.NoError(err)
    a.Len(accounts, 1)
    addressToNode[accounts[0].Address] = name
    return c, accounts[0]
}
```

**Template Override Pattern** (from `netdeploy/network.go`):
```go
type TemplateOverride func(*NetworkTemplate)

// Usage in fixture:
fixture.SetupNoStart(t, templateFile, overrideFunc)
```

## 2. Implementation Approach

### 2.1 Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              Test Process                                    │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │  TestWeightedConsensus                                                  │ │
│  │  ├─ Allocate ports (PID-based + availability check)                     │ │
│  │  ├─ SetupNoStart with TemplateOverride (inject dynamic ports)           │ │
│  │  ├─ Read genesis.json to get genesis hash                               │ │
│  │  ├─ Start 5 weight daemons (with genesis hash)                          │ │
│  │  ├─ Start: Launch algod nodes                                           │ │
│  │  ├─ Build address-to-node mapping                                       │ │
│  │  ├─ Run: Collect block proposer data at checkpoints                     │ │
│  │  └─ Report: Output ratio statistics                                     │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 2.2 Key Implementation Decisions

1. **Daemon Default Weight Feature**: The test daemon (`daemon.py`) needs a `--default-weight` parameter to return the same weight for all queries, bypassing the address-hash fallback behavior.

2. **Dynamic Port Injection via TemplateOverride** (Critical):
   The network template has placeholder ports. At runtime, we:
   - Allocate available ports dynamically
   - Use `netdeploy.TemplateOverride` to modify `ConfigJSONOverride` for each node
   - Pass the override function to `fixture.SetupNoStart()`

   This ensures nodes connect to the same ports where daemons are actually listening.

3. **Block Proposer Tracking**: Use `client.BookkeepingBlock(round)` to retrieve blocks and examine `block.Proposer()` to count proposals per node.

4. **Checkpoint-Based Reporting**: Collect statistics at fixed intervals (5, 10, 20, 30, 40, 50, 60 minutes) rather than every block.

5. **Genesis Hash Acquisition Sequence** (Critical):
   ```
   1. Allocate ports          → Determine available port range
   2. fixture.SetupNoStart()  → Creates network with dynamic port override
   3. Read genesis.json       → Extract genesis hash (Hash() is a method!)
   4. Start weight daemons    → Pass genesis hash to each daemon
   5. fixture.Start()         → Launch algod nodes (connect to daemons)
   ```

6. **Consensus Version**: Use `protocol.ConsensusFuture` for access to latest consensus features.

### 2.3 Port Allocation Strategy

To mitigate port conflicts in CI environments with parallel test execution:

1. **Base port calculation**: Use test process PID modulo to offset base port
   ```go
   basePort := 19001 + (os.Getpid() % 1000) * 10
   ```

2. **Port availability check**: Before starting each daemon, verify port is available:
   ```go
   func isPortAvailable(port int) bool {
       ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
       if err != nil {
           return false
       }
       ln.Close()
       return true
   }
   ```

3. **Retry with offset**: If ports unavailable, increment base by 100 and retry (up to 5 times)

4. **Inject into template**: Use `TemplateOverride` to set allocated ports in `ConfigJSONOverride`

## 3. Source Code Structure Changes

### 3.1 New Files

```
test/
├── e2e-go/
│   └── features/
│       └── weightoracle/
│           ├── README.md                      # (exists) - update with implementation details
│           └── weighted_consensus_test.go     # NEW: Main E2E test
└── testdata/
    └── nettemplates/
        └── FiveNodesWeighted.json             # NEW: 6-node network template (placeholder ports)
```

### 3.2 Modified Files

```
node/weightoracle/testdaemon/
└── daemon.py                                  # ADD: --default-weight parameter
```

### 3.3 File Details

#### 3.3.1 `daemon.py` Modifications

Add `--default-weight` CLI parameter that:
- When set, returns this weight for ALL weight queries (bypassing weight table lookup)
- When not set, maintains existing behavior (table lookup → address hash fallback)

```python
# New CLI argument (type=int, argparse handles conversion)
parser.add_argument(
    "--default-weight",
    type=int,
    default=None,
    help="Default weight to return for ALL weight queries (bypasses table lookup)"
)

# Store in __init__
def __init__(self, ..., default_weight: int | None = None):
    self.default_weight = default_weight
    # ... existing initialization ...

# Modified _handle_weight() method
def _handle_weight(self, request: dict[str, Any]) -> dict[str, Any]:
    # Validate required fields
    address = request.get("address")
    selection_id = request.get("selection_id")
    balance_round = request.get("balance_round")

    if not address:
        return {"error": "Missing address field", "code": "bad_request"}
    if not selection_id:
        return {"error": "Missing selection_id field", "code": "bad_request"}
    if not balance_round:
        return {"error": "Missing balance_round field", "code": "bad_request"}

    # NEW: Return default weight if configured
    if self.default_weight is not None:
        return {"weight": str(self.default_weight)}

    # Existing table lookup and fallback logic...
    key = f"{address}:{selection_id}:{balance_round}"
    with self._lock:
        if key in self.weight_table:
            weight = self.weight_table[key]
        else:
            weight = sum(ord(c) for c in address) % 1000000
    return {"weight": str(weight)}
```

**Note**: The existing `--total-weight` uses `type=int`, so `--default-weight` follows the same pattern. Values like 1,000,000 and 1,500,000 are valid (well above the hash fallback's max of 999,999).

#### 3.3.2 `FiveNodesWeighted.json` Network Template

The template uses placeholder port 0 which will be overridden at runtime:

```json
{
    "Genesis": {
        "NetworkName": "tbd",
        "LastPartKeyRound": 50000,
        "Wallets": [
            {"Name": "Wallet1", "Stake": 20, "Online": true},
            {"Name": "Wallet2", "Stake": 20, "Online": true},
            {"Name": "Wallet3", "Stake": 20, "Online": true},
            {"Name": "Wallet4", "Stake": 20, "Online": true},
            {"Name": "Wallet5", "Stake": 20, "Online": true}
        ]
    },
    "Nodes": [
        {
            "Name": "Relay",
            "IsRelay": true
        },
        {
            "Name": "Node1",
            "Wallets": [{"Name": "Wallet1", "ParticipationOnly": true}]
        },
        {
            "Name": "Node2",
            "Wallets": [{"Name": "Wallet2", "ParticipationOnly": true}]
        },
        {
            "Name": "Node3",
            "Wallets": [{"Name": "Wallet3", "ParticipationOnly": true}]
        },
        {
            "Name": "Node4",
            "Wallets": [{"Name": "Wallet4", "ParticipationOnly": true}]
        },
        {
            "Name": "Node5",
            "Wallets": [{"Name": "Wallet5", "ParticipationOnly": true}]
        }
    ]
}
```

**Note**:
- No `ConfigJSONOverride` in the template - ports are injected dynamically via `TemplateOverride`
- `LastPartKeyRound: 50000` supports ~5.5 hours at 400ms/block, well above the 60-minute test requirement (~9000 rounds)

#### 3.3.3 `weighted_consensus_test.go` Structure

```go
package weightoracle

import (
    "encoding/base64"
    "encoding/json"
    "fmt"
    "net"
    "os"
    "os/exec"
    "path/filepath"
    "runtime"
    "testing"
    "time"

    "github.com/stretchr/testify/require"

    "github.com/algorand/go-algorand/data/basics"
    "github.com/algorand/go-algorand/data/bookkeeping"
    "github.com/algorand/go-algorand/libgoal"
    "github.com/algorand/go-algorand/netdeploy"
    "github.com/algorand/go-algorand/protocol"
    "github.com/algorand/go-algorand/test/framework/fixtures"
    "github.com/algorand/go-algorand/test/partitiontest"
)

// Test configuration constants
const (
    numNodes        = 5
    weightedNodeIdx = 4  // Node5 (0-indexed)
    normalWeight    = 1000000
    weightedWeight  = 1500000
    totalWeight     = 5500000  // 4*1M + 1*1.5M

    // Daemon startup configuration
    daemonStartupTimeout = 10 * time.Second
    daemonPingRetries    = 20
    daemonPingInterval   = 500 * time.Millisecond
)

// Node names (must match template)
var nodeNames = []string{"Node1", "Node2", "Node3", "Node4", "Node5"}

// Normal nodes (all except weighted node) for ratio computation
var normalNodes = []string{"Node1", "Node2", "Node3", "Node4"}

// Weighted node name
const weightedNodeName = "Node5"

// Checkpoint intervals
var checkpoints = []time.Duration{
    5 * time.Minute,
    10 * time.Minute,
    20 * time.Minute,
    30 * time.Minute,
    40 * time.Minute,
    50 * time.Minute,
    60 * time.Minute,
}

// weightDaemon manages a Python weight daemon process
type weightDaemon struct {
    cmd    *exec.Cmd
    port   int
    weight int
    ready  bool
}

// checkpointStats captures data at each measurement point
type checkpointStats struct {
    elapsed time.Duration
    round   basics.Round
    counts  map[string]int // node name → blocks proposed
    ratio   float64        // weighted/average(normal)
}

func TestWeightedConsensus(t *testing.T) {
    partitiontest.PartitionTest(t)
    defer fixtures.ShutdownSynchronizedTest(t)
    t.Parallel()

    a := require.New(fixtures.SynchronizedTest(t))

    // Determine test duration based on -short flag
    testDuration := 60 * time.Minute
    if testing.Short() {
        testDuration = 5 * time.Minute
    }

    consensusVersion := protocol.ConsensusFuture
    const lookback = 32

    var fixture fixtures.RestClientFixture
    fixture.FasterConsensus(consensusVersion, time.Second, lookback)

    // Step 1: Allocate ports BEFORE setting up network
    basePort := allocateBasePorts(t, numNodes)
    t.Logf("Allocated base port: %d", basePort)

    // Step 2: Create TemplateOverride to inject dynamic ports
    portOverride := createPortOverride(basePort)

    // Step 3: SetupNoStart with port override creates network directories and genesis
    fixture.SetupNoStart(t, filepath.Join("nettemplates", "FiveNodesWeighted.json"), portOverride)

    // Step 4: Get genesis hash from created network
    genesisHash := getGenesisHashFromNetwork(t, &fixture)

    // Step 5: Start weight daemons with allocated ports
    daemons := startAllDaemons(t, basePort, genesisHash)

    // Register cleanup with t.Cleanup for robust shutdown
    t.Cleanup(func() {
        for _, d := range daemons {
            stopDaemon(d)
        }
    })

    // Step 6: Verify all daemons are healthy
    for _, d := range daemons {
        waitForDaemonReady(t, d)
    }

    // Step 7: Start the network (nodes will connect to daemons)
    fixture.Start()
    defer fixture.Shutdown()

    // Step 8: Build address-to-node mapping
    addressToNode := buildAddressMapping(t, &fixture, a)

    // Step 9: Run checkpoint loop collecting statistics
    // ... (checkpoint collection and reporting logic)

    _ = testDuration    // Used in checkpoint loop
    _ = addressToNode   // Used for counting proposals by node
}

// createPortOverride returns a TemplateOverride that injects ExternalWeightOraclePort
func createPortOverride(basePort int) netdeploy.TemplateOverride {
    return func(template *netdeploy.NetworkTemplate) {
        for i := range template.Nodes {
            node := &template.Nodes[i]
            // Skip relay node
            if node.IsRelay {
                continue
            }
            // Find node index (Node1 -> 0, Node2 -> 1, etc.)
            nodeIdx := -1
            for j, name := range nodeNames {
                if node.Name == name {
                    nodeIdx = j
                    break
                }
            }
            if nodeIdx < 0 {
                continue
            }
            // Inject port into ConfigJSONOverride
            port := basePort + nodeIdx
            override := map[string]interface{}{
                "ExternalWeightOraclePort": port,
            }
            // Merge with existing override if present
            if node.ConfigJSONOverride != "" {
                var existing map[string]interface{}
                json.Unmarshal([]byte(node.ConfigJSONOverride), &existing)
                for k, v := range existing {
                    override[k] = v
                }
            }
            overrideBytes, _ := json.Marshal(override)
            node.ConfigJSONOverride = string(overrideBytes)
        }
    }
}

// getGenesisHashFromNetwork reads genesis.json from the network directory
func getGenesisHashFromNetwork(t *testing.T, fixture *fixtures.RestClientFixture) string {
    // Network root directory available after SetupNoStart
    genesisPath := filepath.Join(fixture.PrimaryDataDir(), "..", "genesis.json")
    genesis, err := bookkeeping.LoadGenesisFromFile(genesisPath)
    require.NoError(t, err, "failed to load genesis")
    // Note: Hash() is a METHOD that returns crypto.Digest, not a field
    hash := genesis.Hash()
    return base64.StdEncoding.EncodeToString(hash[:])
}

// buildAddressMapping creates a map from wallet address to node name
func buildAddressMapping(t *testing.T, fixture *fixtures.RestClientFixture, a *require.Assertions) map[string]string {
    addressToNode := make(map[string]string)
    for _, nodeName := range nodeNames {
        client := fixture.GetLibGoalClientForNamedNode(nodeName)
        accounts, err := fixture.GetNodeWalletsSortedByBalance(client)
        a.NoError(err)
        a.Len(accounts, 1, "expected exactly 1 wallet for node %s", nodeName)
        addressToNode[accounts[0].Address] = nodeName
        t.Logf("Node %s has address %s", nodeName, accounts[0].Address)
    }
    return addressToNode
}

// allocateBasePorts finds an available port range for daemons
func allocateBasePorts(t *testing.T, count int) int {
    // Try PID-based offset first
    basePort := 19001 + (os.Getpid()%1000)*10

    for attempt := 0; attempt < 5; attempt++ {
        allAvailable := true
        for i := 0; i < count; i++ {
            if !isPortAvailable(basePort + i) {
                allAvailable = false
                break
            }
        }
        if allAvailable {
            return basePort
        }
        basePort += 100 // Try next range
    }
    t.Fatalf("failed to find %d available ports", count)
    return 0
}

func isPortAvailable(port int) bool {
    ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
    if err != nil {
        return false
    }
    ln.Close()
    return true
}

// getDaemonPath returns the absolute path to daemon.py using runtime.Caller
func getDaemonPath() string {
    _, thisFile, _, ok := runtime.Caller(0)
    if !ok {
        panic("failed to get current file path")
    }
    testDir := filepath.Dir(thisFile)
    // Navigate from test/e2e-go/features/weightoracle to node/weightoracle/testdaemon
    return filepath.Join(testDir, "..", "..", "..", "..", "node", "weightoracle", "testdaemon", "daemon.py")
}

// startAllDaemons starts all weight daemons with appropriate weights
func startAllDaemons(t *testing.T, basePort int, genesisHash string) []*weightDaemon {
    daemons := make([]*weightDaemon, numNodes)
    for i := 0; i < numNodes; i++ {
        weight := normalWeight
        if i == weightedNodeIdx {
            weight = weightedWeight
        }
        daemons[i] = startDaemon(t, basePort+i, weight, totalWeight, genesisHash)
    }
    return daemons
}

// startDaemon launches a Python weight daemon process
func startDaemon(t *testing.T, port, weight, total int, genesisHash string) *weightDaemon {
    daemonPath := getDaemonPath()

    cmd := exec.Command("python3", daemonPath,
        "--port", fmt.Sprintf("%d", port),
        "--default-weight", fmt.Sprintf("%d", weight),
        "--total-weight", fmt.Sprintf("%d", total),
        "--genesis-hash", genesisHash,
    )
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr

    err := cmd.Start()
    require.NoError(t, err, "failed to start daemon on port %d", port)

    return &weightDaemon{
        cmd:    cmd,
        port:   port,
        weight: weight,
    }
}

// waitForDaemonReady pings daemon until it responds or timeout
func waitForDaemonReady(t *testing.T, d *weightDaemon) {
    deadline := time.Now().Add(daemonStartupTimeout)
    for attempt := 0; attempt < daemonPingRetries; attempt++ {
        if time.Now().After(deadline) {
            break
        }
        if pingDaemon(d.port) {
            d.ready = true
            return
        }
        time.Sleep(daemonPingInterval)
    }
    t.Fatalf("daemon on port %d failed to respond within %v", d.port, daemonStartupTimeout)
}

// pingDaemon sends a ping request to the daemon
func pingDaemon(port int) bool {
    conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
    if err != nil {
        return false
    }
    defer conn.Close()

    _, err = conn.Write([]byte(`{"type":"ping"}`))
    if err != nil {
        return false
    }

    buf := make([]byte, 256)
    conn.SetReadDeadline(time.Now().Add(time.Second))
    n, err := conn.Read(buf)
    if err != nil {
        return false
    }
    return n > 0 && string(buf[:n]) == `{"pong":true}`+"\n"
}

// stopDaemon terminates the daemon process
func stopDaemon(d *weightDaemon) {
    if d == nil || d.cmd == nil || d.cmd.Process == nil {
        return
    }
    d.cmd.Process.Kill()
    d.cmd.Wait()
}

// countProposers counts block proposals by each node in a round range
func countProposers(client libgoal.Client, from, to basics.Round, addressToNode map[string]string) (map[string]int, error) {
    counts := make(map[string]int)
    for round := from; round <= to; round++ {
        block, err := client.BookkeepingBlock(round)
        if err != nil {
            return nil, err
        }
        proposerAddr := block.Proposer().String()
        if nodeName, ok := addressToNode[proposerAddr]; ok {
            counts[nodeName]++
        }
    }
    return counts, nil
}

// computeRatio calculates weighted node proposals / average normal node proposals
func computeRatio(counts map[string]int, weightedNode string, normalNodes []string) float64 {
    weightedCount := float64(counts[weightedNode])
    var normalSum float64
    for _, node := range normalNodes {
        normalSum += float64(counts[node])
    }
    normalAvg := normalSum / float64(len(normalNodes))
    if normalAvg == 0 {
        return 0
    }
    return weightedCount / normalAvg
}
```

## 4. Data Model / API / Interface Changes

### 4.1 Wire Protocol (Unchanged)

The weight daemon wire protocol remains unchanged. The new `--default-weight` parameter only affects internal behavior.

### 4.2 Test Data Structures

```go
// checkpointStats captures data at each measurement point
type checkpointStats struct {
    elapsed    time.Duration
    round      basics.Round
    counts     map[string]int  // node name → blocks proposed
    ratio      float64         // weighted/average(normal)
}

// nodeInfo maps node identities
type nodeInfo struct {
    name     string
    address  basics.Address
    weight   int
    isWeighted bool
}
```

## 5. Delivery Phases

### Phase 1: Daemon Enhancement (Prerequisite)
**Goal**: Add `--default-weight` parameter to `daemon.py`

Tasks:
1. Add `--default-weight` CLI argument (type=int, matching existing `--total-weight`)
2. Add `default_weight` parameter to `WeightDaemon.__init__()`
3. Modify `_handle_weight()` to return default when set, before table lookup
4. Test with netcat: `echo '{"type":"weight","address":"X","selection_id":"Y","balance_round":"1"}' | nc localhost PORT`

Verification:
- Manual test: daemon returns configured default weight for any address
- Existing daemon behavior unchanged when `--default-weight` not specified

### Phase 2: Network Template
**Goal**: Create `FiveNodesWeighted.json` network template

Tasks:
1. Create template with 5 equal-stake (20% each) wallets
2. Add 1 relay + 5 participating nodes (no ConfigJSONOverride - ports injected dynamically)
3. Set `LastPartKeyRound: 50000` (supports ~5.5 hours at 400ms/block)

Verification:
- Template passes `NetworkTemplate.Validate()`
- Network starts successfully with template and port override (manual test)

### Phase 3: E2E Test Implementation
**Goal**: Implement `TestWeightedConsensus`

Tasks:
1. Implement port allocation with conflict avoidance (PID-based offset + availability check)
2. Implement `createPortOverride()` TemplateOverride function to inject dynamic ports
3. Implement daemon lifecycle management:
   - Use `runtime.Caller` to reliably find daemon.py path
   - Start with timeout handling
   - Health check with retry loop (20 retries, 500ms interval, 10s total timeout)
   - Clean shutdown via `t.Cleanup()`
4. Implement genesis hash extraction from network directory (using `genesis.Hash()` method)
5. Implement test setup sequence: allocate ports → `SetupNoStart(override)` → get genesis → start daemons → `Start()`
6. Implement address-to-node mapping via `fixture.GetLibGoalClientForNamedNode()` and `fixture.GetNodeWalletsSortedByBalance()`
7. Implement block proposer counting via `client.BookkeepingBlock(round).Proposer()`
8. Implement checkpoint loop with statistics collection
9. Implement summary table output
10. Add `-short` flag support for 5-minute variant

Verification:
- Test completes successfully with `-short` flag
- Ratio trends toward 1.5 over time
- All daemons shut down cleanly on test exit (including on failure)

### Phase 4: Validation & Documentation
**Goal**: Validate test behavior and update documentation

Tasks:
1. Run full 60-minute test
2. Collect and analyze checkpoint data
3. Update README.md with actual results
4. Add example output to documentation

Verification:
- Final ratio within 1.35-1.65 range (10% tolerance)
- Documentation accurately reflects test behavior

## 6. Verification Approach

### 6.1 Unit Testing

| Test | File | Purpose |
|------|------|---------|
| Daemon default weight | `daemon.py` (manual netcat) | Verify `--default-weight` overrides lookup |
| Network template validation | Existing framework | Template passes `Validate()` |

### 6.2 Integration Testing

| Test | Command | Expected Result |
|------|---------|-----------------|
| Short test | `go test ./test/e2e-go/features/weightoracle/... -run TestWeightedConsensus -short -v` | Completes in ~5 min, ratio > 1.0 |
| Full test | `go test ./test/e2e-go/features/weightoracle/... -run TestWeightedConsensus -v -timeout 70m` | Completes in 60 min, ratio ~1.5 |

### 6.3 Verification Commands

```bash
# Build before testing
make build

# Run short test
go test -v ./test/e2e-go/features/weightoracle/... -run TestWeightedConsensus -short

# Run full test (requires 70+ minutes timeout)
go test -v ./test/e2e-go/features/weightoracle/... -run TestWeightedConsensus -timeout 70m

# Check for race conditions (short test)
go test -v ./test/e2e-go/features/weightoracle/... -run TestWeightedConsensus -short -race
```

### 6.4 Success Criteria

1. **Daemon starts and responds**: Each daemon responds to ping within 10 seconds (20 retries × 500ms)
2. **Network initializes**: All 6 nodes (1 relay + 5 participants) start successfully
3. **Blocks produced**: Network produces blocks at expected rate (~150/minute at 400ms/block)
4. **Weight effect visible**: Weighted node proposes more blocks than average
5. **Ratio convergence**: Final ratio within 10% of 1.5 (1.35-1.65)
6. **Clean shutdown**: All processes terminate without errors (verified via `t.Cleanup()`)

## 7. Risk Mitigations

| Risk | Mitigation |
|------|------------|
| Port conflicts in CI | PID-based port offset + availability check + retry with different range + TemplateOverride injection |
| Daemon startup race | Ping with retry loop (20×500ms = 10s timeout) before starting nodes |
| Genesis hash timing | Use `SetupNoStart()` → read genesis → start daemons → `Start()` sequence |
| Template/daemon port mismatch | Use `TemplateOverride` to inject same ports used by daemons |
| Statistical variance | 60-minute duration, 10% tolerance, informational output (not hard fail) |
| Test timeout | Explicit `-timeout 70m` flag, `-short` variant for CI |
| Process cleanup on failure | `t.Cleanup()` registration for robust daemon shutdown |
| Daemon path resolution | Use `runtime.Caller(0)` to find test file location and derive absolute path |

## 8. Implementation Order

1. **Modify `daemon.py`** - Add `--default-weight` (~15 lines)
2. **Create `FiveNodesWeighted.json`** - Network template without ports (~30 lines)
3. **Create `weighted_consensus_test.go`** - E2E test with TemplateOverride (~500 lines)
4. **Update `README.md`** - Documentation with examples
5. **Run tests and validate** - Short and full duration

Total estimated new code: ~550 lines Go + ~15 lines Python
