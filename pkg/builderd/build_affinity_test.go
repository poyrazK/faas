package builderd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type affinityClaimSpy struct {
	*state.MemStore
	targeted bool
	polled   bool
}

func (s *affinityClaimSpy) ClaimQueuedBuildWithNodeAffinity(_ context.Context, _, nodeID string, affinityGrace time.Duration) (state.Build, error) {
	s.targeted = nodeID == "node-a" && affinityGrace == 5*time.Second
	return state.Build{}, state.ErrNotFound
}

func (s *affinityClaimSpy) ClaimNextQueuedBuildWithNodeAffinity(_ context.Context, nodeID string, affinityGrace, fairnessWindow time.Duration) (state.Build, error) {
	s.polled = nodeID == "node-a" && affinityGrace == 5*time.Second && fairnessWindow == 30*time.Second
	return state.Build{}, state.ErrNotFound
}

func TestBuildClaimsUseNodeAffinityOnBothIngressPaths(t *testing.T) {
	store := &affinityClaimSpy{MemStore: state.NewMemStore()}
	b := New(store, nil, nil, nil, nil, nil, Config{
		BuilderNodeID:      "node-a",
		CacheAffinityGrace: 5 * time.Second,
		FairnessWindow:     30 * time.Second,
	}, nil)

	if _, err := b.ProcessOne(context.Background(), "build-id"); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if !store.targeted {
		t.Fatal("ProcessOne did not use the affinity-aware targeted claim")
	}
	if _, err := b.ProcessNext(context.Background()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("ProcessNext error = %v, want ErrNotFound", err)
	}
	if !store.polled {
		t.Fatal("ProcessNext did not use the affinity-aware polling claim")
	}
}
