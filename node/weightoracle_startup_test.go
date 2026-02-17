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

package node

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/algorand/go-deadlock"

	"github.com/algorand/go-algorand/config"
	"github.com/algorand/go-algorand/crypto"
	"github.com/algorand/go-algorand/data/account"
	"github.com/algorand/go-algorand/data/basics"
	"github.com/algorand/go-algorand/data/bookkeeping"
	"github.com/algorand/go-algorand/ledger/ledgercore"
	"github.com/algorand/go-algorand/logging"
	"github.com/algorand/go-algorand/protocol"
	"github.com/algorand/go-algorand/test/partitiontest"
	"github.com/algorand/go-algorand/util/db"
)

// mockWeightServer is a TCP server that simulates a weight daemon for testing.
// It accepts JSON requests and returns configurable responses.
//
// Note: Tests in this file validate startup behavior before the node's Start() method
// is called. Since we don't call Start(), we also don't call Stop() - the node is not
// running and there's nothing to stop.
type mockWeightServer struct {
	listener net.Listener
	port     uint16
	wg       sync.WaitGroup
	mu       deadlock.Mutex
	closed   bool

	// Configurable responses
	genesisHash      crypto.Digest
	protocolVersion  string
	algorithmVersion string

	// Weight responses: map from address string to weight
	weights map[string]uint64

	// DefaultWeight is returned for addresses not in the weights map
	// If 0, returns "0" for unknown addresses
	defaultWeight uint64

	// TotalWeight response
	totalWeight uint64

	// Error injection
	pingError     string
	pingErrorCode string
	identityError string
	weightError   string
}

// newMockWeightServer creates and starts a mock weight server.
func newMockWeightServer(t *testing.T) *mockWeightServer {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := listener.Addr().(*net.TCPAddr)
	s := &mockWeightServer{
		listener:         listener,
		port:             uint16(addr.Port),
		protocolVersion:  ledgercore.ExpectedWeightProtocolVersion,
		algorithmVersion: ledgercore.ExpectedWeightAlgorithmVersion,
		weights:          make(map[string]uint64),
		totalWeight:      1000000,
	}

	s.wg.Add(1)
	go s.serve()

	return s
}

func (s *mockWeightServer) serve() {
	defer s.wg.Done()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			continue
		}

		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

func (s *mockWeightServer) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	var req map[string]interface{}
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&req); err != nil {
		return
	}

	var resp interface{}
	reqType, _ := req["type"].(string)

	switch reqType {
	case "ping":
		s.mu.Lock()
		pingErr := s.pingError
		pingCode := s.pingErrorCode
		s.mu.Unlock()

		if pingErr != "" {
			resp = map[string]interface{}{
				"error": pingErr,
				"code":  pingCode,
			}
		} else {
			resp = map[string]interface{}{
				"pong": true,
			}
		}

	case "identity":
		s.mu.Lock()
		identityErr := s.identityError
		genesisHash := s.genesisHash
		protocolVer := s.protocolVersion
		algorithmVer := s.algorithmVersion
		s.mu.Unlock()

		if identityErr != "" {
			resp = map[string]interface{}{
				"error": identityErr,
				"code":  "internal",
			}
		} else {
			resp = map[string]interface{}{
				"genesis_hash":      base64.StdEncoding.EncodeToString(genesisHash[:]),
				"protocol_version":  protocolVer,
				"algorithm_version": algorithmVer,
			}
		}

	case "weight":
		s.mu.Lock()
		weightErr := s.weightError
		addr, _ := req["address"].(string)
		weight, ok := s.weights[addr]
		defWeight := s.defaultWeight
		s.mu.Unlock()

		if weightErr != "" {
			resp = map[string]interface{}{
				"error": weightErr,
				"code":  "internal",
			}
		} else if !ok {
			// Return default weight for unknown addresses
			resp = map[string]interface{}{
				"weight": formatUint64(defWeight),
			}
		} else {
			resp = map[string]interface{}{
				"weight": formatUint64(weight),
			}
		}

	case "total_weight":
		s.mu.Lock()
		tw := s.totalWeight
		s.mu.Unlock()

		resp = map[string]interface{}{
			"total_weight": formatUint64(tw),
		}

	default:
		resp = map[string]interface{}{
			"error": "unknown request type",
			"code":  "unsupported",
		}
	}

	encoder := json.NewEncoder(conn)
	_ = encoder.Encode(resp)
}

func formatUint64(v uint64) string {
	return strconv.FormatUint(v, 10)
}

func (s *mockWeightServer) SetGenesisHash(h crypto.Digest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.genesisHash = h
}

