package state_test

// adr: 435. Retained presence prevents legacy fallback without reading scans.

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeProducerPresenceTestStore = state.ContractTestStore

func runtimeProducerPresence(t *testing.T, s runtimeProducerPresenceTestStore) {
	t.Helper()
	proof, app, dep := state.RegistryVerificationFixture(t, s, false)
	if _, err := s.RecordDeploymentRegistryVerification(t.Context(), proof); err != nil {
		t.Fatal(err)
	}
	present, err := s.HasDeploymentRuntimeProducers(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || present {
		t.Fatal("publisher verification alone invented producer presence", present, err)
	}
	_, _, app, dep = state.ArtifactScanBaseFixture(t, s)
	present, err = s.HasDeploymentRuntimeProducers(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || !present {
		t.Fatal("unscanned retained producer appeared absent", present, err)
	}
	if _, err := s.GetFreshDeploymentArtifactScanEvidence(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
		t.Fatal("presence synthesized component scans", err)
	}
	if err := s.SetDeploymentRootfs(t.Context(), dep.ID, "/changed.ext4", "apps/changed.ext4", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), app.AccountID, app.ID, dep.ID, ""); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("fixture retained a valid current producer", err)
	}
	present, err = s.HasDeploymentRuntimeProducers(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || !present {
		t.Fatal("metadata drift erased retained producer presence", present, err)
	}
	for i := range 3 {
		ids := []string{app.AccountID, app.ID, dep.ID}
		ids[i] = uuid.NewString()
		present, err = s.HasDeploymentRuntimeProducers(t.Context(), ids[0], ids[1], ids[2])
		if present || !errors.Is(err, state.ErrNotFound) {
			t.Fatal("presence escaped owner scope", i, present, err)
		}
	}
	if present, err := s.HasDeploymentRuntimeProducers(t.Context(), "invalid", app.ID, dep.ID); present || !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("malformed presence scope accepted", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if present, err := s.HasDeploymentRuntimeProducers(ctx, app.AccountID, app.ID, dep.ID); present || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled presence read succeeded", err)
	}
}

func TestMemRuntimeProducerPresenceIncludesStaleHistory(t *testing.T) {
	runtimeProducerPresence(t, state.NewMemStore())
}
