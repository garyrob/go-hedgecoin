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

package ledger

import (
	"github.com/algorand/go-algorand/crypto"
	"github.com/algorand/go-algorand/data/basics"
	"github.com/algorand/go-algorand/ledger/ledgercore"
)

// testWeightOracle is a mock implementation of WeightOracle for ledger tests.
// It returns account stake as weight (simple passthrough behavior for testing).
type testWeightOracle struct {
	ledger *Ledger
}

func (m *testWeightOracle) Weight(balanceRound basics.Round, addr basics.Address, selectionID crypto.VRFVerifier) (uint64, error) {
	// Return the account's stake as weight for testing purposes.
	// If the account has zero stake or doesn't exist, return a default weight of 1
	// to avoid invariant violations in block evaluation. In production, the daemon
	// would only return 0 for accounts that truly have no weight.
	acctData, _, _, _ := m.ledger.LookupLatest(addr)
	if acctData.MicroAlgos.Raw == 0 {
		// Account has zero stake or doesn't exist - return a default weight
		return 1, nil
	}
	return acctData.MicroAlgos.Raw, nil
}

func (m *testWeightOracle) TotalWeight(balanceRound basics.Round, voteRound basics.Round) (uint64, error) {
	// Return the online circulation as total weight for testing purposes
	circulation, err := m.ledger.OnlineCirculation(balanceRound, voteRound)
	if err != nil {
		return 0, err
	}
	return circulation.Raw, nil
}

func (m *testWeightOracle) Ping() error {
	return nil
}

func (m *testWeightOracle) Identity() (ledgercore.DaemonIdentity, error) {
	return ledgercore.DaemonIdentity{
		GenesisHash:            m.ledger.GenesisHash(),
		WeightAlgorithmVersion: "1.0",
		WeightProtocolVersion:  "1.0",
	}, nil
}

// Compile-time check that testWeightOracle implements WeightOracle
var _ ledgercore.WeightOracle = (*testWeightOracle)(nil)

// setupTestWeightOracle sets up a mock weight oracle on the ledger for testing.
// This must be called after creating a ledger to ensure ExternalWeight/TotalExternalWeight
// don't panic during tests.
func setupTestWeightOracle(l *Ledger) {
	mockOracle := &testWeightOracle{ledger: l}
	l.SetWeightOracle(mockOracle)
}
