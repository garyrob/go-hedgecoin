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

package committee

import (
	"math/rand" // used for replicability of sortition benchmark
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/algorand/go-algorand/data/basics"
	"github.com/algorand/go-algorand/test/partitiontest"
)

// test SelfCheckSelected (should always be true, with current testingenv parameters)
// and then set balance to 0 and test not SelfCheckSelected
func TestAccountSelected(t *testing.T) {
	partitiontest.PartitionTest(t)

	seedGen := rand.New(rand.NewSource(1))
	N := 1
	for i := 0; i < N; i++ {
		selParams, _, round, addresses, _, vrfSecrets := testingenv(t, 100, 2000, seedGen)
		period := Period(0)

		leaders := uint64(0)
		for i := range addresses {
			ok, record, selectionSeed, totalMoney := selParams(addresses[i])
			if !ok {
				t.Errorf("can't read selection params")
			}
			sel := AgreementSelector{
				Seed:   selectionSeed,
				Round:  round,
				Period: period,
				Step:   Propose,
			}
			m := Membership{
				Record:              record,
				Selector:            sel,
				TotalMoney:          totalMoney,
				ExternalWeight:      record.VotingStake().Raw,
				TotalExternalWeight: totalMoney.Raw,
			}
			u := MakeCredential(vrfSecrets[i], sel)
			credential, _ := u.Verify(proto, m)
			leaders += credential.Weight
		}

		if (leaders < uint64(proto.NumProposers/2)) || (leaders > uint64(2*proto.NumProposers)) {
			t.Errorf("bad number of leaders %v expected %v", leaders, proto.NumProposers)
		}

		committee := uint64(0)
		step := Soft
		for i, addr := range addresses {
			_, record, selectionSeed, totalMoney := selParams(addr)
			sel := AgreementSelector{
				Seed:   selectionSeed,
				Round:  round,
				Period: period,
				Step:   step,
			}
			m := Membership{
				Record:              record,
				Selector:            sel,
				TotalMoney:          totalMoney,
				ExternalWeight:      record.VotingStake().Raw,
				TotalExternalWeight: totalMoney.Raw,
			}
			u := MakeCredential(vrfSecrets[i], sel)
			credential, _ := u.Verify(proto, m)
			committee += credential.Weight
		}

		if (committee < uint64(0.8*float64(step.CommitteeSize(proto)))) || (committee > uint64(1.2*float64(step.CommitteeSize(proto)))) {
			t.Errorf("bad number of committee members %v expected %v", committee, step.CommitteeSize(proto))
		}
		if i == 0 {
			// pin down deterministic outputs for first iteration
			assert.EqualValues(t, 17, leaders)
			assert.EqualValues(t, 2918, committee)
		}
	}
}

func TestRichAccountSelected(t *testing.T) {
	partitiontest.PartitionTest(t)

	selParams, _, round, addresses, _, vrfSecrets := testingenv(t, 10, 2000, nil)

	period := Period(0)
	ok, record, selectionSeed, _ := selParams(addresses[0])
	if !ok {
		t.Errorf("can't read selection params")
	}

	TotalMoney := basics.MicroAlgos{Raw: 1 << 50}
	record.MicroAlgosWithRewards.Raw = TotalMoney.Raw / 2
	sel := AgreementSelector{
		Seed:   selectionSeed,
		Round:  round,
		Period: period,
		Step:   Propose,
	}
	m := Membership{
		Record:              record,
		Selector:            sel,
		TotalMoney:          TotalMoney,
		ExternalWeight:      record.VotingStake().Raw,
		TotalExternalWeight: TotalMoney.Raw,
	}

	lu := MakeCredential(vrfSecrets[0], sel)
	lcred, _ := lu.Verify(proto, m)

	if lcred.Weight == 0 {
		t.Errorf("bad number of leaders %v expected %v", lcred.Weight, proto.NumProposers)
	}

	step := Cert
	sel = AgreementSelector{
		Seed:   selectionSeed,
		Round:  round,
		Period: period,
		Step:   step,
	}
	m = Membership{
		Record:              record,
		Selector:            sel,
		TotalMoney:          TotalMoney,
		ExternalWeight:      record.VotingStake().Raw,
		TotalExternalWeight: TotalMoney.Raw,
	}

	cu := MakeCredential(vrfSecrets[0], sel)
	ccred, _ := cu.Verify(proto, m)

	if (ccred.Weight < uint64(0.4*float64(step.CommitteeSize(proto)))) || (ccred.Weight > uint64(.6*float64(step.CommitteeSize(proto)))) {
		t.Errorf("bad number of committee members %v expected %v", ccred.Weight, step.CommitteeSize(proto))
	}
	// pin down deterministic outputs, given initial seed values
	assert.EqualValues(t, 6, lcred.Weight)
	assert.EqualValues(t, 735, ccred.Weight)
}

