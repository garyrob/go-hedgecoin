// Copyright (C) 2019-2026 Algorand, Inc.
// This file is part of go-algorand
//
// go-algorand is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.
//
// go-algorand is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with go-algorand.  If not, see <https://www.gnu.org/licenses/>.

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
	"github.com/algorand/go-algorand/test/framework/fixtures"
	"github.com/algorand/go-algorand/test/partitiontest"
)

// Test configuration constants
const (
	numParticipatingNodes = 5
	numTotalNodes         = 6 // 5 participating + 1 relay
	normalWeight          = 1000000
	weightedWeight        = 1500000
	totalWeight           = 5500000 // 4*1M + 1*1.5M

	// Daemon startup configuration
	daemonStartupTimeout = 10 * time.Second
	daemonPingRetries    = 20
	daemonPingInterval   = 500 * time.Millisecond

	// Environment variable to override test duration
	testDurationEnvVar = "WEIGHT_TEST_DURATION"
)

// Node names (must match template) - participating nodes only
var nodeNames = []string{"Node1", "Node2", "Node3", "Node4", "Node5"}

// All node names including relay
var allNodeNames = []string{"Node1", "Node2", "Node3", "Node4", "Node5", "Relay"}

// Normal nodes (all except weighted node) for ratio computation
var normalNodes = []string{"Node1", "Node2", "Node3", "Node4"}

// Weighted node name
const weightedNodeName = "Node5"

// Checkpoint intervals for full test (60 minutes)
var fullCheckpoints = []time.Duration{
	5 * time.Minute,
	10 * time.Minute,
	20 * time.Minute,
	30 * time.Minute,
	40 * time.Minute,
	50 * time.Minute,
	60 * time.Minute,
}

// Checkpoint intervals for short test (5 minutes)
var shortCheckpoints = []time.Duration{
	5 * time.Minute,
}

// getTestDuration returns the test duration based on:
// 1. WEIGHT_TEST_DURATION environment variable (if set)
// 2. testing.Short() flag (5 minutes)
// 3. Default full test (60 minutes)
func getTestDuration(t *testing.T) time.Duration {
	if envDuration := os.Getenv(testDurationEnvVar); envDuration != "" {
		duration, err := time.ParseDuration(envDuration)
		if err != nil {
			t.Logf("Warning: invalid %s value %q, using default", testDurationEnvVar, envDuration)
		} else if duration > 0 {
			t.Logf("Using custom test duration from %s: %v", testDurationEnvVar, duration)
			return duration
		}
	}
	if testing.Short() {
		return 5 * time.Minute
	}
	return 60 * time.Minute
}

// getCheckpoints returns checkpoint intervals for the given duration.
// For durations <= 5 minutes, returns a single checkpoint at the end.
// For longer durations, returns checkpoints at 5, 10, 20, 30, ... minutes,
// plus a final checkpoint at the total duration.
func getCheckpoints(totalDuration time.Duration) []time.Duration {
	if totalDuration <= 5*time.Minute {
		return []time.Duration{totalDuration}
	}

	var checkpoints []time.Duration
	checkpoints = append(checkpoints, 5*time.Minute)

	// Add 10 minute mark if duration allows
	if totalDuration >= 10*time.Minute {
		checkpoints = append(checkpoints, 10*time.Minute)
	}

	// Add 10-minute intervals after that
	for t := 20 * time.Minute; t <= totalDuration; t += 10 * time.Minute {
		checkpoints = append(checkpoints, t)
	}

	// Ensure final checkpoint is at totalDuration
	if checkpoints[len(checkpoints)-1] != totalDuration {
		checkpoints = append(checkpoints, totalDuration)
	}

	return checkpoints
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
	elapsed   time.Duration
	round     basics.Round
	counts    map[string]int // node name -> blocks proposed
	normalAvg float64        // average blocks proposed by normal nodes
	ratio     float64        // weighted/average(normal)
}

