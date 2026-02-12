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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/algorand/go-algorand/crypto"
	"github.com/algorand/go-algorand/data/basics"
	"github.com/algorand/go-algorand/test/partitiontest"
)

// mockOracle is a mock implementation of WeightOracle for compile-time interface verification.
type mockOracle struct{}

func (m *mockOracle) Weight(balanceRound basics.Round, addr basics.Address, selectionID crypto.VRFVerifier) (uint64, error) {
	return 0, nil
}

func (m *mockOracle) TotalWeight(balanceRound basics.Round, voteRound basics.Round) (uint64, error) {
	return 0, nil
}

func (m *mockOracle) Ping() error {
	return nil
}

func (m *mockOracle) Identity() (DaemonIdentity, error) {
	return DaemonIdentity{}, nil
}

// Compile-time interface satisfaction check
var _ WeightOracle = (*mockOracle)(nil)

func TestDaemonErrorFormat(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	tests := []struct {
		name     string
		code     string
		msg      string
		expected string
	}{
		{
			name:     "not_found error",
			code:     "not_found",
			msg:      "account not found",
			expected: "daemon error [not_found]: account not found",
		},
		{
			name:     "internal error",
			code:     "internal",
			msg:      "database connection failed",
			expected: "daemon error [internal]: database connection failed",
		},
		{
			name:     "bad_request error",
			code:     "bad_request",
			msg:      "invalid round number",
			expected: "daemon error [bad_request]: invalid round number",
		},
		{
			name:     "unsupported error",
			code:     "unsupported",
			msg:      "feature not available",
			expected: "daemon error [unsupported]: feature not available",
		},
		{
			name:     "empty message",
			code:     "internal",
			msg:      "",
			expected: "daemon error [internal]: ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &DaemonError{Code: tt.code, Msg: tt.msg}
			require.Equal(t, tt.expected, err.Error())
		})
	}
}

func TestIsDaemonError(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	t.Run("matching code", func(t *testing.T) {
		err := &DaemonError{Code: "not_found", Msg: "account not found"}
		require.True(t, IsDaemonError(err, "not_found"))
	})

	t.Run("non-matching code", func(t *testing.T) {
		err := &DaemonError{Code: "not_found", Msg: "account not found"}
		require.False(t, IsDaemonError(err, "internal"))
	})

	t.Run("non-DaemonError", func(t *testing.T) {
		err := errors.New("some other error")
		require.False(t, IsDaemonError(err, "not_found"))
	})

	t.Run("nil error", func(t *testing.T) {
		require.False(t, IsDaemonError(nil, "not_found"))
	})

	t.Run("wrapped DaemonError matching code", func(t *testing.T) {
		inner := &DaemonError{Code: "internal", Msg: "something went wrong"}
		wrapped := fmt.Errorf("wrapper: %w", inner)
		require.True(t, IsDaemonError(wrapped, "internal"))
	})

	t.Run("wrapped DaemonError non-matching code", func(t *testing.T) {
		inner := &DaemonError{Code: "internal", Msg: "something went wrong"}
		wrapped := fmt.Errorf("wrapper: %w", inner)
		require.False(t, IsDaemonError(wrapped, "not_found"))
	})

	t.Run("doubly wrapped DaemonError", func(t *testing.T) {
		inner := &DaemonError{Code: "bad_request", Msg: "invalid input"}
		wrapped := fmt.Errorf("middle: %w", inner)
		doubleWrapped := fmt.Errorf("outer: %w", wrapped)
		require.True(t, IsDaemonError(doubleWrapped, "bad_request"))
	})
}

func TestErrorsAsUnwrapping(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	t.Run("extract DaemonError from wrapped error", func(t *testing.T) {
		inner := &DaemonError{Code: "not_found", Msg: "account XYZ not found"}
		wrapped := fmt.Errorf("failed to get weight: %w", inner)

		var de *DaemonError
		require.ErrorAs(t, wrapped, &de)
		require.Equal(t, "not_found", de.Code)
		require.Equal(t, "account XYZ not found", de.Msg)
	})

	t.Run("errors.As returns false for non-DaemonError", func(t *testing.T) {
		err := errors.New("plain error")

		var de *DaemonError
		require.NotErrorAs(t, err, &de)
	})
}

func TestVersionConstants(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	// Verify constants are set correctly
	require.Equal(t, "1.0", ExpectedWeightAlgorithmVersion)
	require.Equal(t, "1.0", ExpectedWeightProtocolVersion)
	require.Equal(t, uint64(20), AbsenteeismMultiplier)
}

func TestDaemonIdentityFields(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	// Verify DaemonIdentity struct can be created and its fields accessed
	identity := DaemonIdentity{
		GenesisHash:            crypto.Digest{1, 2, 3},
		WeightAlgorithmVersion: "1.0",
		WeightProtocolVersion:  "1.0",
	}

	require.Equal(t, crypto.Digest{1, 2, 3}, identity.GenesisHash)
	require.Equal(t, "1.0", identity.WeightAlgorithmVersion)
	require.Equal(t, "1.0", identity.WeightProtocolVersion)
}
