package gateway

// adr: 607

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

type confirmingWeightsStore struct {
	backend       *PGBackend
	firstRead     chan struct{}
	releaseRead   chan struct{}
	mu            sync.Mutex
	reads         int
	installed     []string
	observerError error
}

func (s *confirmingWeightsStore) LiveDeployments(ctx context.Context, _ string) ([]DeploymentWeightsRow, error) {
	s.mu.Lock()
	s.reads++
	n := s.reads
	s.mu.Unlock()
	if n == 1 && s.firstRead != nil {
		close(s.firstRead)
		select {
		case <-s.releaseRead:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	id := "old"
	if n > 1 {
		id = "new"
	}
	return []DeploymentWeightsRow{{ID: id, TrafficPercent: 100}}, nil
}

func (s *confirmingWeightsStore) DeploymentWeightsInstalled(_ context.Context, app string, rows []DeploymentWeightsRow) error {
	// This also proves receipt publication does not hold the routing cache lock.
	s.backend.tgtMu.RLock()
	picker := s.backend.appsPicker[app]
	installed := picker != nil && picker.weightsAuthoritative && len(picker.weights) == 1 && picker.weights[0].DeploymentID == rows[0].ID
	s.backend.tgtMu.RUnlock()
	if !installed {
		return errors.New("receipt before matching cache installation")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installed = append(s.installed, rows[0].ID)
	return s.observerError
}

func TestInstalledWeightsSerializesSnapshotInstallationAndReceipt(t *testing.T) {
	store := &confirmingWeightsStore{firstRead: make(chan struct{}), releaseRead: make(chan struct{})}
	backend := NewPGBackend(nil, nil, nil).WithStore(store)
	store.backend = backend
	first := make(chan error, 1)
	go func() { first <- backend.RefreshDeploymentWeights(t.Context(), "app") }()
	select {
	case <-store.firstRead:
	case <-time.After(3 * time.Second):
		t.Fatal("first refresh not started")
	}
	ctx, cancel := context.WithCancel(t.Context())
	blocked := make(chan error, 1)
	go func() { blocked <- backend.RefreshDeploymentWeights(ctx, "app") }()
	cancel()
	select {
	case err := <-blocked:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled refresh stuck behind snapshot")
	}
	second := make(chan error, 1)
	go func() { second <- backend.RefreshDeploymentWeights(t.Context(), "app") }()
	close(store.releaseRead)
	for _, done := range []chan error{first, second} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("refresh deadlocked")
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.reads != 2 || !slices.Equal(store.installed, []string{"old", "new"}) {
		t.Fatal("stale snapshot or cancelled read was acknowledged", store.reads, store.installed)
	}
	backend.tgtMu.RLock()
	defer backend.tgtMu.RUnlock()
	if backend.appsPicker["app"].weights[0].DeploymentID != "new" {
		t.Fatal("older refresh overwrote newer cache")
	}
}

func TestInstalledWeightsPublicationFailureDoesNotUndoInstalledCache(t *testing.T) {
	sentinel := errors.New("synthetic receipt outage")
	store := &confirmingWeightsStore{observerError: sentinel}
	backend := NewPGBackend(nil, nil, nil).WithStore(store)
	store.backend = backend
	if err := backend.RefreshDeploymentWeights(t.Context(), "app"); !errors.Is(err, sentinel) {
		t.Fatal("receipt failure hidden", err)
	}
	store.observerError = nil
	if err := backend.RefreshDeploymentWeights(t.Context(), "app"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(store.installed, []string{"old", "new"}) {
		t.Fatal(store.installed)
	}
}