// TestWeightedConsensus tests that external weight affects block proposal rates.
// A node with 1.5x weight should propose ~1.5x more blocks than average.
func TestWeightedConsensus(t *testing.T) {
	partitiontest.PartitionTest(t)
	defer fixtures.ShutdownSynchronizedTest(t)
	t.Parallel()

	a := require.New(fixtures.SynchronizedTest(t))

	var fixture fixtures.RestClientFixture

	// Step 1: Allocate ports BEFORE setting up network (for all nodes including relay)
	// NOTE: The current algod build requires ExternalWeightOraclePort to be configured
	// AND reachable at startup for all nodes.
	basePort := allocateBasePorts(t, numTotalNodes)
	t.Logf("Allocated base port: %d", basePort)

	// Step 2: Create TemplateOverride to inject dynamic ports
	portOverride := createPortOverride(basePort)

	// Step 3: SetupNoStart with port override creates network directories and genesis
	// The template uses ConsensusFuture which has external weight oracle support
	fixture.SetupNoStart(t, filepath.Join("nettemplates", "FiveNodesWeighted.json"), portOverride)

	// Step 4: Get genesis hash from created network
	genesisHash := getGenesisHashFromNetwork(t, &fixture)
	t.Logf("Genesis hash: %s", genesisHash)

	// Step 5: Extract wallet addresses from genesis and create weight table
	// CRITICAL: All nodes must see the same weight for every address.
	// This is required because credential verification uses the receiver's view of stake.
	addressWeightsFile := createAddressWeightsFile(t, &fixture)
	t.Logf("Created address weights file: %s", addressWeightsFile)

	// Step 6: Start weight daemons with allocated ports and shared weight table
	daemons := startAllDaemonsWithWeights(t, basePort, genesisHash, addressWeightsFile)

	// Register cleanup with t.Cleanup for robust shutdown
	t.Cleanup(func() {
		for _, d := range daemons {
			stopDaemon(d)
		}
	})

	// Step 7: Verify all daemons are healthy
	for i, d := range daemons {
		waitForDaemonReady(t, d)
		t.Logf("Daemon %d (port %d) is ready", i+1, d.port)
	}

	// Step 8: Start the network (nodes will connect to daemons)
	fixture.Start()
	defer fixture.Shutdown()

	// Step 9: Build address-to-node mapping
	addressToNode := buildAddressMapping(t, &fixture, a)
	t.Logf("Address to node mapping complete. Found %d nodes.", len(addressToNode))

	// Get client for querying blocks
	client := fixture.LibGoalClient

	// Get initial status to record start round
	status, err := client.Status()
	a.NoError(err)
	startRound := basics.Round(status.LastRound)
	t.Logf("Starting at round %d", startRound)

	// Select checkpoints based on test duration
	testDuration := getTestDuration(t)
	checkpoints := getCheckpoints(testDuration)

	// Step 10: Run checkpoint loop collecting statistics
	startTime := time.Now()
	var allStats []*checkpointStats

	t.Logf("")
	t.Logf("Test started. Will collect checkpoints at: %v", checkpoints)
	t.Logf("Expected test duration: %v", checkpoints[len(checkpoints)-1])
	t.Logf("")

	for _, checkpoint := range checkpoints {
		// Wait until checkpoint time
		elapsed := time.Since(startTime)
		if elapsed < checkpoint {
			sleepDuration := checkpoint - elapsed
			t.Logf("Waiting %v until %v checkpoint...", sleepDuration.Round(time.Second), checkpoint)
			time.Sleep(sleepDuration)
		}

		// Get current round
		status, err = client.Status()
		a.NoError(err)
		currentRound := basics.Round(status.LastRound)

		// Count proposals from start to current round
		counts, err := countProposers(client, startRound+1, currentRound, addressToNode)
		a.NoError(err)

		// Compute normal average and ratio
		var normalSum float64
		for _, node := range normalNodes {
			normalSum += float64(counts[node])
		}
		normalAvg := normalSum / float64(len(normalNodes))
		var ratio float64
		if normalAvg > 0 {
			ratio = float64(counts[weightedNodeName]) / normalAvg
		}

		// Record checkpoint stats
		stats := &checkpointStats{
			elapsed:   time.Since(startTime),
			round:     currentRound,
			counts:    counts,
			normalAvg: normalAvg,
			ratio:     ratio,
		}
		allStats = append(allStats, stats)

		// Print checkpoint
		printCheckpoint(t, stats)
	}

	// Print final summary
	printSummary(t, allStats)

	// Log test completion
	t.Logf("Test completed successfully after %v", time.Since(startTime).Round(time.Second))
}

