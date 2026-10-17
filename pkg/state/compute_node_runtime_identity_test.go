package state

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type runtimeIncarnationTestStore interface {
	ComputeNodeRuntimeIdentityStore
	CreateComputeNode(context.Context, ComputeNode) (ComputeNode, error)
	DeleteComputeNode(context.Context, string) error
}

func runtimeIncarnationNode(t *testing.T, s runtimeIncarnationTestStore) ComputeNode {
	t.Helper()
	n, err := s.CreateComputeNode(t.Context(), ComputeNode{Name: "native-lineage-" + uuid.NewString(), TargetURL: "unix:///tmp/native-lineage", VPCPUs: 2, MemMB: 1024, MaxConcurrency: 5, AdmissionCeilingMB: 1024, VCPUBudget: 2, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMemRuntimeIncarnationHistory(t *testing.T) {
	s := NewMemStore()
	runtimeIncarnationLifecycle(t, s, func(n string) runtimeadmission.Identity {
		s.mu.Lock()
		defer s.mu.Unlock()
		return runtimeadmission.Identity{NodeID: n, Incarnation: s.computeNodeRuntimeIncarnations[n], ProtocolVersion: s.computeNodeRuntimeProtocols[n]}
	})
	if len(s.computeNodeRuntimeHistory) != 0 {
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
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, changed); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
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
			if i == 0 && err != nil || i != 0 && err != nil && !errors.Is(err, ErrApplicationStandardRuntimeStale) {
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
		if err := s.RegisterComputeNodeRuntimeIdentity(ctx, old); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
			t.Fatalf("superseded startup reactivated: %v", err)
		}
	}
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, b); err != nil {
		t.Fatalf("current startup retry failed: %v", err)
	}
	unknown := b
	unknown.NodeID = uuid.NewString()
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, unknown); !errors.Is(err, ErrNotFound) {
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
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, b); !errors.Is(err, ErrNotFound) {
		t.Fatalf("erased node registered: %v", err)
	}
}

func TestMemRuntimeIncarnationRestartCannotReanimateGrant(t *testing.T) {
	runtimeIncarnationGrantReplay(t, NewMemStore())
}

// Storage fixtures simulate receipts; they do not establish native acceptance.
func runtimeIncarnationGrantReplay(t *testing.T, s nativeArtifactTestStore) {
	t.Helper()
	ins, receipt := issueConsumedNativeFixture(t, s)
	ctx := t.Context()
	old := receipt.Binding
	restarted := runtimeadmission.Identity{NodeID: old.NodeID, Incarnation: uuid.NewString(), ProtocolVersion: runtimeadmission.ProtocolVersion}
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, restarted); err != nil {
		t.Fatal(err)
	}
	original := runtimeadmission.Identity{NodeID: old.NodeID, Incarnation: old.Incarnation, ProtocolVersion: old.ProtocolVersion}
	for range 3 {
		if err := s.RegisterComputeNodeRuntimeIdentity(ctx, original); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
			t.Fatalf("old registration reanimated grant: %v", err)
		}
		if _, err := s.PublishInstanceApplicationStandardRuntime(ctx, ins.State, StateRunning, receipt); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
			t.Fatalf("superseded receipt published: %v", err)
		}
		assertNativeBootUnpublished(t, s, ins)
	}
	restarted.Incarnation = uuid.NewString()
	restarted.ProtocolVersion = runtimeadmission.ArtifactProtocolVersion
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, restarted); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, original); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("later restart restored earlier authority: %v", err)
	}
	if _, err := s.PublishInstanceApplicationStandardRuntime(ctx, ins.State, StateRunning, receipt); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("later restart restored old grant: %v", err)
	}
	assertNativeBootUnpublished(t, s, ins)
}

func TestMemRuntimeIncarnationPublishedReceiptRemainsFenced(t *testing.T) {
	runtimeIncarnationPublishedReceipt(t, NewMemStore())
}

func runtimeIncarnationPublishedReceipt(t *testing.T, s interface {
	nativeArtifactTestStore
	InstanceApplicationStandardRuntimeReceiptStore
}) {
	t.Helper()
	ctx := t.Context()
	ins, r := issueConsumedNativeFixture(t, s)
	if _, err := s.PublishInstanceApplicationStandardRuntime(ctx, ins.State, StateRunning, r); err != nil {
		t.Fatal(err)
	}
	if current, err := s.GetInstanceApplicationStandardRuntimeReceipt(ctx, ins.ID); err != nil || !current.Equal(r) {
		t.Fatalf("current simulated receipt unavailable: %+v %v", current, err)
	}
	newer := runtimeadmission.Identity{NodeID: r.Binding.NodeID, Incarnation: uuid.NewString(), ProtocolVersion: runtimeadmission.ArtifactProtocolVersion}
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetInstanceApplicationStandardRuntimeReceipt(ctx, ins.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("restart retained old residency authority: %v", err)
	}
	old := runtimeadmission.Identity{NodeID: r.Binding.NodeID, Incarnation: r.Binding.Incarnation, ProtocolVersion: r.Binding.ProtocolVersion}
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, old); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("old process reactivated published authority: %v", err)
	}
	if _, err := s.GetInstanceApplicationStandardRuntimeReceipt(ctx, ins.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("replayed startup restored receipt: %v", err)
	}
}