func TestPoorAccountSelectedLeaders(t *testing.T) {
	partitiontest.PartitionTest(t)

	seedGen := rand.New(rand.NewSource(1))
	N := 2
	failsLeaders := 0
	leaders := make([]uint64, N)
	for i := 0; i < N; i++ {
		selParams, _, round, addresses, _, vrfSecrets := testingenv(t, 100, 2000, seedGen)
		period := Period(0)
		for j := range addresses {
			ok, record, selectionSeed, _ := selParams(addresses[j])
			if !ok {
				t.Errorf("can't read selection params")
			}

			sel := AgreementSelector{
				Seed:   selectionSeed,
				Round:  round,
				Period: period,
				Step:   Propose,
			}

			record.MicroAlgosWithRewards.Raw = uint64(1000 / len(addresses))
			m := Membership{
				Record:              record,
				Selector:            sel,
				TotalMoney:          basics.MicroAlgos{Raw: 1000},
				ExternalWeight:      record.VotingStake().Raw,
				TotalExternalWeight: 1000,
			}

			u := MakeCredential(vrfSecrets[j], sel)
			credential, _ := u.Verify(proto, m)
			leaders[i] += credential.Weight

		}

		if leaders[i] < uint64(0.5*float64(proto.NumProposers)) || (leaders[i] > uint64(2*proto.NumProposers)) {
			failsLeaders++
		}
	}

	if failsLeaders == 2 {
		t.Errorf("bad number of leaders %v expected %v", leaders, proto.NumProposers)
	}
	// pin down deterministic outputs, given initial seed values
	assert.EqualValues(t, 18, leaders[0])
	assert.EqualValues(t, 20, leaders[1])
}

func TestPoorAccountSelectedCommittee(t *testing.T) {
	partitiontest.PartitionTest(t)

	seedGen := rand.New(rand.NewSource(1))
	N := 1
	committee := uint64(0)
	for i := 0; i < N; i++ {
		selParams, _, round, addresses, _, vrfSecrets := testingenv(t, 100, 2000, seedGen)
		period := Period(0)

		step := Cert
		for j := range addresses {
			ok, record, selectionSeed, _ := selParams(addresses[j])
			if !ok {
				t.Errorf("can't read selection params")
			}

			sel := AgreementSelector{
				Seed:   selectionSeed,
				Round:  round,
				Period: period,
				Step:   step,
			}

			record.MicroAlgosWithRewards.Raw = uint64(2000 / len(addresses))
			m := Membership{
				Record:              record,
				Selector:            sel,
				TotalMoney:          basics.MicroAlgos{Raw: 2000},
				ExternalWeight:      record.VotingStake().Raw,
				TotalExternalWeight: 2000,
			}
			u := MakeCredential(vrfSecrets[j], sel)
			credential, _ := u.Verify(proto, m)
			committee += credential.Weight
		}

		if (committee < uint64(0.8*float64(step.CommitteeSize(proto)))) || (committee > uint64(1.2*float64(step.CommitteeSize(proto)))) {
			t.Errorf("bad number of committee members %v expected %v", committee, step.CommitteeSize(proto))
		}
		if i == 0 { // pin down deterministic committee size, given initial seed value
			assert.EqualValues(t, 1513, committee)
		}
	}
}

