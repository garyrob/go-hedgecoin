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
	"fmt"

	"github.com/algorand/go-algorand/config"
	"github.com/algorand/go-algorand/data/basics"
	"github.com/algorand/go-algorand/data/committee"
	"github.com/algorand/go-algorand/ledger/ledgercore"
	"github.com/algorand/go-algorand/logging"
	"github.com/algorand/go-algorand/protocol"
)

// A Selector is the input used to define proposers and members of voting
// committees.
type selector struct {
	_struct struct{} `codec:""` // not omitempty

	Seed   committee.Seed `codec:"seed"`
	Round  basics.Round   `codec:"rnd"`
	Period period         `codec:"per"`
	Step   step           `codec:"step"`
}

// ToBeHashed implements the crypto.Hashable interface.
func (sel selector) ToBeHashed() (protocol.HashID, []byte) {
	return protocol.AgreementSelector, protocol.Encode(&sel)
}

// CommitteeSize returns the size of the committee, which is determined by
// Selector.Step.
func (sel selector) CommitteeSize(proto config.ConsensusParams) uint64 {
	return sel.Step.committeeSize(proto)
}

// BalanceRound returns the round that should be considered by agreement when
// looking at online stake (and status and key material). It is exported so that
// AVM can provide opcodes that return the same data.
func BalanceRound(r basics.Round, cparams config.ConsensusParams) basics.Round {
	return r.SubSaturate(BalanceLookback(cparams))
}

// BalanceLookback is how far back agreement looks when considering balances for
// voting stake.
func BalanceLookback(cparams config.ConsensusParams) basics.Round {
	return basics.Round(2 * cparams.SeedRefreshInterval * cparams.SeedLookback)
}

func seedRound(r basics.Round, cparams config.ConsensusParams) basics.Round {
	return r.SubSaturate(basics.Round(cparams.SeedLookback))
}

// membership obtains membership verification parameters for the given address and round.
func membership(l LedgerReader, addr basics.Address, r basics.Round, p period, s step) (m committee.Membership, err error) {
	cparams, err := l.ConsensusParams(ParamsRound(r))
	if err != nil {
		return
	}
	balanceRound := BalanceRound(r, cparams)
	seedRound := seedRound(r, cparams)

	record, err := l.LookupAgreement(balanceRound, addr)
	if err != nil {
		err = fmt.Errorf("membership (r=%d): Failed to obtain balance record for address %v in round %d: %w", r, addr, balanceRound, err)
		return
	}

	total, err := l.Circulation(balanceRound, r)
	if err != nil {
		err = fmt.Errorf("membership (r=%d): Failed to obtain total circulation in round %d: %v", r, balanceRound, err)
		return
	}

	seed, err := l.Seed(seedRound)
	if err != nil {
		err = fmt.Errorf("membership (r=%d): Failed to obtain seed in round %d: %v", r, seedRound, err)
		return
	}

	m.Record = committee.BalanceRecord{OnlineAccountData: record, Addr: addr}
	m.Selector = selector{Seed: seed, Round: r, Period: p, Step: s}
	m.TotalMoney = total

	// CRITICAL: Gate weight queries on vote-key validity (see DD §3.2).
	// membership() is called BEFORE vote-key validity checks in vote.go,
	// so we may receive messages from accounts with expired/invalid keys.
	// Without this check, we would panic on valid daemon responses for ineligible accounts.
	keyEligible := (r >= record.VoteFirstValid) && (record.VoteLastValid == 0 || r <= record.VoteLastValid)

	if !keyEligible {
		// Leave ExternalWeight and TotalExternalWeight as zero.
		// vote.verify will reject this message immediately afterward
		// based on the same key validity check.
		return m, nil
	}

	// Fetch external weights - REQUIRED for this weighted-selection network.
	// Only reached for accounts with valid vote keys at round r.
	ew, ok := l.(ledgercore.ExternalWeighter)
	if !ok {
		// This is a local invariant violation: startup should have validated oracle configuration.
		logging.Base().Panicf("membership (r=%d): weighted network requires ExternalWeighter support", r)
	}

	m.ExternalWeight, err = ew.ExternalWeight(balanceRound, addr, record.SelectionID)
	if err != nil {
		// Check error type: not_found/bad_request/unsupported are invariant violations
		// (we only query for key-eligible participants per §3.2), internal is operational
		var de *ledgercore.DaemonError
		if errors.As(err, &de) && de.Code != "internal" {
			// not_found, bad_request, unsupported → invariant violation
			logging.Base().Panicf("membership (r=%d): daemon invariant violation for addr %v: %v", r, addr, err)
		}
		// internal or network error → return error for operational handling
		err = fmt.Errorf("membership (r=%d): Failed to obtain external weight for address %v: %w", r, addr, err)
		return
	}

	m.TotalExternalWeight, err = ew.TotalExternalWeight(balanceRound, r)
	if err != nil {
		var de *ledgercore.DaemonError
		if errors.As(err, &de) && de.Code != "internal" {
			logging.Base().Panicf("membership (r=%d): daemon invariant violation for total weight: %v", r, err)
		}
		err = fmt.Errorf("membership (r=%d): Failed to obtain total external weight: %w", r, err)
		return
	}

	// Validate non-zero weight requirements per protocol spec.
	if m.ExternalWeight == 0 {
		logging.Base().Panicf("membership (r=%d): eligible participant %v has zero weight (invalid daemon state)", r, addr)
	}
	if m.TotalExternalWeight == 0 {
		logging.Base().Panicf("membership (r=%d): total weight is zero (invalid daemon state)", r)
	}

	// Validate population alignment: total must include this account's weight
	if m.TotalExternalWeight < m.ExternalWeight {
		logging.Base().Panicf("membership (r=%d): TotalExternalWeight %d < ExternalWeight %d (population alignment violated)",
			r, m.TotalExternalWeight, m.ExternalWeight)
	}

	return m, nil
}