// createPortOverride returns a TemplateOverride that injects ExternalWeightOraclePort
func createPortOverride(basePort int) netdeploy.TemplateOverride {
	return func(template *netdeploy.NetworkTemplate) {
		for i := range template.Nodes {
			node := &template.Nodes[i]

			// Find node index from allNodeNames
			nodeIdx := -1
			for j, name := range allNodeNames {
				if node.Name == name {
					nodeIdx = j
					break
				}
			}
			if nodeIdx < 0 {
				continue
			}

			port := basePort + nodeIdx
			override := map[string]interface{}{
				"ExternalWeightOraclePort": port,
			}

			// Merge with existing override if present
			if node.ConfigJSONOverride != "" {
				var existing map[string]interface{}
				if err := json.Unmarshal([]byte(node.ConfigJSONOverride), &existing); err == nil {
					for k, v := range existing {
						override[k] = v
					}
				}
			}
			overrideBytes, _ := json.Marshal(override)
			node.ConfigJSONOverride = string(overrideBytes)
		}
	}
}

// getGenesisHashFromNetwork reads genesis.json from the network directory
func getGenesisHashFromNetwork(t *testing.T, fixture *fixtures.RestClientFixture) string {
	genesisPath := filepath.Join(fixture.PrimaryDataDir(), "..", "genesis.json")
	genesis, err := bookkeeping.LoadGenesisFromFile(genesisPath)
	require.NoError(t, err, "failed to load genesis")
	hash := genesis.Hash()
	return base64.StdEncoding.EncodeToString(hash[:])
}

// createAddressWeightsFile extracts wallet addresses from genesis and creates a JSON file
// mapping each address to its weight. This ensures all daemons return the same weight
// for every address, which is CRITICAL for credential verification in consensus.
func createAddressWeightsFile(t *testing.T, fixture *fixtures.RestClientFixture) string {
	genesisPath := filepath.Join(fixture.PrimaryDataDir(), "..", "genesis.json")
	genesis, err := bookkeeping.LoadGenesisFromFile(genesisPath)
	require.NoError(t, err, "failed to load genesis")

	// Build address -> weight mapping
	// Wallet5 (Node5) gets 1.5x weight, others get 1x
	addressWeights := make(map[string]int)
	for _, alloc := range genesis.Allocation {
		// Skip non-wallet accounts (RewardsPool, FeeSink, etc.)
		if alloc.Comment == "" || !isWalletComment(alloc.Comment) {
			continue
		}

		weight := normalWeight
		if alloc.Comment == "Wallet5" {
			weight = weightedWeight
		}
		addressWeights[alloc.Address] = weight
		t.Logf("Address weight: %s (%s) = %d", alloc.Address, alloc.Comment, weight)
	}

	// Write to a JSON file in the network directory
	weightsPath := filepath.Join(fixture.PrimaryDataDir(), "..", "address_weights.json")
	weightsJSON, err := json.Marshal(addressWeights)
	require.NoError(t, err, "failed to marshal address weights")
	err = os.WriteFile(weightsPath, weightsJSON, 0644)
	require.NoError(t, err, "failed to write address weights file")

	return weightsPath
}

