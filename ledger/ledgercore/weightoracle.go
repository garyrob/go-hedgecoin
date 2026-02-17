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
	"errors"
	"fmt"

	"github.com/algorand/go-algorand/crypto"
	"github.com/algorand/go-algorand/data/basics"
)

// ExpectedWeightAlgorithmVersion is the weight algorithm version that nodes expect
// from the external weight daemon. This ensures all nodes use identical weight
// derivation logic.
const ExpectedWeightAlgorithmVersion = "1.0"

// ExpectedWeightProtocolVersion is the wire protocol version that nodes expect
// from the external weight daemon. This ensures compatibility in communication.
const ExpectedWeightProtocolVersion = "1.0"

// AbsenteeismMultiplier is the factor used for calculating absenteeism thresholds.
// When combined with total weight, it determines the maximum allowed absent weight
// before consensus participation is affected.
const AbsenteeismMultiplier uint64 = 20

// DaemonIdentity contains metadata returned by the weight daemon's identity endpoint.
// This is used to verify that the daemon is compatible with the node's expectations.
type DaemonIdentity struct {
	// GenesisHash is the hash of the genesis block the daemon is configured for.
	GenesisHash crypto.Digest

	// WeightAlgorithmVersion identifies the weight calculation algorithm version.
	WeightAlgorithmVersion string

	// WeightProtocolVersion identifies the wire protocol version.
	WeightProtocolVersion string
}

// Verify DaemonError implements the error interface.
var _ error = (*DaemonError)(nil)

// DaemonError represents an error response from the weight daemon.
// It carries both a machine-readable code and a human-readable message.
type DaemonError struct {
	// Code is a machine-readable error code (e.g., "not_found", "internal", "bad_request", "unsupported")
	Code string

	// Msg is a human-readable error message
	Msg string
}

// Error implements the error interface for DaemonError.
func (e *DaemonError) Error() string {
	return fmt.Sprintf("daemon error [%s]: %s", e.Code, e.Msg)
}

// IsDaemonError checks if err is a DaemonError with the specified code.
// It handles wrapped errors using errors.As.
func IsDaemonError(err error, code string) bool {
	var de *DaemonError
	if errors.As(err, &de) {
		return de.Code == code
	}
	return false
}

// WeightOracle defines the interface for communicating with an external weight daemon.
// It provides methods to query individual account weights and total network weight,
// as well as health check and identity verification.
type WeightOracle interface {
	// Weight returns the consensus weight for the given account at the specified balance round.
	// The selectionID is the VRF public key associated with the account's participation keys.
	Weight(balanceRound basics.Round, addr basics.Address, selectionID crypto.VRFVerifier) (uint64, error)

	// TotalWeight returns the total consensus weight at the specified balance round for voting
	// in the given vote round.
	TotalWeight(balanceRound basics.Round, voteRound basics.Round) (uint64, error)

	// Ping checks if the daemon is reachable and healthy.
	Ping() error

	// Identity returns metadata about the daemon including genesis hash and version information.
	Identity() (DaemonIdentity, error)
}