func TestNoMoneyAccountNotSelected(t *testing.T) {
	partitiontest.PartitionTest(t)

	seedGen := rand.New(rand.NewSource(1))
	N := 1
	for i := 0; i < N; i++ {
		selParams, _, round, addresses, _, _ := testingenv(t, 10, 2000, seedGen)
		gen := rand.New(rand.NewSource(2))
		_, _, zeroVRFSecret := newAccount(t, gen)
		period := Period(0)
		ok, record, selectionSeed, _ := selParams(addresses[i])
		if !ok {
			t.Errorf("can't read selection params")
		}
		sel := AgreementSelector{
			Seed:   selectionSeed,
			Round:  round,
			Period: period,
			Step:   Propose,
		}

		record.MicroAlgosWithRewards.Raw = 0
		m := Membership{
			Record:              record,
			Selector:            sel,
			TotalMoney:          basics.MicroAlgos{Raw: 1000},
			ExternalWeight:      0, // zero weight means not selected
			TotalExternalWeight: 1000,
		}
		u := MakeCredential(zeroVRFSecret, sel)
		_, err := u.Verify(proto, m)
		require.Error(t, err, "account should not have been selected")
	}
}

func TestLeadersSelected(t *testing.T) {
	partitiontest.PartitionTest(t)

	selParams, _, round, addresses, _, vrfSecrets := testingenv(t, 100, 2000, nil)

	period := Period(0)
	step := Propose

	ok, record, selectionSeed, _ := selParams(addresses[0])
	if !ok {
		t.Errorf("can't read selection params")
	}

	record.MicroAlgosWithRewards.Raw = 50000
	totalMoney := basics.MicroAlgos{Raw: 100000}

	sel := AgreementSelector{
		Seed:   selectionSeed,
		Round:  round,
		Period: period,
		Step:   step,
	}

	m := Membership{
		Record:              record,
		Selector:            sel,
		TotalMoney:          totalMoney,
		ExternalWeight:      record.VotingStake().Raw,
		TotalExternalWeight: totalMoney.Raw,
	}
	_, err := MakeCredential(vrfSecrets[0], sel).Verify(proto, m)
	require.NoError(t, err, "leader should have been selected")
}

func TestCommitteeSelected(t *testing.T) {
	partitiontest.PartitionTest(t)

	selParams, _, round, addresses, _, vrfSecrets := testingenv(t, 100, 2000, nil)

	period := Period(0)
	step := Soft

	ok, record, selectionSeed, _ := selParams(addresses[0])
	if !ok {
		t.Errorf("can't read selection params")
	}

	record.MicroAlgosWithRewards.Raw = 50000
	totalMoney := basics.MicroAlgos{Raw: 100000}

	sel := AgreementSelector{
		Seed:   selectionSeed,
		Round:  round,
		Period: period,
		Step:   step,
	}

	m := Membership{
		Record:              record,
		Selector:            sel,
		TotalMoney:          totalMoney,
		ExternalWeight:      record.VotingStake().Raw,
		TotalExternalWeight: totalMoney.Raw,
	}
	_, err := MakeCredential(vrfSecrets[0], sel).Verify(proto, m)
	require.NoError(t, err, "committee should have been selected")
}

