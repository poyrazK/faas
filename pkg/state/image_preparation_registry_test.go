package state

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/imagepublisher"
)

type registryPreparationTestStore interface {
	registryRootfsTestStore
	DeploymentImagePreparationStore
	RegistryImagePreparationStore
}

func registryImagePreparationContract(t *testing.T, s registryPreparationTestStore, kind, outcome string) {
	t.Helper()
	in, _, app, dep := registryRootfsFixture(t, s, false)
	in.Kind = kind
	p, err := s.BeginImagePreparation(t.Context(), dep.ID, "registry-node")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.TransitionImagePreparation(t.Context(), dep.ID, p.ClaimToken, DeployImaging); err != nil {
		t.Fatal(err)
	}
	token := p.ClaimToken
	if outcome == "stale claim" {
		token = uuid.NewString()
	}
	if outcome == "revoked publisher" {
		if err := s.DeleteAppTrustedSigner(t.Context(), app.AccountID, app.ID, "company"); err != nil {
			t.Fatal(err)
		}
	}
	value, err := s.PublishRegistryImagePreparationLayer(t.Context(), in, token)
	if outcome == "complete" {
		if err != nil {
			t.Fatal(err)
		}
		assertRegistryImageCheckpoint(t, s, in, dep, value)
		return
	}
	if outcome == "stale claim" && !errors.Is(err, ErrConflict) || outcome == "revoked publisher" && !errors.Is(err, imagepublisher.ErrSignatureInvalid) {
		t.Fatal("refused for an unrelated reason", err)
	}
	got, err := s.DeploymentByID(t.Context(), dep.ID)
	if err != nil || got.RootfsPath != dep.RootfsPath || got.RootfsKey != dep.RootfsKey || got.RootfsBytes != dep.RootfsBytes {
		t.Fatal("refusal partially stamped metadata", err)
	}
	if _, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), app.AccountID, app.ID, dep.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("refusal retained selected registry evidence", err)
	}
}

func assertRegistryImageCheckpoint(t *testing.T, s registryPreparationTestStore, in DeploymentRegistryRootfsInput, dep Deployment, value DeploymentRegistryRootfs) {
	t.Helper()
	got, err := s.DeploymentByID(t.Context(), dep.ID)
	if err != nil || got.RootfsKey != in.StorageKey || got.RootfsBytes != in.ContentBytes {
		t.Fatal("verified rootfs stamp missing", err)
	}
	p, err := s.BeginImagePreparation(t.Context(), dep.ID, "registry-node")
	if err != nil || p.Phase != ImageLayerPublished || p.InputPath != dep.ImageDigest {
		t.Fatal("restart lost original registry input or checkpoint", err)
	}
	if _, err := s.PublishRegistryImagePreparationLayer(t.Context(), in, p.ClaimToken); !errors.Is(err, ErrConflict) {
		t.Fatal("completed registry conversion was republished", err)
	}
	current, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), in.AccountID, in.AppID, in.DeploymentID, "")
	if err != nil || current.ID != value.ID || !current.PublishedAt.Equal(value.PublishedAt) {
		t.Fatal("restart changed registry evidence", err)
	}
}

func TestRegistryImagePreparationAtomicCheckpoint(t *testing.T) {
	for _, kind := range []string{"app-layer", "full-rootfs"} {
		for _, outcome := range []string{"complete", "stale claim", "revoked publisher"} {
			t.Run(kind+"/"+outcome, func(t *testing.T) {
				registryImagePreparationContract(t, NewMemStore(), kind, outcome)
			})
		}
	}
}
