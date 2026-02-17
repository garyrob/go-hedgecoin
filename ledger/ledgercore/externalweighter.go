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

package ledgercore

import (
	"github.com/algorand/go-algorand/crypto"
	"github.com/algorand/go-algorand/data/basics"
)

// ExternalWeighter defines the interface that the Ledger implements to provide
// external weight lookups to the agreement and evaluation layers.
// This interface is separate from WeightOracle because:
// - WeightOracle is the daemon client interface (used by node/ package)
// - ExternalWeighter is the ledger-layer interface (used by agreement/ and ledger/eval/ via type assertion)
type ExternalWeighter interface {
	// ExternalWeight returns the consensus weight for the given account at the specified balance round.
	// The selectionID is the VRF public key associated with the account's participation keys.
	ExternalWeight(balanceRound basics.Round, addr basics.Address, selectionID crypto.VRFVerifier) (uint64, error)

	// TotalExternalWeight returns the total consensus weight at the specified balance round for voting
	// in the given vote round.
	TotalExternalWeight(balanceRound basics.Round, voteRound basics.Round) (uint64, error)
}