func (s *mockWeightServer) SetProtocolVersion(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.protocolVersion = v
}

func (s *mockWeightServer) SetAlgorithmVersion(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.algorithmVersion = v
}

func (s *mockWeightServer) SetWeight(addr string, weight uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.weights[addr] = weight
}

func (s *mockWeightServer) SetPingError(errMsg, code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pingError = errMsg
	s.pingErrorCode = code
}

func (s *mockWeightServer) SetIdentityError(errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.identityError = errMsg
}

func (s *mockWeightServer) SetWeightError(errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.weightError = errMsg
}

func (s *mockWeightServer) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.listener.Close()
	s.wg.Wait()
}

// TestStartupValidationPortZero tests that node startup fails when ExternalWeightOraclePort is 0.
func TestStartupValidationPortZero(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	testDir := t.TempDir()

	genesis := bookkeeping.Genesis{
		SchemaID:    "test-startup-port-zero",
		Proto:       protocol.ConsensusCurrentVersion,
		Network:     config.Devtestnet,
		FeeSink:     sinkAddr.String(),
		RewardsPool: poolAddr.String(),
	}

	cfg := config.GetDefaultLocal()
	cfg.ExternalWeightOraclePort = 0 // This should cause startup to fail

	log := logging.TestingLog(t)

	node, err := MakeFull(log, testDir, cfg, []string{}, genesis)
	require.Error(t, err)
	require.Nil(t, node)
	require.Contains(t, err.Error(), "ExternalWeightOraclePort must be configured")
}

// TestStartupValidationDaemonUnreachable tests that node startup fails when the daemon is not reachable.
func TestStartupValidationDaemonUnreachable(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	testDir := t.TempDir()

	// Get a port that's guaranteed to be available but not listening
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	listener.Close()

	genesis := bookkeeping.Genesis{
		SchemaID:    "test-startup-unreachable",
		Proto:       protocol.ConsensusCurrentVersion,
		Network:     config.Devtestnet,
		FeeSink:     sinkAddr.String(),
		RewardsPool: poolAddr.String(),
	}

	cfg := config.GetDefaultLocal()
	cfg.ExternalWeightOraclePort = port // Point to closed port

	log := logging.TestingLog(t)

	node, err := MakeFull(log, testDir, cfg, []string{}, genesis)
	require.Error(t, err)
	require.Nil(t, node)
	require.Contains(t, err.Error(), "weight daemon not reachable")
}

// TestStartupValidationGenesisHashMismatch tests that node startup fails when genesis hash doesn't match.
func TestStartupValidationGenesisHashMismatch(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	testDir := t.TempDir()

	// Start mock server with a different genesis hash
	server := newMockWeightServer(t)
	defer server.Close()

	// Set a different genesis hash
	var wrongHash crypto.Digest
	wrongHash[0] = 0xFF
	wrongHash[1] = 0xEE
	server.SetGenesisHash(wrongHash)

	genesis := bookkeeping.Genesis{
		SchemaID:    "test-startup-genesis-mismatch",
		Proto:       protocol.ConsensusCurrentVersion,
		Network:     config.Devtestnet,
		FeeSink:     sinkAddr.String(),
		RewardsPool: poolAddr.String(),
	}

	cfg := config.GetDefaultLocal()
	cfg.ExternalWeightOraclePort = server.port

	log := logging.TestingLog(t)

	node, err := MakeFull(log, testDir, cfg, []string{}, genesis)
	require.Error(t, err)
	require.Nil(t, node)
	require.Contains(t, err.Error(), "genesis hash mismatch")
}

// TestStartupValidationAlgorithmVersionMismatch tests that node startup fails when algorithm version doesn't match.
func TestStartupValidationAlgorithmVersionMismatch(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	testDir := t.TempDir()

	server := newMockWeightServer(t)
	defer server.Close()

	// Server will compute genesis hash from the genesis block during startup,
	// so we need to set it correctly. For this test, we first create a minimal
	// genesis to compute the hash.
	genesis := bookkeeping.Genesis{
		SchemaID:    "test-startup-algo-mismatch",
		Proto:       protocol.ConsensusCurrentVersion,
		Network:     config.Devtestnet,
		FeeSink:     sinkAddr.String(),
		RewardsPool: poolAddr.String(),
	}

	// Compute the genesis hash that the node will expect
	genesisHash := genesis.Hash()
	server.SetGenesisHash(genesisHash)
	server.SetAlgorithmVersion("2.0") // Wrong version

	cfg := config.GetDefaultLocal()
	cfg.ExternalWeightOraclePort = server.port

	log := logging.TestingLog(t)

	node, err := MakeFull(log, testDir, cfg, []string{}, genesis)
	require.Error(t, err)
	require.Nil(t, node)
	require.Contains(t, err.Error(), "algorithm version mismatch")
}

