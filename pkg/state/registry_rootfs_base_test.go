package state

// adr: 393

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/imagechain"
)

type registryBaseTestStore interface {
	registryRootfsTestStore
	BaseImageProducerStore
}

func registryRootfsBaseBinding(t *testing.T, s registryBaseTestStore) {
	inBase := baseProducerFixture(t, "base/bound.ext4", "parent")
	base, err := s.PublishBaseImageProducer(t.Context(), inBase)
	if err != nil {
		t.Fatal(err)
	}
	full := baseProducerFixture(t, "base/unpublished.ext4", "parent", "app")
	verification, app, dep := registryVerificationFixtureWithChain(t, s, false, full.ImageChain)
	signed, err := s.RecordDeploymentRegistryVerification(t.Context(), verification)
	if err != nil {
		t.Fatal(err)
	}
	appArtifact := []byte("actual app output")
	in := DeploymentRegistryRootfsInput{ID: uuid.NewString(), RegistryVerificationID: signed.ID, RegistryInputHash: signed.InputHash, AccountID: app.AccountID, OrgID: app.OrgID, AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Kind: "app-layer", StorageKey: "apps/" + app.Slug + "/" + dep.ID + ".ext4", RootfsPath: "/srv/apps/" + dep.ID + ".ext4", ArtifactDigest: imagechain.Digest(appArtifact), ArtifactBytes: int64(len(appArtifact)), ContentBytes: 8, LayerStart: 1, Layers: full.Layers[1:], BaseProducerID: base.ID, BaseInputHash: base.InputHash}
	for _, mode := range []string{"wrong base hash", "wrong prefix", "wrong start"} {
		t.Run(mode, func(t *testing.T) {
			bad := in
			bad.ID = uuid.NewString()
			switch mode {
			case "wrong base hash":
				bad.BaseInputHash = imagechain.Digest([]byte("other"))[7:]
			case "wrong prefix":
				other, err := s.PublishBaseImageProducer(t.Context(), baseProducerFixture(t, "base/unrelated.ext4", "other"))
				if err != nil {
					t.Fatal(err)
				}
				bad.BaseProducerID, bad.BaseInputHash = other.ID, other.InputHash
			case "wrong start":
				bad.LayerStart = 0
				bad.Layers = full.Layers
			}
			if _, err := s.PublishDeploymentRegistryRootfs(t.Context(), bad); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
				t.Fatalf("unbound base accepted: %v", err)
			}
			if _, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), app.AccountID, app.ID, dep.ID, ""); !errors.Is(err, ErrNotFound) {
				t.Fatalf("failed attempt published: %v", err)
			}
		})
	}
	produced, err := s.PublishDeploymentRegistryRootfs(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if produced.Input.BaseProducerID != base.ID || produced.Input.BaseInputHash != base.InputHash {
		t.Fatal("base identity lost")
	}
	replacement := inBase
	replacement.ID = uuid.NewString()
	if _, err := s.PublishBaseImageProducer(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}
	changed := in
	changed.ID = uuid.NewString()
	if _, err := s.PublishDeploymentRegistryRootfs(t.Context(), changed); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("superseded base acquired new producer: %v", err)
	}
	historical, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), app.AccountID, app.ID, dep.ID, "")
	if err != nil || historical.ID != produced.ID {
		t.Fatalf("history lost or failed attempt changed selection: %v", err)
	}
}
func TestMemRegistryRootfsBaseBinding(t *testing.T) { registryRootfsBaseBinding(t, NewMemStore()) }