func TestAccountNotSelected(t *testing.T) {
	partitiontest.PartitionTest(t)

	selParams, _, round, addresses, _, vrfSecrets := testingenv(t, 100, 2000, nil)
	period := Period(0)
	leaders := uint64(0)
	for i := range addresses {
		ok, record, selectionSeed, totalMoney := selParams(addresses[i])
		if !ok {
			t.Errorf("can't read selection params")
		}

		sel := AgreementSelector{
			Seed:   selectionSeed,
			Round:  round,
			Period: period,
			Step:   Propose,
		}
		record.MicroAlgosWithRewards.Raw = 0
		m := Membership{
			Record:              record,
			Selector:            sel,
			TotalMoney:          totalMoney,
			ExternalWeight:      0, // zero weight means not selected
			TotalExternalWeight: totalMoney.Raw,
		}
		credential, _ := MakeCredential(vrfSecrets[i], sel).Verify(proto, m)
		require.False(t, credential.Selected())
		leaders += credential.Weight
	}
	require.Zero(t, leaders)
}

// TestZeroWeightReturnsError verifies that ExternalWeight == 0 returns an error
func TestZeroWeightReturnsError(t *testing.T) {
	partitiontest.PartitionTest(t)

	selParams, _, round, addresses, _, vrfSecrets := testingenv(t, 10, 2000, nil)
	period := Period(0)
	step := Propose

	ok, record, selectionSeed, totalMoney := selParams(addresses[0])
	require.True(t, ok)

	sel := AgreementSelector{
		Seed:   selectionSeed,
		Round:  round,
		Period: period,
		Step:   step,
	}

	m := Membership{
		Record:              record,
		Selector:            sel,
		TotalMoney:          totalMoney,
		ExternalWeight:      0, // zero weight
		TotalExternalWeight: totalMoney.Raw,
	}

	u := MakeCredential(vrfSecrets[0], sel)
	cred, err := u.Verify(proto, m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "weight 0")
	require.Zero(t, cred.Weight)
}

// TestNonZeroWeightRunsSortition verifies that ExternalWeight > 0 runs sortition
func TestNonZeroWeightRunsSortition(t *testing.T) {
	partitiontest.PartitionTest(t)

	selParams, _, round, addresses, _, vrfSecrets := testingenv(t, 10, 2000, nil)
	period := Period(0)
	step := Propose

	ok, record, selectionSeed, totalMoney := selParams(addresses[0])
	require.True(t, ok)

	sel := AgreementSelector{
		Seed:   selectionSeed,
		Round:  round,
		Period: period,
		Step:   step,
	}

	// Set weight to half of total - high probability of selection
	m := Membership{
		Record:              record,
		Selector:            sel,
		TotalMoney:          totalMoney,
		ExternalWeight:      totalMoney.Raw / 2,
		TotalExternalWeight: totalMoney.Raw,
	}

	u := MakeCredential(vrfSecrets[0], sel)
	cred, err := u.Verify(proto, m)
	require.NoError(t, err)
	require.Greater(t, cred.Weight, uint64(0))
}

// TestTotalExternalWeightLessThanExternalWeightPanics verifies population alignment check
func TestTotalExternalWeightLessThanExternalWeightPanics(t *testing.T) {
	partitiontest.PartitionTest(t)

	selParams, _, round, addresses, _, vrfSecrets := testingenv(t, 10, 2000, nil)
	period := Period(0)
	step := Propose

	ok, record, selectionSeed, totalMoney := selParams(addresses[0])
	require.True(t, ok)

	sel := AgreementSelector{
		Seed:   selectionSeed,
		Round:  round,
		Period: period,
		Step:   step,
	}

	m := Membership{
		Record:              record,
		Selector:            sel,
		TotalMoney:          totalMoney,
		ExternalWeight:      1000,
		TotalExternalWeight: 500, // less than ExternalWeight
	}

	u := MakeCredential(vrfSecrets[0], sel)
	require.Panics(t, func() {
		u.Verify(proto, m)
	})
}