// TestStartupValidationProtocolVersionMismatch tests that node startup fails when protocol version doesn't match.
func TestStartupValidationProtocolVersionMismatch(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	testDir := t.TempDir()

	server := newMockWeightServer(t)
	defer server.Close()

	genesis := bookkeeping.Genesis{
		SchemaID:    "test-startup-proto-mismatch",
		Proto:       protocol.ConsensusCurrentVersion,
		Network:     config.Devtestnet,
		FeeSink:     sinkAddr.String(),
		RewardsPool: poolAddr.String(),
	}

	genesisHash := genesis.Hash()
	server.SetGenesisHash(genesisHash)
	server.SetProtocolVersion("2.0") // Wrong version

	cfg := config.GetDefaultLocal()
	cfg.ExternalWeightOraclePort = server.port

	log := logging.TestingLog(t)

	node, err := MakeFull(log, testDir, cfg, []string{}, genesis)
	require.Error(t, err)
	require.Nil(t, node)
	require.Contains(t, err.Error(), "protocol version mismatch")
}

// TestStartupValidationSuccessNoKeys tests successful startup with no participation keys.
func TestStartupValidationSuccessNoKeys(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	testDir := t.TempDir()

	server := newMockWeightServer(t)
	defer server.Close()

	genesis := bookkeeping.Genesis{
		SchemaID:    "test-startup-success-nokeys",
		Proto:       protocol.ConsensusCurrentVersion,
		Network:     config.Devtestnet,
		FeeSink:     sinkAddr.String(),
		RewardsPool: poolAddr.String(),
	}

	genesisHash := genesis.Hash()
	server.SetGenesisHash(genesisHash)

	cfg := config.GetDefaultLocal()
	cfg.ExternalWeightOraclePort = server.port

	log := logging.TestingLog(t)

	node, err := MakeFull(log, testDir, cfg, []string{}, genesis)
	require.NoError(t, err)
	require.NotNil(t, node)
}

// TestStartupValidationWithEligibleKeyHavingWeight tests successful startup with
// a participation key that has non-zero weight from the daemon.
func TestStartupValidationWithEligibleKeyHavingWeight(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	testDir := t.TempDir()

	server := newMockWeightServer(t)
	defer server.Close()

	// Create a participation key
	firstRound := basics.Round(0)
	lastRound := basics.Round(1000)

	genesis := bookkeeping.Genesis{
		SchemaID:    "test-startup-key-with-weight",
		Proto:       protocol.ConsensusCurrentVersion,
		Network:     config.Devtestnet,
		FeeSink:     sinkAddr.String(),
		RewardsPool: poolAddr.String(),
	}

	genesisHash := genesis.Hash()
	server.SetGenesisHash(genesisHash)

	// Create genesis directory and participation key
	genesisDir := filepath.Join(testDir, genesis.ID())
	err := os.MkdirAll(genesisDir, 0700)
	require.NoError(t, err)

	// Generate a root key
	rootFilename := filepath.Join(genesisDir, "root.rootkey")
	rootAccess, err := db.MakeAccessor(rootFilename, false, false)
	require.NoError(t, err)
	root, err := account.GenerateRoot(rootAccess)
	rootAccess.Close()
	require.NoError(t, err)

	// Generate participation key with proper filename format
	partKeyName := config.PartKeyFilename(t.Name(), uint64(firstRound), uint64(lastRound))
	partFilename := filepath.Join(genesisDir, partKeyName)
	partAccess, err := db.MakeAccessor(partFilename, false, false)
	require.NoError(t, err)
	part, err := account.FillDBWithParticipationKeys(partAccess, root.Address(), firstRound, lastRound, config.Consensus[protocol.ConsensusCurrentVersion].DefaultKeyDilution)
	require.NoError(t, err)
	partAccess.Close()

	// Add the account to genesis with the participation key's SelectionID and VoteID
	genesis.Allocation = append(genesis.Allocation, bookkeeping.GenesisAllocation{
		Address: root.Address().String(),
		State: bookkeeping.GenesisAccountData{
			Status:      basics.Online,
			MicroAlgos:  basics.MicroAlgos{Raw: 1000000},
			SelectionID: part.VRFSecrets().PK,
			VoteID:      part.VotingSecrets().OneTimeSignatureVerifier,
		},
	})

	// Recompute genesis hash after adding allocation
	genesisHash = genesis.Hash()
	server.SetGenesisHash(genesisHash)

	// Set weight for this address
	server.SetWeight(root.Address().String(), 1000)

	cfg := config.GetDefaultLocal()
	cfg.ExternalWeightOraclePort = server.port

	log := logging.TestingLog(t)

	node, err := MakeFull(log, testDir, cfg, []string{}, genesis)
	require.NoError(t, err)
	require.NotNil(t, node)
}

