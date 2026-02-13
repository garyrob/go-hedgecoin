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

package agreement

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/algorand/go-algorand/config"
	"github.com/algorand/go-algorand/crypto"
	"github.com/algorand/go-algorand/data/basics"
	"github.com/algorand/go-algorand/data/committee"
	"github.com/algorand/go-algorand/ledger/ledgercore"
	"github.com/algorand/go-algorand/protocol"
	"github.com/algorand/go-algorand/test/partitiontest"
)

// mockLedgerReaderWithWeights implements both LedgerReader and ExternalWeighter for testing membership().
type mockLedgerReaderWithWeights struct {
	// LedgerReader method implementations
	lookupAgreementFn func(basics.Round, basics.Address) (basics.OnlineAccountData, error)
	circulationFn     func(basics.Round, basics.Round) (basics.MicroAlgos, error)
	seedFn            func(basics.Round) (committee.Seed, error)
	consensusParamsFn func(basics.Round) (config.ConsensusParams, error)

	// ExternalWeighter method implementations (nil means not supported)
	externalWeightFn      func(basics.Round, basics.Address, crypto.VRFVerifier) (uint64, error)
	totalExternalWeightFn func(basics.Round, basics.Round) (uint64, error)

	// Tracking for verification
	externalWeightCalled      bool
	totalExternalWeightCalled bool
}

// LedgerReader interface implementation

func (m *mockLedgerReaderWithWeights) NextRound() basics.Round {
	return basics.Round(1000) // Arbitrary value, not used by membership()
}

func (m *mockLedgerReaderWithWeights) Wait(basics.Round) chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func (m *mockLedgerReaderWithWeights) Seed(r basics.Round) (committee.Seed, error) {
	if m.seedFn != nil {
		return m.seedFn(r)
	}
	return committee.Seed{}, nil
}

func (m *mockLedgerReaderWithWeights) LookupAgreement(r basics.Round, a basics.Address) (basics.OnlineAccountData, error) {
	if m.lookupAgreementFn != nil {
		return m.lookupAgreementFn(r, a)
	}
	return basics.OnlineAccountData{}, nil
}

func (m *mockLedgerReaderWithWeights) Circulation(r basics.Round, voteRnd basics.Round) (basics.MicroAlgos, error) {
	if m.circulationFn != nil {
		return m.circulationFn(r, voteRnd)
	}
	return basics.MicroAlgos{Raw: 1000000}, nil
}

func (m *mockLedgerReaderWithWeights) LookupDigest(basics.Round) (crypto.Digest, error) {
	return crypto.Digest{}, nil
}

func (m *mockLedgerReaderWithWeights) ConsensusParams(r basics.Round) (config.ConsensusParams, error) {
	if m.consensusParamsFn != nil {
		return m.consensusParamsFn(r)
	}
	return config.Consensus[protocol.ConsensusCurrentVersion], nil
}

func (m *mockLedgerReaderWithWeights) ConsensusVersion(basics.Round) (protocol.ConsensusVersion, error) {
	return protocol.ConsensusCurrentVersion, nil
}

// ExternalWeighter interface implementation

func (m *mockLedgerReaderWithWeights) ExternalWeight(balanceRound basics.Round, addr basics.Address, selectionID crypto.VRFVerifier) (uint64, error) {
	m.externalWeightCalled = true
	if m.externalWeightFn != nil {
		return m.externalWeightFn(balanceRound, addr, selectionID)
	}
	return 0, nil
}

func (m *mockLedgerReaderWithWeights) TotalExternalWeight(balanceRound basics.Round, voteRound basics.Round) (uint64, error) {
	m.totalExternalWeightCalled = true
	if m.totalExternalWeightFn != nil {
		return m.totalExternalWeightFn(balanceRound, voteRound)
	}
	return 0, nil
}

// mockLedgerReaderNoWeights implements only LedgerReader (not ExternalWeighter)
// for testing the type assertion failure case.
type mockLedgerReaderNoWeights struct {
	lookupAgreementFn func(basics.Round, basics.Address) (basics.OnlineAccountData, error)
}

