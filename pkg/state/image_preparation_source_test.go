package state

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/buildpublisher"
)

type sourcePreparationTestStore interface {
	sourceRootfsTestStore
	DeploymentImagePreparationStore
	SourceImagePreparationStore
}

func sourceImagePreparationContract(t *testing.T, s sourcePreparationTestStore, runtime, outcome string) {
	t.Helper()
	f := sourceBuildRootfsFixtureRuntime(t, s, runtime)
	p, err := s.BeginImagePreparation(t.Context(), f.Dep.ID, "source-node")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.TransitionImagePreparation(t.Context(), f.Dep.ID, p.ClaimToken, DeployImaging); err != nil {
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
		if err := s.UpdateDeploymentStatus(t.Context(), f.Dep.ID, DeployCancelled, ""); err != nil {
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
	if outcome == "stale claim" && !errors.Is(err, ErrConflict) || outcome == "revoked publisher" && !errors.Is(err, buildpublisher.ErrInvalid) {
		t.Fatal("refused for an unrelated reason", err)
	}
	assertSourcePreparationUnpublished(t, s, f)
}

func assertSourceImageCheckpoint(t *testing.T, s sourcePreparationTestStore, f sourceRootfsFixture, value SourceBuildRootfs) {
	t.Helper()
	dep, err := s.DeploymentByID(t.Context(), f.Dep.ID)
	if err != nil || !sourceBuildRootfsMetadataMatches(value, dep) {
		t.Fatal("source producer metadata was not published atomically", err)
	}
	p, err := s.BeginImagePreparation(t.Context(), f.Dep.ID, "source-node")
	if err != nil || p.Phase != ImageLayerPublished || p.InputPath != f.Dep.RootfsPath || p.InputKey != f.Dep.RootfsKey {
		t.Fatal("restart lost checkpoint or original source input", err)
	}
	if _, err := s.PublishSourceImagePreparationLayer(t.Context(), f.Input, p.ClaimToken); !errors.Is(err, ErrConflict) {
		t.Fatal("completed conversion was republished", err)
	}
	current, err := s.GetCurrentSourceBuildRootfs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID)
	if err != nil || current.ID != value.ID || !current.PublishedAt.Equal(value.PublishedAt) {
		t.Fatal("restart changed retained source evidence", err)
	}
}

func assertSourcePreparationUnpublished(t *testing.T, s sourcePreparationTestStore, f sourceRootfsFixture) {
	t.Helper()
	dep, err := s.DeploymentByID(t.Context(), f.Dep.ID)
	if err != nil || dep.RootfsPath != f.Dep.RootfsPath || dep.RootfsKey != f.Dep.RootfsKey || dep.RootfsBytes != f.Dep.RootfsBytes {
		t.Fatal("refusal partially stamped metadata", err)
	}
	if _, err := s.GetCurrentSourceBuildRootfs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("refusal retained a selected producer", err)
	}
}

func TestSourceImagePreparationAtomicCheckpoint(t *testing.T) {
	for _, runtime := range []string{"", "node22"} {
		for _, outcome := range []string{"complete", "stale claim", "revoked publisher", "cancelled"} {
			t.Run(runtime+"/"+outcome, func(t *testing.T) {
				sourceImagePreparationContract(t, NewMemStore(), runtime, outcome)
			})
		}
	}
}