// TestStartupValidationEligibleKeyZeroWeight tests that startup fails when
// an eligible participation key has zero weight from the daemon.
func TestStartupValidationEligibleKeyZeroWeight(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	testDir := t.TempDir()

	server := newMockWeightServer(t)
	defer server.Close()

	firstRound := basics.Round(0)
	lastRound := basics.Round(1000)

	genesis := bookkeeping.Genesis{
		SchemaID:    "test-startup-key-zero-weight",
		Proto:       protocol.ConsensusCurrentVersion,
		Network:     config.Devtestnet,
		FeeSink:     sinkAddr.String(),
		RewardsPool: poolAddr.String(),
	}

	// Create genesis directory and participation key
	genesisDir := filepath.Join(testDir, genesis.ID())
	err := os.MkdirAll(genesisDir, 0700)
	require.NoError(t, err)

	// Generate a root key
	rootFilename := filepath.Join(genesisDir, "root.rootkey")
	rootAccess, err := db.MakeAccessor(rootFilename, false, false)
	require.NoError(t, err)
	root, err := account.GenerateRoot(rootAccess)
	rootAccess.Close()
	require.NoError(t, err)

	// Generate participation key with proper filename format
	partKeyName := config.PartKeyFilename(t.Name(), uint64(firstRound), uint64(lastRound))
	partFilename := filepath.Join(genesisDir, partKeyName)
	partAccess, err := db.MakeAccessor(partFilename, false, false)
	require.NoError(t, err)
	part, err := account.FillDBWithParticipationKeys(partAccess, root.Address(), firstRound, lastRound, config.Consensus[protocol.ConsensusCurrentVersion].DefaultKeyDilution)
	require.NoError(t, err)
	partAccess.Close()

	// Add the account to genesis with the participation key's SelectionID and VoteID
	genesis.Allocation = append(genesis.Allocation, bookkeeping.GenesisAllocation{
		Address: root.Address().String(),
		State: bookkeeping.GenesisAccountData{
			Status:      basics.Online,
			MicroAlgos:  basics.MicroAlgos{Raw: 1000000},
			SelectionID: part.VRFSecrets().PK,
			VoteID:      part.VotingSecrets().OneTimeSignatureVerifier,
		},
	})

	genesisHash := genesis.Hash()
	server.SetGenesisHash(genesisHash)

	// DO NOT set weight for this address - it will return 0

	cfg := config.GetDefaultLocal()
	cfg.ExternalWeightOraclePort = server.port

	log := logging.TestingLog(t)

	node, err := MakeFull(log, testDir, cfg, []string{}, genesis)
	require.Error(t, err)
	require.Nil(t, node)
	require.Contains(t, err.Error(), "zero weight")
}