func (m *mockLedgerReaderNoWeights) NextRound() basics.Round {
	return basics.Round(1000)
}

func (m *mockLedgerReaderNoWeights) Wait(basics.Round) chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func (m *mockLedgerReaderNoWeights) Seed(basics.Round) (committee.Seed, error) {
	return committee.Seed{}, nil
}

func (m *mockLedgerReaderNoWeights) LookupAgreement(r basics.Round, a basics.Address) (basics.OnlineAccountData, error) {
	if m.lookupAgreementFn != nil {
		return m.lookupAgreementFn(r, a)
	}
	return basics.OnlineAccountData{}, nil
}

func (m *mockLedgerReaderNoWeights) Circulation(basics.Round, basics.Round) (basics.MicroAlgos, error) {
	return basics.MicroAlgos{Raw: 1000000}, nil
}

func (m *mockLedgerReaderNoWeights) LookupDigest(basics.Round) (crypto.Digest, error) {
	return crypto.Digest{}, nil
}

func (m *mockLedgerReaderNoWeights) ConsensusParams(basics.Round) (config.ConsensusParams, error) {
	return config.Consensus[protocol.ConsensusCurrentVersion], nil
}

func (m *mockLedgerReaderNoWeights) ConsensusVersion(basics.Round) (protocol.ConsensusVersion, error) {
	return protocol.ConsensusCurrentVersion, nil
}

// Test: Eligible account should have weights populated correctly
func TestMembershipEligibleAccount(t *testing.T) {
	partitiontest.PartitionTest(t)

	testAddr := basics.Address{1, 2, 3}
	testSelectionID := crypto.VRFVerifier{4, 5, 6}
	testRound := basics.Round(100)

	mock := &mockLedgerReaderWithWeights{
		lookupAgreementFn: func(r basics.Round, a basics.Address) (basics.OnlineAccountData, error) {
			return basics.OnlineAccountData{
				VotingData: basics.VotingData{
					VoteFirstValid: basics.Round(1),
					VoteLastValid:  basics.Round(1000), // r=100 is within valid range
					SelectionID:    testSelectionID,
				},
			}, nil
		},
		externalWeightFn: func(balanceRound basics.Round, addr basics.Address, selectionID crypto.VRFVerifier) (uint64, error) {
			require.Equal(t, testAddr, addr)
			require.Equal(t, testSelectionID, selectionID)
			return 500, nil
		},
		totalExternalWeightFn: func(balanceRound basics.Round, voteRound basics.Round) (uint64, error) {
			return 10000, nil
		},
	}

	m, err := membership(mock, testAddr, testRound, 0, soft)
	require.NoError(t, err)
	require.Equal(t, uint64(500), m.ExternalWeight)
	require.Equal(t, uint64(10000), m.TotalExternalWeight)
	require.True(t, mock.externalWeightCalled)
	require.True(t, mock.totalExternalWeightCalled)
}

// Test: Ineligible account (r > VoteLastValid) should have zero weights and no daemon query
func TestMembershipIneligibleExpiredKeys(t *testing.T) {
	partitiontest.PartitionTest(t)

	testAddr := basics.Address{1, 2, 3}
	testRound := basics.Round(100)

	mock := &mockLedgerReaderWithWeights{
		lookupAgreementFn: func(r basics.Round, a basics.Address) (basics.OnlineAccountData, error) {
			return basics.OnlineAccountData{
				VotingData: basics.VotingData{
					VoteFirstValid: basics.Round(1),
					VoteLastValid:  basics.Round(50), // r=100 is past valid range
				},
			}, nil
		},
		externalWeightFn: func(basics.Round, basics.Address, crypto.VRFVerifier) (uint64, error) {
			t.Fatal("ExternalWeight should not be called for ineligible account")
			return 0, nil
		},
		totalExternalWeightFn: func(basics.Round, basics.Round) (uint64, error) {
			t.Fatal("TotalExternalWeight should not be called for ineligible account")
			return 0, nil
		},
	}

	m, err := membership(mock, testAddr, testRound, 0, soft)
	require.NoError(t, err)
	require.Equal(t, uint64(0), m.ExternalWeight)
	require.Equal(t, uint64(0), m.TotalExternalWeight)
	require.False(t, mock.externalWeightCalled)
	require.False(t, mock.totalExternalWeightCalled)
}

