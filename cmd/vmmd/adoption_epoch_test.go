package main

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

type memEpochJournal struct {
	e    fcvm.JournalEpoch
	have bool
	fail string // a state whose write fails
}

func (j *memEpochJournal) ReadEpoch() (fcvm.JournalEpoch, bool, error) { return j.e, j.have, nil }
func (j *memEpochJournal) WriteEpoch(e fcvm.JournalEpoch) error {
	if e.State == j.fail {
		return errors.New("disk full")
	}
	j.e, j.have = e, true
	return nil
}

func TestClaimOwnershipEpoch(t *testing.T) {
	ctx := context.Background()
	s := state.NewMemStore()
	node, err := s.CreateComputeNode(ctx, state.ComputeNode{Name: "epoch-node", TargetURL: "unix:///tmp/epoch", VPCPUs: 2, MemMB: 1024, MaxConcurrency: 5, AdmissionCeilingMB: 1024, VCPUBudget: 2, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	boot := uuid.NewString()
	j := &memEpochJournal{}

	// First adopting start: nothing claimed yet anywhere, so adoption may go on.
	first := uuid.NewString()
	if got, err := claimOwnershipEpoch(ctx, j, s, node.ID, first, boot); err != nil || got.Epoch != 1 || got.Predecessor != "" || j.e.State != fcvm.JournalEpochClaimed {
		t.Fatalf("first claim = %+v, %v, journal %+v", got, err, j.e)
	}
	// An ordinary restart follows its own claim.
	second := uuid.NewString()
	if got, err := claimOwnershipEpoch(ctx, j, s, node.ID, second, boot); err != nil || got.Epoch != 2 || got.Predecessor != first || got.Incarnation != second {
		t.Fatalf("restart claim = %+v, %v", got, err)
	}
	if e, _ := s.ComputeNodeVMMDEpoch(ctx, node.ID); e.Predecessor != first || e.Incarnation != second {
		t.Fatalf("node epoch = %+v", e)
	}
	// Someone else advances the node: the next start is fenced but still
	// holds the new epoch, so the outsider's guests cannot be claimed twice.
	if _, err := s.ClaimComputeNodeVMMDEpoch(ctx, node.ID, 2, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	third := uuid.NewString()
	if got, err := claimOwnershipEpoch(ctx, j, s, node.ID, third, boot); !errors.Is(err, errAdoptionFenced) || got.Epoch != 4 {
		t.Fatalf("fenced claim = %+v, %v", got, err)
	}
	// A predecessor that wrote its intent and stopped before the database
	// claim is still explained.
	j.fail = fcvm.JournalEpochClaimed
	if _, err := claimOwnershipEpoch(ctx, j, s, node.ID, uuid.NewString(), boot); err == nil {
		t.Fatal("a failed epoch write was ignored")
	}
	j.fail = ""
	if _, err := claimOwnershipEpoch(ctx, j, s, node.ID, uuid.NewString(), boot); err != nil {
		t.Fatalf("claim after an interrupted predecessor: %v", err)
	}
	// A lost journal on a node that already had claims is fenced.
	if _, err := claimOwnershipEpoch(ctx, &memEpochJournal{}, s, node.ID, uuid.NewString(), boot); !errors.Is(err, errAdoptionFenced) {
		t.Fatalf("lost journal: %v", err)
	}
	// A journal from another node is fenced.
	foreign := &memEpochJournal{e: fcvm.JournalEpoch{NodeID: uuid.NewString(), Epoch: 7, BootID: boot, Incarnation: uuid.NewString(), State: fcvm.JournalEpochClaimed}, have: true}
	if _, err := claimOwnershipEpoch(ctx, foreign, s, node.ID, uuid.NewString(), boot); !errors.Is(err, errAdoptionFenced) {
		t.Fatalf("foreign journal: %v", err)
	}
}

func TestEpochExplained(t *testing.T) {
	node, inc := uuid.NewString(), uuid.NewString()
	claimed := fcvm.JournalEpoch{NodeID: node, Epoch: 5, Incarnation: inc, State: fcvm.JournalEpochClaimed}
	claiming := claimed
	claiming.State = fcvm.JournalEpochClaiming
	for _, tt := range []struct {
		name  string
		local fcvm.JournalEpoch
		have  bool
		db    state.VMMDEpoch
		want  bool
	}{
		{"first start", fcvm.JournalEpoch{}, false, state.VMMDEpoch{}, true},
		{"no journal, claimed node", fcvm.JournalEpoch{}, false, state.VMMDEpoch{Epoch: 3}, false},
		{"own claim", claimed, true, state.VMMDEpoch{Epoch: 5, Incarnation: inc}, true},
		{"claim by another incarnation", claimed, true, state.VMMDEpoch{Epoch: 5, Incarnation: uuid.NewString()}, false},
		{"node advanced", claimed, true, state.VMMDEpoch{Epoch: 6, Incarnation: uuid.NewString()}, false},
		{"intent never landed", claiming, true, state.VMMDEpoch{Epoch: 4}, true},
		{"intent landed", claiming, true, state.VMMDEpoch{Epoch: 5, Incarnation: inc}, true},
		{"intent overtaken", claiming, true, state.VMMDEpoch{Epoch: 5, Incarnation: uuid.NewString()}, false},
	} {
		if got := epochExplained(tt.local, tt.have, tt.db, node); got != tt.want {
			t.Errorf("%s: explained = %v, want %v", tt.name, got, tt.want)
		}
	}
}