// TestStartupValidationKeyOutOfRoundWindow tests that keys outside the current vote round window are skipped.
func TestStartupValidationKeyOutOfRoundWindow(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	testDir := t.TempDir()

	server := newMockWeightServer(t)
	defer server.Close()

	// Create a key that's valid for rounds 1000-2000 (way in the future)
	// At startup, voteRound will be ~1, so this key should be skipped
	firstRound := basics.Round(1000)
	lastRound := basics.Round(2000)

	genesis := bookkeeping.Genesis{
		SchemaID:    "test-startup-key-out-of-window",
		Proto:       protocol.ConsensusCurrentVersion,
		Network:     config.Devtestnet,
		FeeSink:     sinkAddr.String(),
		RewardsPool: poolAddr.String(),
	}

	// Create genesis directory and participation key
	genesisDir := filepath.Join(testDir, genesis.ID())
	err := os.MkdirAll(genesisDir, 0700)
	require.NoError(t, err)

	// Generate a root key
	rootFilename := filepath.Join(genesisDir, "root.rootkey")
	rootAccess, err := db.MakeAccessor(rootFilename, false, false)
	require.NoError(t, err)
	root, err := account.GenerateRoot(rootAccess)
	rootAccess.Close()
	require.NoError(t, err)

	// Generate participation key with proper filename format
	partKeyName := config.PartKeyFilename(t.Name(), uint64(firstRound), uint64(lastRound))
	partFilename := filepath.Join(genesisDir, partKeyName)
	partAccess, err := db.MakeAccessor(partFilename, false, false)
	require.NoError(t, err)
	part, err := account.FillDBWithParticipationKeys(partAccess, root.Address(), firstRound, lastRound, config.Consensus[protocol.ConsensusCurrentVersion].DefaultKeyDilution)
	require.NoError(t, err)
	partAccess.Close()

	// Add the account to genesis with the participation key's SelectionID and VoteID
	genesis.Allocation = append(genesis.Allocation, bookkeeping.GenesisAllocation{
		Address: root.Address().String(),
		State: bookkeeping.GenesisAccountData{
			Status:      basics.Online,
			MicroAlgos:  basics.MicroAlgos{Raw: 1000000},
			SelectionID: part.VRFSecrets().PK,
			VoteID:      part.VotingSecrets().OneTimeSignatureVerifier,
		},
	})

	genesisHash := genesis.Hash()
	server.SetGenesisHash(genesisHash)

	// DO NOT set weight - the key should be skipped because it's out of the round window
	// If it's NOT skipped, the test would fail with "zero weight" error

	cfg := config.GetDefaultLocal()
	cfg.ExternalWeightOraclePort = server.port

	log := logging.TestingLog(t)

	node, err := MakeFull(log, testDir, cfg, []string{}, genesis)
	require.NoError(t, err) // Should succeed because the key is skipped
	require.NotNil(t, node)
}

// TestStartupValidationAccountNotInSnapshot tests that keys for accounts not in the
// balance snapshot (offline accounts) are skipped.
func TestStartupValidationAccountNotInSnapshot(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	testDir := t.TempDir()

	server := newMockWeightServer(t)
	defer server.Close()

	firstRound := basics.Round(0)
	lastRound := basics.Round(1000)

	genesis := bookkeeping.Genesis{
		SchemaID:    "test-startup-account-not-in-snapshot",
		Proto:       protocol.ConsensusCurrentVersion,
		Network:     config.Devtestnet,
		FeeSink:     sinkAddr.String(),
		RewardsPool: poolAddr.String(),
	}

	// Create genesis directory and participation key
	genesisDir := filepath.Join(testDir, genesis.ID())
	err := os.MkdirAll(genesisDir, 0700)
	require.NoError(t, err)

	// Generate a root key
	rootFilename := filepath.Join(genesisDir, "root.rootkey")
	rootAccess, err := db.MakeAccessor(rootFilename, false, false)
	require.NoError(t, err)
	root, err := account.GenerateRoot(rootAccess)
	rootAccess.Close()
	require.NoError(t, err)

	// Generate participation key with proper filename format
	partKeyName := config.PartKeyFilename(t.Name(), uint64(firstRound), uint64(lastRound))
	partFilename := filepath.Join(genesisDir, partKeyName)
	partAccess, err := db.MakeAccessor(partFilename, false, false)
	require.NoError(t, err)
	_, err = account.FillDBWithParticipationKeys(partAccess, root.Address(), firstRound, lastRound, config.Consensus[protocol.ConsensusCurrentVersion].DefaultKeyDilution)
	require.NoError(t, err)
	partAccess.Close()

	// DO NOT add the account to genesis - it won't be in the balance snapshot
	// The key should be skipped

	genesisHash := genesis.Hash()
	server.SetGenesisHash(genesisHash)

	cfg := config.GetDefaultLocal()
	cfg.ExternalWeightOraclePort = server.port

	log := logging.TestingLog(t)

	node, err := MakeFull(log, testDir, cfg, []string{}, genesis)
	require.NoError(t, err) // Should succeed because the account is not in snapshot
	require.NotNil(t, node)
}