// Test: Ineligible account (r < VoteFirstValid) should have zero weights and no daemon query
func TestMembershipIneligibleKeysNotYetValid(t *testing.T) {
	partitiontest.PartitionTest(t)

	testAddr := basics.Address{1, 2, 3}
	testRound := basics.Round(100)

	mock := &mockLedgerReaderWithWeights{
		lookupAgreementFn: func(r basics.Round, a basics.Address) (basics.OnlineAccountData, error) {
			return basics.OnlineAccountData{
				VotingData: basics.VotingData{
					VoteFirstValid: basics.Round(200), // r=100 is before valid range
					VoteLastValid:  basics.Round(500),
				},
			}, nil
		},
		externalWeightFn: func(basics.Round, basics.Address, crypto.VRFVerifier) (uint64, error) {
			t.Fatal("ExternalWeight should not be called for ineligible account")
			return 0, nil
		},
		totalExternalWeightFn: func(basics.Round, basics.Round) (uint64, error) {
			t.Fatal("TotalExternalWeight should not be called for ineligible account")
			return 0, nil
		},
	}

	m, err := membership(mock, testAddr, testRound, 0, soft)
	require.NoError(t, err)
	require.Equal(t, uint64(0), m.ExternalWeight)
	require.Equal(t, uint64(0), m.TotalExternalWeight)
	require.False(t, mock.externalWeightCalled)
	require.False(t, mock.totalExternalWeightCalled)
}

// Test: Perpetual keys (VoteLastValid == 0) should always be eligible
func TestMembershipPerpetualKeys(t *testing.T) {
	partitiontest.PartitionTest(t)

	testAddr := basics.Address{1, 2, 3}
	testSelectionID := crypto.VRFVerifier{4, 5, 6}
	testRound := basics.Round(100000) // Very high round number

	mock := &mockLedgerReaderWithWeights{
		lookupAgreementFn: func(r basics.Round, a basics.Address) (basics.OnlineAccountData, error) {
			return basics.OnlineAccountData{
				VotingData: basics.VotingData{
					VoteFirstValid: basics.Round(1),
					VoteLastValid:  basics.Round(0), // 0 means perpetual keys
					SelectionID:    testSelectionID,
				},
			}, nil
		},
		externalWeightFn: func(balanceRound basics.Round, addr basics.Address, selectionID crypto.VRFVerifier) (uint64, error) {
			return 750, nil
		},
		totalExternalWeightFn: func(balanceRound basics.Round, voteRound basics.Round) (uint64, error) {
			return 15000, nil
		},
	}

	m, err := membership(mock, testAddr, testRound, 0, soft)
	require.NoError(t, err)
	require.Equal(t, uint64(750), m.ExternalWeight)
	require.Equal(t, uint64(15000), m.TotalExternalWeight)
	require.True(t, mock.externalWeightCalled)
	require.True(t, mock.totalExternalWeightCalled)
}

// Test: ExternalWeighter type assertion failure should panic
func TestMembershipExternalWeighterAssertionFailure(t *testing.T) {
	partitiontest.PartitionTest(t)

	testAddr := basics.Address{1, 2, 3}
	testRound := basics.Round(100)

	mock := &mockLedgerReaderNoWeights{
		lookupAgreementFn: func(r basics.Round, a basics.Address) (basics.OnlineAccountData, error) {
			return basics.OnlineAccountData{
				VotingData: basics.VotingData{
					VoteFirstValid: basics.Round(1),
					VoteLastValid:  basics.Round(1000), // Eligible, so will try to fetch weights
				},
			}, nil
		},
	}

	require.Panics(t, func() {
		membership(mock, testAddr, testRound, 0, soft)
	})
}

