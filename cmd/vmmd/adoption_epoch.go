package main

// vmmd restart adoption ADR §3, slice 3: the node's ownership epoch. Every
// adopting start claims the next epoch with a compare-and-swap on
// compute_nodes, mirrored in the resource journal's .epoch file around the
// database write. Adoption is allowed only when the database epoch is
// explained by this journal's own last claim; otherwise someone else
// advanced the node (an operator re-registration, a lost or foreign
// journal) and the successor adopts nothing.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

type vmmdEpochStore interface {
	ComputeNodeVMMDEpoch(ctx context.Context, nodeID string) (state.VMMDEpoch, error)
	ClaimComputeNodeVMMDEpoch(ctx context.Context, nodeID string, expected uint64, incarnation string) (uint64, error)
}

type epochJournal interface {
	ReadEpoch() (fcvm.JournalEpoch, bool, error)
	WriteEpoch(fcvm.JournalEpoch) error
}

// errAdoptionFenced reports a database epoch no claim of this node explains.
var errAdoptionFenced = errors.New("vmmd: ownership epoch advanced outside this node's claims; adopting nothing")

// ownershipClaim is one start's claimed epoch and the incarnation it took
// the node over from.
type ownershipClaim struct {
	NodeID      string
	Epoch       uint64
	Incarnation string
	Predecessor string
}

// claimOwnershipEpoch claims the node's next epoch. It returns the claim,
// and errAdoptionFenced (with the epoch still claimed) when the predecessor's
// epoch is not this journal's. A zero Epoch means nothing was claimed.
func claimOwnershipEpoch(ctx context.Context, journal epochJournal, store vmmdEpochStore, nodeID, incarnation, bootID string) (ownershipClaim, error) {
	local, haveLocal, err := journal.ReadEpoch()
	if err != nil {
		return ownershipClaim{}, fmt.Errorf("vmmd: read journal epoch: %w", err)
	}
	db, err := store.ComputeNodeVMMDEpoch(ctx, nodeID)
	if err != nil {
		return ownershipClaim{}, fmt.Errorf("vmmd: read node epoch: %w", err)
	}
	explained := epochExplained(local, haveLocal, db, nodeID)
	claim := fcvm.JournalEpoch{NodeID: nodeID, Epoch: db.Epoch + 1, BootID: bootID, Incarnation: incarnation, State: fcvm.JournalEpochClaiming}
	if err := journal.WriteEpoch(claim); err != nil {
		return ownershipClaim{}, fmt.Errorf("vmmd: record epoch intent: %w", err)
	}
	next, err := store.ClaimComputeNodeVMMDEpoch(ctx, nodeID, db.Epoch, incarnation)
	if err != nil {
		return ownershipClaim{}, fmt.Errorf("vmmd: claim node epoch: %w", err)
	}
	claim.Epoch, claim.State = next, fcvm.JournalEpochClaimed
	if err := journal.WriteEpoch(claim); err != nil {
		return ownershipClaim{}, fmt.Errorf("vmmd: record claimed epoch: %w", err)
	}
	got := ownershipClaim{NodeID: nodeID, Epoch: next, Incarnation: incarnation, Predecessor: db.Incarnation}
	if !explained {
		return got, errAdoptionFenced
	}
	return got, nil
}

// epochExplained reports whether db is the epoch this journal last claimed
// (or was about to claim when its vmmd stopped).
func epochExplained(local fcvm.JournalEpoch, haveLocal bool, db state.VMMDEpoch, nodeID string) bool {
	if !haveLocal {
		return db.Epoch == 0 // The node's first adopting start.
	}
	if local.NodeID != nodeID {
		return false
	}
	switch local.State {
	case fcvm.JournalEpochClaimed:
		return db.Epoch == local.Epoch && db.Incarnation == local.Incarnation
	case fcvm.JournalEpochClaiming:
		// Either the claim never reached the database, or it did and the
		// file was not updated before the predecessor stopped.
		return db.Epoch == local.Epoch-1 || (db.Epoch == local.Epoch && db.Incarnation == local.Incarnation)
	}
	return false
}

func kernelBootID() (string, error) {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