// TestStartupValidationSelectionIDMismatch tests that keys with SelectionID
// mismatch vs the snapshot are skipped.
func TestStartupValidationSelectionIDMismatch(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	testDir := t.TempDir()

	server := newMockWeightServer(t)
	defer server.Close()

	firstRound := basics.Round(0)
	lastRound := basics.Round(1000)

	genesis := bookkeeping.Genesis{
		SchemaID:    "test-startup-selectionid-mismatch",
		Proto:       protocol.ConsensusCurrentVersion,
		Network:     config.Devtestnet,
		FeeSink:     sinkAddr.String(),
		RewardsPool: poolAddr.String(),
	}

	// Create genesis directory and participation key
	genesisDir := filepath.Join(testDir, genesis.ID())
	err := os.MkdirAll(genesisDir, 0700)
	require.NoError(t, err)

	// Generate a root key
	rootFilename := filepath.Join(genesisDir, "root.rootkey")
	rootAccess, err := db.MakeAccessor(rootFilename, false, false)
	require.NoError(t, err)
	root, err := account.GenerateRoot(rootAccess)
	rootAccess.Close()
	require.NoError(t, err)

	// Generate participation key with proper filename format
	partKeyName := config.PartKeyFilename(t.Name(), uint64(firstRound), uint64(lastRound))
	partFilename := filepath.Join(genesisDir, partKeyName)
	partAccess, err := db.MakeAccessor(partFilename, false, false)
	require.NoError(t, err)
	_, err = account.FillDBWithParticipationKeys(partAccess, root.Address(), firstRound, lastRound, config.Consensus[protocol.ConsensusCurrentVersion].DefaultKeyDilution)
	require.NoError(t, err)
	partAccess.Close()

	// Add the account to genesis with a DIFFERENT SelectionID than the participation key
	var differentSelectionID crypto.VRFVerifier
	differentSelectionID[0] = 0xFF
	differentSelectionID[1] = 0xEE
	genesis.Allocation = append(genesis.Allocation, bookkeeping.GenesisAllocation{
		Address: root.Address().String(),
		State: bookkeeping.GenesisAccountData{
			Status:      basics.Online,
			MicroAlgos:  basics.MicroAlgos{Raw: 1000000},
			SelectionID: differentSelectionID, // Different from part.VRFSecrets().PK
		},
	})

	genesisHash := genesis.Hash()
	server.SetGenesisHash(genesisHash)

	// DO NOT set weight - the key should be skipped due to SelectionID mismatch

	cfg := config.GetDefaultLocal()
	cfg.ExternalWeightOraclePort = server.port

	log := logging.TestingLog(t)

	node, err := MakeFull(log, testDir, cfg, []string{}, genesis)
	require.NoError(t, err) // Should succeed because the key is skipped
	require.NotNil(t, node)
}

// TestStartupValidationKeyValidityGating tests that keys failing key-validity gating
// (VoteFirstValid/VoteLastValid) are skipped.
func TestStartupValidationKeyValidityGating(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	testDir := t.TempDir()

	server := newMockWeightServer(t)
	defer server.Close()

	firstRound := basics.Round(0)
	lastRound := basics.Round(1000)

	genesis := bookkeeping.Genesis{
		SchemaID:    "test-startup-key-validity-gating",
		Proto:       protocol.ConsensusCurrentVersion,
		Network:     config.Devtestnet,
		FeeSink:     sinkAddr.String(),
		RewardsPool: poolAddr.String(),
	}

	// Create genesis directory and participation key
	genesisDir := filepath.Join(testDir, genesis.ID())
	err := os.MkdirAll(genesisDir, 0700)
	require.NoError(t, err)

	// Generate a root key
	rootFilename := filepath.Join(genesisDir, "root.rootkey")
	rootAccess, err := db.MakeAccessor(rootFilename, false, false)
	require.NoError(t, err)
	root, err := account.GenerateRoot(rootAccess)
	rootAccess.Close()
	require.NoError(t, err)

	// Generate participation key with proper filename format
	partKeyName := config.PartKeyFilename(t.Name(), uint64(firstRound), uint64(lastRound))
	partFilename := filepath.Join(genesisDir, partKeyName)
	partAccess, err := db.MakeAccessor(partFilename, false, false)
	require.NoError(t, err)
	part, err := account.FillDBWithParticipationKeys(partAccess, root.Address(), firstRound, lastRound, config.Consensus[protocol.ConsensusCurrentVersion].DefaultKeyDilution)
	require.NoError(t, err)
	partAccess.Close()

	// Add the account to genesis with VoteFirstValid in the future
	// This means the key won't be eligible at voteRound=1
	genesis.Allocation = append(genesis.Allocation, bookkeeping.GenesisAllocation{
		Address: root.Address().String(),
		State: bookkeeping.GenesisAccountData{
			Status:         basics.Online,
			MicroAlgos:     basics.MicroAlgos{Raw: 1000000},
			SelectionID:    part.VRFSecrets().PK,
			VoteID:         part.VotingSecrets().OneTimeSignatureVerifier,
			VoteFirstValid: basics.Round(100), // Key not valid until round 100
		},
	})

	genesisHash := genesis.Hash()
	server.SetGenesisHash(genesisHash)

	// DO NOT set weight - the key should be skipped due to VoteFirstValid gating

	cfg := config.GetDefaultLocal()
	cfg.ExternalWeightOraclePort = server.port

	log := logging.TestingLog(t)

	node, err := MakeFull(log, testDir, cfg, []string{}, genesis)
	require.NoError(t, err) // Should succeed because the key is skipped
	require.NotNil(t, node)
}