// TestTotalExternalWeightZeroWithPositiveWeightPanics verifies panic when TotalExternalWeight is 0 but ExternalWeight > 0
func TestTotalExternalWeightZeroWithPositiveWeightPanics(t *testing.T) {
	partitiontest.PartitionTest(t)

	selParams, _, round, addresses, _, vrfSecrets := testingenv(t, 10, 2000, nil)
	period := Period(0)
	step := Propose

	ok, record, selectionSeed, totalMoney := selParams(addresses[0])
	require.True(t, ok)

	sel := AgreementSelector{
		Seed:   selectionSeed,
		Round:  round,
		Period: period,
		Step:   step,
	}

	m := Membership{
		Record:              record,
		Selector:            sel,
		TotalMoney:          totalMoney,
		ExternalWeight:      100,
		TotalExternalWeight: 0, // zero total with positive individual weight
	}

	u := MakeCredential(vrfSecrets[0], sel)
	require.Panics(t, func() {
		u.Verify(proto, m)
	})
}

// TestExpectedSelectionExceedsTotalWeightPanics verifies panic when expectedSelection > TotalExternalWeight
func TestExpectedSelectionExceedsTotalWeightPanics(t *testing.T) {
	partitiontest.PartitionTest(t)

	selParams, _, round, addresses, _, vrfSecrets := testingenv(t, 10, 2000, nil)
	period := Period(0)
	step := Soft // Soft step has large committee size

	ok, record, selectionSeed, _ := selParams(addresses[0])
	require.True(t, ok)

	sel := AgreementSelector{
		Seed:   selectionSeed,
		Round:  round,
		Period: period,
		Step:   step,
	}

	// CommitteeSize for Soft step is large (e.g., 2990)
	// Set TotalExternalWeight to something smaller than that
	m := Membership{
		Record:              record,
		Selector:            sel,
		TotalMoney:          basics.MicroAlgos{Raw: 100},
		ExternalWeight:      50,
		TotalExternalWeight: 100, // smaller than step.CommitteeSize(proto)
	}

	u := MakeCredential(vrfSecrets[0], sel)
	require.Panics(t, func() {
		u.Verify(proto, m)
	})
}

// TestStakeValueIsIrrelevant verifies that stake value doesn't affect sortition outcome
func TestStakeValueIsIrrelevant(t *testing.T) {
	partitiontest.PartitionTest(t)

	selParams, _, round, addresses, _, vrfSecrets := testingenv(t, 10, 2000, nil)
	period := Period(0)
	step := Propose

	ok, record, selectionSeed, totalMoney := selParams(addresses[0])
	require.True(t, ok)

	sel := AgreementSelector{
		Seed:   selectionSeed,
		Round:  round,
		Period: period,
		Step:   step,
	}

	// First run with stake = 0
	record.MicroAlgosWithRewards.Raw = 0
	m1 := Membership{
		Record:              record,
		Selector:            sel,
		TotalMoney:          totalMoney,
		ExternalWeight:      totalMoney.Raw / 2,
		TotalExternalWeight: totalMoney.Raw,
	}

	u := MakeCredential(vrfSecrets[0], sel)
	cred1, err1 := u.Verify(proto, m1)

	// Second run with stake = 1,000,000
	record.MicroAlgosWithRewards.Raw = 1000000
	m2 := Membership{
		Record:              record,
		Selector:            sel,
		TotalMoney:          totalMoney,
		ExternalWeight:      totalMoney.Raw / 2,
		TotalExternalWeight: totalMoney.Raw,
	}

	cred2, err2 := u.Verify(proto, m2)

	// Both should have identical outcomes since weight is the same
	require.Equal(t, err1 == nil, err2 == nil)
	require.Equal(t, cred1.Weight, cred2.Weight)
	require.Equal(t, cred1.VrfOut, cred2.VrfOut)
}