// Test: Zero weight returned for eligible account should panic
func TestMembershipZeroWeightPanic(t *testing.T) {
	partitiontest.PartitionTest(t)

	testAddr := basics.Address{1, 2, 3}
	testRound := basics.Round(100)

	mock := &mockLedgerReaderWithWeights{
		lookupAgreementFn: func(r basics.Round, a basics.Address) (basics.OnlineAccountData, error) {
			return basics.OnlineAccountData{
				VotingData: basics.VotingData{
					VoteFirstValid: basics.Round(1),
					VoteLastValid:  basics.Round(1000),
				},
			}, nil
		},
		externalWeightFn: func(basics.Round, basics.Address, crypto.VRFVerifier) (uint64, error) {
			return 0, nil // Zero weight for eligible participant
		},
		totalExternalWeightFn: func(basics.Round, basics.Round) (uint64, error) {
			return 10000, nil
		},
	}

	require.Panics(t, func() {
		membership(mock, testAddr, testRound, 0, soft)
	})
}

// Test: Zero total weight should panic
func TestMembershipZeroTotalWeightPanic(t *testing.T) {
	partitiontest.PartitionTest(t)

	testAddr := basics.Address{1, 2, 3}
	testRound := basics.Round(100)

	mock := &mockLedgerReaderWithWeights{
		lookupAgreementFn: func(r basics.Round, a basics.Address) (basics.OnlineAccountData, error) {
			return basics.OnlineAccountData{
				VotingData: basics.VotingData{
					VoteFirstValid: basics.Round(1),
					VoteLastValid:  basics.Round(1000),
				},
			}, nil
		},
		externalWeightFn: func(basics.Round, basics.Address, crypto.VRFVerifier) (uint64, error) {
			return 500, nil
		},
		totalExternalWeightFn: func(basics.Round, basics.Round) (uint64, error) {
			return 0, nil // Zero total weight
		},
	}

	require.Panics(t, func() {
		membership(mock, testAddr, testRound, 0, soft)
	})
}

// Test: TotalExternalWeight < ExternalWeight should panic
func TestMembershipTotalLessThanIndividualPanic(t *testing.T) {
	partitiontest.PartitionTest(t)

	testAddr := basics.Address{1, 2, 3}
	testRound := basics.Round(100)

	mock := &mockLedgerReaderWithWeights{
		lookupAgreementFn: func(r basics.Round, a basics.Address) (basics.OnlineAccountData, error) {
			return basics.OnlineAccountData{
				VotingData: basics.VotingData{
					VoteFirstValid: basics.Round(1),
					VoteLastValid:  basics.Round(1000),
				},
			}, nil
		},
		externalWeightFn: func(basics.Round, basics.Address, crypto.VRFVerifier) (uint64, error) {
			return 500, nil
		},
		totalExternalWeightFn: func(basics.Round, basics.Round) (uint64, error) {
			return 100, nil // Less than individual weight (500)
		},
	}

	require.Panics(t, func() {
		membership(mock, testAddr, testRound, 0, soft)
	})
}

// Test: DaemonError with "not_found" code should panic
func TestMembershipDaemonErrorNotFoundPanic(t *testing.T) {
	partitiontest.PartitionTest(t)

	testAddr := basics.Address{1, 2, 3}
	testRound := basics.Round(100)

	mock := &mockLedgerReaderWithWeights{
		lookupAgreementFn: func(r basics.Round, a basics.Address) (basics.OnlineAccountData, error) {
			return basics.OnlineAccountData{
				VotingData: basics.VotingData{
					VoteFirstValid: basics.Round(1),
					VoteLastValid:  basics.Round(1000),
				},
			}, nil
		},
		externalWeightFn: func(basics.Round, basics.Address, crypto.VRFVerifier) (uint64, error) {
			return 0, &ledgercore.DaemonError{Code: "not_found", Msg: "account not found"}
		},
		totalExternalWeightFn: func(basics.Round, basics.Round) (uint64, error) {
			return 10000, nil
		},
	}

	require.Panics(t, func() {
		membership(mock, testAddr, testRound, 0, soft)
	})
}