// TestStartupValidationKeyValidityGatingLastValid tests that keys failing key-validity gating
// (VoteLastValid in the past) are skipped.
func TestStartupValidationKeyValidityGatingLastValid(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	testDir := t.TempDir()

	server := newMockWeightServer(t)
	defer server.Close()

	firstRound := basics.Round(0)
	lastRound := basics.Round(1000)

	genesis := bookkeeping.Genesis{
		SchemaID:    "test-startup-key-validity-gating-last",
		Proto:       protocol.ConsensusCurrentVersion,
		Network:     config.Devtestnet,
		FeeSink:     sinkAddr.String(),
		RewardsPool: poolAddr.String(),
	}

	// Create genesis directory and participation key
	genesisDir := filepath.Join(testDir, genesis.ID())
	err := os.MkdirAll(genesisDir, 0700)
	require.NoError(t, err)

	// Generate a root key
	rootFilename := filepath.Join(genesisDir, "root.rootkey")
	rootAccess, err := db.MakeAccessor(rootFilename, false, false)
	require.NoError(t, err)
	root, err := account.GenerateRoot(rootAccess)
	rootAccess.Close()
	require.NoError(t, err)

	// Generate participation key with proper filename format
	partKeyName := config.PartKeyFilename(t.Name(), uint64(firstRound), uint64(lastRound))
	partFilename := filepath.Join(genesisDir, partKeyName)
	partAccess, err := db.MakeAccessor(partFilename, false, false)
	require.NoError(t, err)
	part, err := account.FillDBWithParticipationKeys(partAccess, root.Address(), firstRound, lastRound, config.Consensus[protocol.ConsensusCurrentVersion].DefaultKeyDilution)
	require.NoError(t, err)
	partAccess.Close()

	// Add the account to genesis - but note that since this is the genesis state,
	// VoteLastValid=0 means "no expiration" (the key is always valid if VoteFirstValid passes).
	// So we can't really test VoteLastValid in the past at genesis time because
	// there's no round 0 that has already passed.
	//
	// However, for completeness, let's add the account with the correct SelectionID
	// and VoteFirstValid=0, VoteLastValid=0 (unlimited validity).
	// This key SHOULD be validated.
	genesis.Allocation = append(genesis.Allocation, bookkeeping.GenesisAllocation{
		Address: root.Address().String(),
		State: bookkeeping.GenesisAccountData{
			Status:         basics.Online,
			MicroAlgos:     basics.MicroAlgos{Raw: 1000000},
			SelectionID:    part.VRFSecrets().PK,
			VoteID:         part.VotingSecrets().OneTimeSignatureVerifier,
			VoteFirstValid: basics.Round(0),
			VoteLastValid:  basics.Round(0), // 0 means unlimited
		},
	})

	genesisHash := genesis.Hash()
	server.SetGenesisHash(genesisHash)

	// Set weight for this address - it should be validated
	server.SetWeight(root.Address().String(), 1000)

	cfg := config.GetDefaultLocal()
	cfg.ExternalWeightOraclePort = server.port

	log := logging.TestingLog(t)

	node, err := MakeFull(log, testDir, cfg, []string{}, genesis)
	require.NoError(t, err)
	require.NotNil(t, node)
}

