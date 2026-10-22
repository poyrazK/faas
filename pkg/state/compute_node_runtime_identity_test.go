package state_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeIncarnationTestStore interface {
	state.ComputeNodeRuntimeIdentityStore
	CreateComputeNode(context.Context, state.ComputeNode) (state.ComputeNode, error)
	DeleteComputeNode(context.Context, string) error
}

func runtimeIncarnationNode(t *testing.T, s runtimeIncarnationTestStore) state.ComputeNode {
	t.Helper()
	n, err := s.CreateComputeNode(t.Context(), state.ComputeNode{Name: "native-lineage-" + uuid.NewString(), TargetURL: "unix:///tmp/native-lineage", VPCPUs: 2, MemMB: 1024, MaxConcurrency: 5, AdmissionCeilingMB: 1024, VCPUBudget: 2, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMemRuntimeIncarnationHistory(t *testing.T) {
	s := state.NewMemStore()
	runtimeIncarnationLifecycle(t, s, func(n string) runtimeadmission.Identity {
		(*s.Mu()).Lock()
		defer (*s.Mu()).Unlock()
		return runtimeadmission.Identity{NodeID: n, Incarnation: (*s.ComputeNodeRuntimeIncarnations())[n], ProtocolVersion: (*s.ComputeNodeRuntimeProtocols())[n]}
	})
	if len((*s.ComputeNodeRuntimeHistory())) != 0 {
		t.Fatal("node erasure retained startup history")
	}
}

func runtimeIncarnationLifecycle(t *testing.T, s runtimeIncarnationTestStore, current func(string) runtimeadmission.Identity) {
	t.Helper()
	n := runtimeIncarnationNode(t, s)
	ctx := t.Context()
	a := runtimeadmission.Identity{NodeID: n.ID, Incarnation: uuid.NewString(), ProtocolVersion: runtimeadmission.ProtocolVersion}
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, a); err != nil {
		t.Fatalf("current retry: %v", err)
	}
	changed := a
	changed.ProtocolVersion = runtimeadmission.ArtifactProtocolVersion
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, changed); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
		t.Fatalf("startup protocol changed: %v", err)
	}
	b := changed
	b.Incarnation = uuid.NewString()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			want := a
			if i == 0 {
				want = b
			}
			err := s.RegisterComputeNodeRuntimeIdentity(ctx, want)
			if i == 0 && err != nil || i != 0 && err != nil && !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent registration: %v", err)
	}
	if got := current(n.ID); got != b {
		t.Fatalf("late registration replaced current startup: %+v", got)
	}
	for _, old := range []runtimeadmission.Identity{a, changed} {
		if err := s.RegisterComputeNodeRuntimeIdentity(ctx, old); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
			t.Fatalf("superseded startup reactivated: %v", err)
		}
	}
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, b); err != nil {
		t.Fatalf("current startup retry failed: %v", err)
	}
	unknown := b
	unknown.NodeID = uuid.NewString()
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, unknown); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign node registered: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	c := b
	c.Incarnation = uuid.NewString()
	if err := s.RegisterComputeNodeRuntimeIdentity(canceled, c); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled mutation: %v", err)
	}
	if current(n.ID) != b {
		t.Fatal("refusal changed current startup")
	}
	if err := s.DeleteComputeNode(ctx, n.ID); err != nil {
		t.Fatalf("unused node erasure: %v", err)
	}
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, b); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("erased node registered: %v", err)
	}
}

func TestMemRuntimeIncarnationRestartCannotReanimateGrant(t *testing.T) {
	runtimeIncarnationGrantReplay(t, state.NewMemStore())
}

// Storage fixtures simulate receipts; they do not establish native acceptance.
func runtimeIncarnationGrantReplay(t *testing.T, s state.NativeArtifactTestStore) {
	t.Helper()
	ins, receipt := state.IssueConsumedNativeFixture(t, s)
	ctx := t.Context()
	old := receipt.Binding
	restarted := runtimeadmission.Identity{NodeID: old.NodeID, Incarnation: uuid.NewString(), ProtocolVersion: runtimeadmission.ProtocolVersion}
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, restarted); err != nil {
		t.Fatal(err)
	}
	original := runtimeadmission.Identity{NodeID: old.NodeID, Incarnation: old.Incarnation, ProtocolVersion: old.ProtocolVersion}
	for range 3 {
		if err := s.RegisterComputeNodeRuntimeIdentity(ctx, original); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
			t.Fatalf("old registration reanimated grant: %v", err)
		}
		if _, err := s.PublishInstanceApplicationStandardRuntime(ctx, ins.State, state.StateRunning, receipt); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
			t.Fatalf("superseded receipt published: %v", err)
		}
		state.AssertNativeBootUnpublished(t, s, ins)
	}
	restarted.Incarnation = uuid.NewString()
	restarted.ProtocolVersion = runtimeadmission.ArtifactProtocolVersion
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, restarted); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, original); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
		t.Fatalf("later restart restored earlier authority: %v", err)
	}
	if _, err := s.PublishInstanceApplicationStandardRuntime(ctx, ins.State, state.StateRunning, receipt); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
		t.Fatalf("later restart restored old grant: %v", err)
	}
	state.AssertNativeBootUnpublished(t, s, ins)
}

func TestMemRuntimeIncarnationPublishedReceiptRemainsFenced(t *testing.T) {
	runtimeIncarnationPublishedReceipt(t, state.NewMemStore())
}

func runtimeIncarnationPublishedReceipt(t *testing.T, s state.ContractTestStore) {
	t.Helper()
	ctx := t.Context()
	ins, r := state.IssueConsumedNativeFixture(t, s)
	if _, err := s.PublishInstanceApplicationStandardRuntime(ctx, ins.State, state.StateRunning, r); err != nil {
		t.Fatal(err)
	}
	if current, err := s.GetInstanceApplicationStandardRuntimeReceipt(ctx, ins.ID); err != nil || !current.Equal(r) {
		t.Fatalf("current simulated receipt unavailable: %+v %v", current, err)
	}
	newer := runtimeadmission.Identity{NodeID: r.Binding.NodeID, Incarnation: uuid.NewString(), ProtocolVersion: runtimeadmission.ArtifactProtocolVersion}
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetInstanceApplicationStandardRuntimeReceipt(ctx, ins.ID); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
		t.Fatalf("restart retained old residency authority: %v", err)
	}
	old := runtimeadmission.Identity{NodeID: r.Binding.NodeID, Incarnation: r.Binding.Incarnation, ProtocolVersion: r.Binding.ProtocolVersion}
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, old); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
		t.Fatalf("old process reactivated published authority: %v", err)
	}
	if _, err := s.GetInstanceApplicationStandardRuntimeReceipt(ctx, ins.ID); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
		t.Fatalf("replayed startup restored receipt: %v", err)
	}
}
