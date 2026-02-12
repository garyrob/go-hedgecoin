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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/algorand/go-algorand/crypto"
	"github.com/algorand/go-algorand/data/basics"
	"github.com/algorand/go-algorand/test/partitiontest"
)

// mockWeighter is a mock implementation of ExternalWeighter for compile-time interface verification.
type mockWeighter struct{}

func (m *mockWeighter) ExternalWeight(balanceRound basics.Round, addr basics.Address, selectionID crypto.VRFVerifier) (uint64, error) {
	return 0, nil
}

func (m *mockWeighter) TotalExternalWeight(balanceRound basics.Round, voteRound basics.Round) (uint64, error) {
	return 0, nil
}

// Compile-time interface satisfaction check
var _ ExternalWeighter = (*mockWeighter)(nil)

func TestExternalWeighterInterface(t *testing.T) {
	partitiontest.PartitionTest(t)
	t.Parallel()

	// This test verifies that the mockWeighter satisfies ExternalWeighter.
	// The compile-time check above already ensures this, but this test
	// documents the expected behavior and verifies the interface can be used.
	var weighter ExternalWeighter = &mockWeighter{}

	// Verify methods can be called
	weight, err := weighter.ExternalWeight(basics.Round(100), basics.Address{}, crypto.VRFVerifier{})
	require.NoError(t, err)
	require.Equal(t, uint64(0), weight)

	totalWeight, err := weighter.TotalExternalWeight(basics.Round(100), basics.Round(110))
	require.NoError(t, err)
	require.Equal(t, uint64(0), totalWeight)
}