// isWalletComment checks if a genesis allocation comment is for a wallet
func isWalletComment(comment string) bool {
	return len(comment) >= 6 && comment[:6] == "Wallet"
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
		basePort += 100
	}
	t.Fatalf("failed to find %d available ports", count)
	return 0
}

// isPortAvailable checks if a TCP port is available for binding
func isPortAvailable(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// getDaemonPath returns the absolute path to daemon.py
func getDaemonPath() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("failed to get current file path")
	}
	testDir := filepath.Dir(thisFile)
	return filepath.Join(testDir, "..", "..", "..", "..", "node", "weightoracle", "testdaemon", "daemon.py")
}

// startAllDaemonsWithWeights starts weight daemons for all nodes with a shared address weights file.
// CRITICAL: All nodes must see the same weight for every address for consensus to work.
func startAllDaemonsWithWeights(t *testing.T, basePort int, genesisHash string, addressWeightsFile string) []*weightDaemon {
	daemons := make([]*weightDaemon, numTotalNodes)
	for i := 0; i < numTotalNodes; i++ {
		daemons[i] = startDaemonWithWeightsFile(t, basePort+i, totalWeight, genesisHash, addressWeightsFile)
	}
	return daemons
}

// startDaemonWithWeightsFile launches a Python weight daemon with a shared address weights file
func startDaemonWithWeightsFile(t *testing.T, port, total int, genesisHash, addressWeightsFile string) *weightDaemon {
	daemonPath := getDaemonPath()

	cmd := exec.Command("python3", daemonPath,
		"--port", fmt.Sprintf("%d", port),
		"--total-weight", fmt.Sprintf("%d", total),
		"--genesis-hash", genesisHash,
		"--address-weights-file", addressWeightsFile,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Start()
	require.NoError(t, err, "failed to start daemon on port %d", port)

	return &weightDaemon{
		cmd:  cmd,
		port: port,
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

	var response struct {
		Pong bool `json:"pong"`
	}
	if err := json.Unmarshal(buf[:n], &response); err != nil {
		return false
	}
	return response.Pong
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

// printCheckpoint outputs formatted checkpoint data
func printCheckpoint(t *testing.T, stats *checkpointStats) {
	t.Logf("=== Checkpoint at %v (Round %d) ===", stats.elapsed.Round(time.Second), stats.round)
	t.Logf("Block proposals per node:")
	for _, name := range nodeNames {
		marker := ""
		if name == weightedNodeName {
			marker = " (weighted)"
		}
		t.Logf("  %s: %d%s", name, stats.counts[name], marker)
	}
	t.Logf("Ratio (weighted/avg normal): %.3f (expected: 1.5)", stats.ratio)
	t.Logf("")
}

// printSummary outputs the final summary table
func printSummary(t *testing.T, allStats []*checkpointStats) {
	t.Logf("")
	t.Logf("===== FINAL SUMMARY =====")
	t.Logf("")
	t.Logf("%-10s | %-8s | %-8s | %-12s | %-8s", "Elapsed", "Round", "Weighted", "Normal Avg", "Ratio")
	t.Logf("%-10s | %-8s | %-8s | %-12s | %-8s", "----------", "--------", "--------", "------------", "--------")

	for _, stats := range allStats {
		t.Logf("%-10v | %-8d | %-8d | %-12.1f | %-8.3f",
			stats.elapsed.Round(time.Second),
			stats.round,
			stats.counts[weightedNodeName],
			stats.normalAvg,
			stats.ratio)
	}

	t.Logf("")
	if len(allStats) > 0 {
		finalStats := allStats[len(allStats)-1]
		t.Logf("Final ratio: %.3f (expected: 1.5, tolerance: 1.35-1.65)", finalStats.ratio)
		if finalStats.ratio >= 1.35 && finalStats.ratio <= 1.65 {
			t.Logf("SUCCESS: Ratio is within expected range")
		} else {
			t.Logf("WARNING: Ratio is outside expected range (informational, not a test failure)")
		}
	}
	t.Logf("")
}
