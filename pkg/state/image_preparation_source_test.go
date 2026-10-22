package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/buildpublisher"
	"github.com/onebox-faas/faas/pkg/state"
)

type sourcePreparationTestStore = state.ContractTestStore

func sourceImagePreparationContract(t *testing.T, s sourcePreparationTestStore, runtime, outcome string) {
	t.Helper()
	f := state.SourceBuildRootfsFixtureRuntime(t, s, runtime)
	p, err := s.BeginImagePreparation(t.Context(), f.Dep.ID, "source-node")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.TransitionImagePreparation(t.Context(), f.Dep.ID, p.ClaimToken, state.DeployImaging); err != nil {
		t.Fatal(err)
	}
	token := p.ClaimToken
	switch outcome {
	case "stale claim":
		token = uuid.NewString()
	case "revoked publisher":
		if err := s.DeleteAppTrustedSigner(t.Context(), f.App.AccountID, f.App.ID, "company"); err != nil {
			t.Fatal(err)
		}
	case "cancelled":
		if err := s.UpdateDeploymentStatus(t.Context(), f.Dep.ID, state.DeployCancelled, ""); err != nil {
			t.Fatal(err)
		}
	}
	value, err := s.PublishSourceImagePreparationLayer(t.Context(), f.Input, token)
	if outcome == "complete" {
		if err != nil {
			t.Fatal(err)
		}
		assertSourceImageCheckpoint(t, s, f, value)
		return
	}
	if err == nil {
		t.Fatal("rejected preparation was published")
	}
	if outcome == "stale claim" && !errors.Is(err, state.ErrConflict) || outcome == "revoked publisher" && !errors.Is(err, buildpublisher.ErrInvalid) {
		t.Fatal("refused for an unrelated reason", err)
	}
	assertSourcePreparationUnpublished(t, s, f)
}

func assertSourceImageCheckpoint(t *testing.T, s sourcePreparationTestStore, f state.SourceRootfsFixture, value state.SourceBuildRootfs) {
	t.Helper()
	dep, err := s.DeploymentByID(t.Context(), f.Dep.ID)
	if err != nil || !state.SourceBuildRootfsMetadataMatches(value, dep) {
		t.Fatal("source producer metadata was not published atomically", err)
	}
	p, err := s.BeginImagePreparation(t.Context(), f.Dep.ID, "source-node")
	if err != nil || p.Phase != state.ImageLayerPublished || p.InputPath != f.Dep.RootfsPath || p.InputKey != f.Dep.RootfsKey {
		t.Fatal("restart lost checkpoint or original source input", err)
	}
	if _, err := s.PublishSourceImagePreparationLayer(t.Context(), f.Input, p.ClaimToken); !errors.Is(err, state.ErrConflict) {
		t.Fatal("completed conversion was republished", err)
	}
	current, err := s.GetCurrentSourceBuildRootfs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID)
	if err != nil || current.ID != value.ID || !current.PublishedAt.Equal(value.PublishedAt) {
		t.Fatal("restart changed retained source evidence", err)
	}
}

func assertSourcePreparationUnpublished(t *testing.T, s sourcePreparationTestStore, f state.SourceRootfsFixture) {
	t.Helper()
	dep, err := s.DeploymentByID(t.Context(), f.Dep.ID)
	if err != nil || dep.RootfsPath != f.Dep.RootfsPath || dep.RootfsKey != f.Dep.RootfsKey || dep.RootfsBytes != f.Dep.RootfsBytes {
		t.Fatal("refusal partially stamped metadata", err)
	}
	if _, err := s.GetCurrentSourceBuildRootfs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("refusal retained a selected producer", err)
	}
}

func TestSourceImagePreparationAtomicCheckpoint(t *testing.T) {
	for _, runtime := range []string{"", "node22"} {
		for _, outcome := range []string{"complete", "stale claim", "revoked publisher", "cancelled"} {
			t.Run(runtime+"/"+outcome, func(t *testing.T) {
				sourceImagePreparationContract(t, state.NewMemStore(), runtime, outcome)
			})
		}
	}
}