// TestStatisticalValidation validates that weight-based sortition produces expected distributions.
// This test runs multiple accounts through sortition and verifies the total selected weight
// matches expectations within statistical bounds.
func TestStatisticalValidation(t *testing.T) {
	partitiontest.PartitionTest(t)

	seedGen := rand.New(rand.NewSource(42))
	// Use the same pattern as TestAccountSelected which validates committee sizes
	selParams, _, round, addresses, _, vrfSecrets := testingenv(t, 100, 2000, seedGen)
	period := Period(0)

	// Test leaders (Propose step)
	leaders := uint64(0)
	for i := range addresses {
		ok, record, selectionSeed, totalMoney := selParams(addresses[i])
		if !ok {
			t.Errorf("can't read selection params")
		}
		sel := AgreementSelector{
			Seed:   selectionSeed,
			Round:  round,
			Period: period,
			Step:   Propose,
		}
		m := Membership{
			Record:              record,
			Selector:            sel,
			TotalMoney:          totalMoney,
			ExternalWeight:      record.VotingStake().Raw,
			TotalExternalWeight: totalMoney.Raw,
		}
		u := MakeCredential(vrfSecrets[i], sel)
		credential, _ := u.Verify(proto, m)
		leaders += credential.Weight
	}

	// Validate leaders are within expected bounds (matching pattern from TestAccountSelected)
	require.GreaterOrEqual(t, leaders, uint64(proto.NumProposers/2),
		"Leaders selected too low: got %d, expected at least %d", leaders, proto.NumProposers/2)
	require.LessOrEqual(t, leaders, uint64(2*proto.NumProposers),
		"Leaders selected too high: got %d, expected at most %d", leaders, 2*proto.NumProposers)

	// Test committee (Soft step)
	committee := uint64(0)
	step := Soft
	for i, addr := range addresses {
		_, record, selectionSeed, totalMoney := selParams(addr)
		sel := AgreementSelector{
			Seed:   selectionSeed,
			Round:  round,
			Period: period,
			Step:   step,
		}
		m := Membership{
			Record:              record,
			Selector:            sel,
			TotalMoney:          totalMoney,
			ExternalWeight:      record.VotingStake().Raw,
			TotalExternalWeight: totalMoney.Raw,
		}
		u := MakeCredential(vrfSecrets[i], sel)
		credential, _ := u.Verify(proto, m)
		committee += credential.Weight
	}

	// Validate committee is within ±20% of expected (matching pattern from TestAccountSelected)
	expectedCommittee := float64(step.CommitteeSize(proto))
	require.GreaterOrEqual(t, committee, uint64(0.8*expectedCommittee),
		"Committee selected too low: got %d, expected at least %.0f", committee, 0.8*expectedCommittee)
	require.LessOrEqual(t, committee, uint64(1.2*expectedCommittee),
		"Committee selected too high: got %d, expected at most %.0f", committee, 1.2*expectedCommittee)

	t.Logf("Statistical validation: %d leaders (expected ~%d), %d committee (expected ~%d)",
		leaders, proto.NumProposers, committee, step.CommitteeSize(proto))
}

// TODO update to remove VRF verification overhead
func BenchmarkSortition(b *testing.B) {
	selParams, _, round, addresses, _, vrfSecrets := testingenv(b, 100, 2000, nil)

	period := Period(0)
	step := Soft
	ok, record, selectionSeed, _ := selParams(addresses[0])
	if !ok {
		panic("can't read selection params")
	}

	totalMoney := 100000
	credentials := make([]Credential, b.N)
	money := make([]int, b.N)
	rng := rand.New(rand.NewSource(0))

	for i := 0; i < b.N; i++ {
		money[i] = rng.Intn(totalMoney)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		sel := AgreementSelector{
			Seed:   selectionSeed,
			Round:  round,
			Period: period,
			Step:   step,
		}

		record.MicroAlgosWithRewards.Raw = uint64(money[i])
		m := Membership{
			Record:              record,
			Selector:            sel,
			TotalMoney:          basics.MicroAlgos{Raw: uint64(totalMoney)},
			ExternalWeight:      uint64(money[i]),
			TotalExternalWeight: uint64(totalMoney),
		}
		credentials[i], _ = MakeCredential(vrfSecrets[0], sel).Verify(proto, m)
	}
}