// Test: DaemonError with "bad_request" code should panic
func TestMembershipDaemonErrorBadRequestPanic(t *testing.T) {
	partitiontest.PartitionTest(t)

	testAddr := basics.Address{1, 2, 3}
	testRound := basics.Round(100)

	mock := &mockLedgerReaderWithWeights{
		lookupAgreementFn: func(r basics.Round, a basics.Address) (basics.OnlineAccountData, error) {
			return basics.OnlineAccountData{
				VotingData: basics.VotingData{
					VoteFirstValid: basics.Round(1),
					VoteLastValid:  basics.Round(1000),
				},
			}, nil
		},
		externalWeightFn: func(basics.Round, basics.Address, crypto.VRFVerifier) (uint64, error) {
			return 0, &ledgercore.DaemonError{Code: "bad_request", Msg: "invalid request"}
		},
		totalExternalWeightFn: func(basics.Round, basics.Round) (uint64, error) {
			return 10000, nil
		},
	}

	require.Panics(t, func() {
		membership(mock, testAddr, testRound, 0, soft)
	})
}

// Test: DaemonError with "unsupported" code should panic
func TestMembershipDaemonErrorUnsupportedPanic(t *testing.T) {
	partitiontest.PartitionTest(t)

	testAddr := basics.Address{1, 2, 3}
	testRound := basics.Round(100)

	mock := &mockLedgerReaderWithWeights{
		lookupAgreementFn: func(r basics.Round, a basics.Address) (basics.OnlineAccountData, error) {
			return basics.OnlineAccountData{
				VotingData: basics.VotingData{
					VoteFirstValid: basics.Round(1),
					VoteLastValid:  basics.Round(1000),
				},
			}, nil
		},
		externalWeightFn: func(basics.Round, basics.Address, crypto.VRFVerifier) (uint64, error) {
			return 0, &ledgercore.DaemonError{Code: "unsupported", Msg: "operation not supported"}
		},
		totalExternalWeightFn: func(basics.Round, basics.Round) (uint64, error) {
			return 10000, nil
		},
	}

	require.Panics(t, func() {
		membership(mock, testAddr, testRound, 0, soft)
	})
}

// Test: DaemonError with "internal" code should return error (not panic)
func TestMembershipDaemonErrorInternalReturnsError(t *testing.T) {
	partitiontest.PartitionTest(t)

	testAddr := basics.Address{1, 2, 3}
	testRound := basics.Round(100)

	mock := &mockLedgerReaderWithWeights{
		lookupAgreementFn: func(r basics.Round, a basics.Address) (basics.OnlineAccountData, error) {
			return basics.OnlineAccountData{
				VotingData: basics.VotingData{
					VoteFirstValid: basics.Round(1),
					VoteLastValid:  basics.Round(1000),
				},
			}, nil
		},
		externalWeightFn: func(basics.Round, basics.Address, crypto.VRFVerifier) (uint64, error) {
			return 0, &ledgercore.DaemonError{Code: "internal", Msg: "internal server error"}
		},
		totalExternalWeightFn: func(basics.Round, basics.Round) (uint64, error) {
			return 10000, nil
		},
	}

	// Should NOT panic; should return error
	require.NotPanics(t, func() {
		_, err := membership(mock, testAddr, testRound, 0, soft)
		require.Error(t, err)
		require.Contains(t, err.Error(), "Failed to obtain external weight")
	})
}

// Test: Network error (non-DaemonError) should return error (not panic)
func TestMembershipNetworkErrorReturnsError(t *testing.T) {
	partitiontest.PartitionTest(t)

	testAddr := basics.Address{1, 2, 3}
	testRound := basics.Round(100)

	networkError := errors.New("connection timeout")

	mock := &mockLedgerReaderWithWeights{
		lookupAgreementFn: func(r basics.Round, a basics.Address) (basics.OnlineAccountData, error) {
			return basics.OnlineAccountData{
				VotingData: basics.VotingData{
					VoteFirstValid: basics.Round(1),
					VoteLastValid:  basics.Round(1000),
				},
			}, nil
		},
		externalWeightFn: func(basics.Round, basics.Address, crypto.VRFVerifier) (uint64, error) {
			return 0, networkError
		},
		totalExternalWeightFn: func(basics.Round, basics.Round) (uint64, error) {
			return 10000, nil
		},
	}

	// Should NOT panic; should return error
	require.NotPanics(t, func() {
		_, err := membership(mock, testAddr, testRound, 0, soft)
		require.Error(t, err)
		require.Contains(t, err.Error(), "Failed to obtain external weight")
	})
}