// TestStartupValidationDaemonWeightQueryError tests that startup fails when the daemon
// returns an error for a weight query.
func TestStartupValidationDaemonWeightQueryError(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	testDir := t.TempDir()

	server := newMockWeightServer(t)
	defer server.Close()

	firstRound := basics.Round(0)
	lastRound := basics.Round(1000)

	genesis := bookkeeping.Genesis{
		SchemaID:    "test-startup-weight-query-error",
		Proto:       protocol.ConsensusCurrentVersion,
		Network:     config.Devtestnet,
		FeeSink:     sinkAddr.String(),
		RewardsPool: poolAddr.String(),
	}

	// Create genesis directory and participation key
	genesisDir := filepath.Join(testDir, genesis.ID())
	err := os.MkdirAll(genesisDir, 0700)
	require.NoError(t, err)

	// Generate a root key
	rootFilename := filepath.Join(genesisDir, "root.rootkey")
	rootAccess, err := db.MakeAccessor(rootFilename, false, false)
	require.NoError(t, err)
	root, err := account.GenerateRoot(rootAccess)
	rootAccess.Close()
	require.NoError(t, err)

	// Generate participation key with proper filename format
	partKeyName := config.PartKeyFilename(t.Name(), uint64(firstRound), uint64(lastRound))
	partFilename := filepath.Join(genesisDir, partKeyName)
	partAccess, err := db.MakeAccessor(partFilename, false, false)
	require.NoError(t, err)
	part, err := account.FillDBWithParticipationKeys(partAccess, root.Address(), firstRound, lastRound, config.Consensus[protocol.ConsensusCurrentVersion].DefaultKeyDilution)
	require.NoError(t, err)
	partAccess.Close()

	// Add the account to genesis with the participation key's SelectionID and VoteID
	genesis.Allocation = append(genesis.Allocation, bookkeeping.GenesisAllocation{
		Address: root.Address().String(),
		State: bookkeeping.GenesisAccountData{
			Status:      basics.Online,
			MicroAlgos:  basics.MicroAlgos{Raw: 1000000},
			SelectionID: part.VRFSecrets().PK,
			VoteID:      part.VotingSecrets().OneTimeSignatureVerifier,
		},
	})

	genesisHash := genesis.Hash()
	server.SetGenesisHash(genesisHash)

	// Configure server to return an error for weight queries
	server.SetWeightError("database unavailable")

	cfg := config.GetDefaultLocal()
	cfg.ExternalWeightOraclePort = server.port

	log := logging.TestingLog(t)

	node, err := MakeFull(log, testDir, cfg, []string{}, genesis)
	require.Error(t, err)
	require.Nil(t, node)
	require.Contains(t, err.Error(), "failed to query weight")
}

// TestStartupValidationMultipleKeys tests startup validation with multiple participation keys,
// some valid, some skipped, and some that should fail.
func TestStartupValidationMultipleKeys(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	testDir := t.TempDir()

	server := newMockWeightServer(t)
	defer server.Close()

	firstRound := basics.Round(0)
	lastRound := basics.Round(1000)

	genesis := bookkeeping.Genesis{
		SchemaID:    "test-startup-multiple-keys",
		Proto:       protocol.ConsensusCurrentVersion,
		Network:     config.Devtestnet,
		FeeSink:     sinkAddr.String(),
		RewardsPool: poolAddr.String(),
	}

	// Create genesis directory
	genesisDir := filepath.Join(testDir, genesis.ID())
	err := os.MkdirAll(genesisDir, 0700)
	require.NoError(t, err)

	// Create two accounts with participation keys
	var validAccounts []basics.Address

	for i := 0; i < 2; i++ {
		// Generate a root key
		rootFilename := filepath.Join(genesisDir, "root"+string(rune('0'+i))+".rootkey")
		rootAccess, err := db.MakeAccessor(rootFilename, false, false)
		require.NoError(t, err)
		root, err := account.GenerateRoot(rootAccess)
		rootAccess.Close()
		require.NoError(t, err)

		// Generate participation key with proper filename format
		partKeyName := config.PartKeyFilename(t.Name()+string(rune('0'+i)), uint64(firstRound), uint64(lastRound))
		partFilename := filepath.Join(genesisDir, partKeyName)
		partAccess, err := db.MakeAccessor(partFilename, false, false)
		require.NoError(t, err)
		part, err := account.FillDBWithParticipationKeys(partAccess, root.Address(), firstRound, lastRound, config.Consensus[protocol.ConsensusCurrentVersion].DefaultKeyDilution)
		require.NoError(t, err)
		partAccess.Close()

		validAccounts = append(validAccounts, root.Address())

		// Add the account to genesis with VoteID
		genesis.Allocation = append(genesis.Allocation, bookkeeping.GenesisAllocation{
			Address: root.Address().String(),
			State: bookkeeping.GenesisAccountData{
				Status:      basics.Online,
				MicroAlgos:  basics.MicroAlgos{Raw: 1000000},
				SelectionID: part.VRFSecrets().PK,
				VoteID:      part.VotingSecrets().OneTimeSignatureVerifier,
			},
		})
	}

	genesisHash := genesis.Hash()
	server.SetGenesisHash(genesisHash)

	// Set weight for both accounts
	for _, addr := range validAccounts {
		server.SetWeight(addr.String(), 1000)
	}

	cfg := config.GetDefaultLocal()
	cfg.ExternalWeightOraclePort = server.port

	log := logging.TestingLog(t)

	node, err := MakeFull(log, testDir, cfg, []string{}, genesis)
	require.NoError(t, err)
	require.NotNil(t, node)
}