// Test: DaemonError "internal" on TotalExternalWeight should return error (not panic)
func TestMembershipTotalWeightDaemonErrorInternalReturnsError(t *testing.T) {
	partitiontest.PartitionTest(t)

	testAddr := basics.Address{1, 2, 3}
	testRound := basics.Round(100)

	mock := &mockLedgerReaderWithWeights{
		lookupAgreementFn: func(r basics.Round, a basics.Address) (basics.OnlineAccountData, error) {
			return basics.OnlineAccountData{
				VotingData: basics.VotingData{
					VoteFirstValid: basics.Round(1),
					VoteLastValid:  basics.Round(1000),
				},
			}, nil
		},
		externalWeightFn: func(basics.Round, basics.Address, crypto.VRFVerifier) (uint64, error) {
			return 500, nil
		},
		totalExternalWeightFn: func(basics.Round, basics.Round) (uint64, error) {
			return 0, &ledgercore.DaemonError{Code: "internal", Msg: "internal server error"}
		},
	}

	// Should NOT panic; should return error
	require.NotPanics(t, func() {
		_, err := membership(mock, testAddr, testRound, 0, soft)
		require.Error(t, err)
		require.Contains(t, err.Error(), "Failed to obtain total external weight")
	})
}

// Test: DaemonError "not_found" on TotalExternalWeight should panic
func TestMembershipTotalWeightDaemonErrorNotFoundPanic(t *testing.T) {
	partitiontest.PartitionTest(t)

	testAddr := basics.Address{1, 2, 3}
	testRound := basics.Round(100)

	mock := &mockLedgerReaderWithWeights{
		lookupAgreementFn: func(r basics.Round, a basics.Address) (basics.OnlineAccountData, error) {
			return basics.OnlineAccountData{
				VotingData: basics.VotingData{
					VoteFirstValid: basics.Round(1),
					VoteLastValid:  basics.Round(1000),
				},
			}, nil
		},
		externalWeightFn: func(basics.Round, basics.Address, crypto.VRFVerifier) (uint64, error) {
			return 500, nil
		},
		totalExternalWeightFn: func(basics.Round, basics.Round) (uint64, error) {
			return 0, &ledgercore.DaemonError{Code: "not_found", Msg: "total weight not found"}
		},
	}

	require.Panics(t, func() {
		membership(mock, testAddr, testRound, 0, soft)
	})
}

// Test: Network error on TotalExternalWeight should return error (not panic)
func TestMembershipTotalWeightNetworkErrorReturnsError(t *testing.T) {
	partitiontest.PartitionTest(t)

	testAddr := basics.Address{1, 2, 3}
	testRound := basics.Round(100)

	networkError := errors.New("connection refused")

	mock := &mockLedgerReaderWithWeights{
		lookupAgreementFn: func(r basics.Round, a basics.Address) (basics.OnlineAccountData, error) {
			return basics.OnlineAccountData{
				VotingData: basics.VotingData{
					VoteFirstValid: basics.Round(1),
					VoteLastValid:  basics.Round(1000),
				},
			}, nil
		},
		externalWeightFn: func(basics.Round, basics.Address, crypto.VRFVerifier) (uint64, error) {
			return 500, nil
		},
		totalExternalWeightFn: func(basics.Round, basics.Round) (uint64, error) {
			return 0, networkError
		},
	}

	// Should NOT panic; should return error
	require.NotPanics(t, func() {
		_, err := membership(mock, testAddr, testRound, 0, soft)
		require.Error(t, err)
		require.Contains(t, err.Error(), "Failed to obtain total external weight")
	})
}

// Test: Boundary condition - round equals VoteFirstValid
func TestMembershipBoundaryRoundEqualsVoteFirstValid(t *testing.T) {
	partitiontest.PartitionTest(t)

	testAddr := basics.Address{1, 2, 3}
	testRound := basics.Round(100) // Same as VoteFirstValid

	mock := &mockLedgerReaderWithWeights{
		lookupAgreementFn: func(r basics.Round, a basics.Address) (basics.OnlineAccountData, error) {
			return basics.OnlineAccountData{
				VotingData: basics.VotingData{
					VoteFirstValid: basics.Round(100), // Exactly equal to testRound
					VoteLastValid:  basics.Round(500),
				},
			}, nil
		},
		externalWeightFn: func(basics.Round, basics.Address, crypto.VRFVerifier) (uint64, error) {
			return 300, nil
		},
		totalExternalWeightFn: func(basics.Round, basics.Round) (uint64, error) {
			return 5000, nil
		},
	}

	m, err := membership(mock, testAddr, testRound, 0, soft)
	require.NoError(t, err)
	require.Equal(t, uint64(300), m.ExternalWeight)
	require.Equal(t, uint64(5000), m.TotalExternalWeight)
	require.True(t, mock.externalWeightCalled)
}

// Test: Boundary condition - round equals VoteLastValid
func TestMembershipBoundaryRoundEqualsVoteLastValid(t *testing.T) {
	partitiontest.PartitionTest(t)

	testAddr := basics.Address{1, 2, 3}
	testRound := basics.Round(500) // Same as VoteLastValid

	mock := &mockLedgerReaderWithWeights{
		lookupAgreementFn: func(r basics.Round, a basics.Address) (basics.OnlineAccountData, error) {
			return basics.OnlineAccountData{
				VotingData: basics.VotingData{
					VoteFirstValid: basics.Round(100),
					VoteLastValid:  basics.Round(500), // Exactly equal to testRound
				},
			}, nil
		},
		externalWeightFn: func(basics.Round, basics.Address, crypto.VRFVerifier) (uint64, error) {
			return 400, nil
		},
		totalExternalWeightFn: func(basics.Round, basics.Round) (uint64, error) {
			return 8000, nil
		},
	}

	m, err := membership(mock, testAddr, testRound, 0, soft)
	require.NoError(t, err)
	require.Equal(t, uint64(400), m.ExternalWeight)
	require.Equal(t, uint64(8000), m.TotalExternalWeight)
	require.True(t, mock.externalWeightCalled)
}

// Test: Boundary condition - round is one past VoteLastValid (should be ineligible)
func TestMembershipBoundaryRoundOnePastVoteLastValid(t *testing.T) {
	partitiontest.PartitionTest(t)

	testAddr := basics.Address{1, 2, 3}
	testRound := basics.Round(501) // One past VoteLastValid

	mock := &mockLedgerReaderWithWeights{
		lookupAgreementFn: func(r basics.Round, a basics.Address) (basics.OnlineAccountData, error) {
			return basics.OnlineAccountData{
				VotingData: basics.VotingData{
					VoteFirstValid: basics.Round(100),
					VoteLastValid:  basics.Round(500), // testRound is just past this
				},
			}, nil
		},
		externalWeightFn: func(basics.Round, basics.Address, crypto.VRFVerifier) (uint64, error) {
			t.Fatal("ExternalWeight should not be called for ineligible account")
			return 0, nil
		},
		totalExternalWeightFn: func(basics.Round, basics.Round) (uint64, error) {
			t.Fatal("TotalExternalWeight should not be called for ineligible account")
			return 0, nil
		},
	}

	m, err := membership(mock, testAddr, testRound, 0, soft)
	require.NoError(t, err)
	require.Equal(t, uint64(0), m.ExternalWeight)
	require.Equal(t, uint64(0), m.TotalExternalWeight)
	require.False(t, mock.externalWeightCalled)
}
