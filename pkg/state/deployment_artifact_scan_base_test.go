package state

// adr: 435

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
)

type artifactScanBaseTestStore interface {
	artifactScanTestStore
	BaseImageProducerStore
}

func artifactScanBaseFixture(t *testing.T, s artifactScanBaseTestStore) (DeploymentArtifactScanInput, BaseImageProducer, App, Deployment) {
	return artifactScanBaseFixtureWithSidecar(t, s, false)
}

func artifactScanBaseFixtureWithSidecar(t *testing.T, s artifactScanBaseTestStore, sidecar bool) (DeploymentArtifactScanInput, BaseImageProducer, App, Deployment) {
	t.Helper()
	baseInput := baseProducerFixture(t, "base/scan-parent.ext4", "parent")
	base, err := s.PublishBaseImageProducer(t.Context(), baseInput)
	if err != nil {
		t.Fatal(err)
	}
	full := baseProducerFixture(t, "base/unpublished-scan.ext4", "parent", "app")
	verification, app, dep := registryVerificationFixtureWithChain(t, s, sidecar, full.ImageChain)
	if sidecar {
		proof, err := s.RecordDeploymentRegistryVerification(t.Context(), verification)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.PublishDeploymentRegistryRootfs(t.Context(), DeploymentRegistryRootfsInput{ID: uuid.NewString(),
			RegistryVerificationID: proof.ID, RegistryInputHash: proof.InputHash, AccountID: app.AccountID, OrgID: app.OrgID,
			AppID: app.ID, DeploymentID: dep.ID, WorkloadName: "metrics", Scope: dep.Scope, Kind: "sidecar-layer",
			StorageKey: "sidecars/" + dep.ID + "/metrics.ext4", ArtifactDigest: imagechain.Digest([]byte("sidecar output")),
			ArtifactBytes: 8192, ContentBytes: 8, Layers: full.Layers}); err != nil {
			t.Fatal(err)
		}
		verification.ID, verification.WorkloadName, verification.ImageReference = uuid.NewString(), "", dep.ImageDigest
		verification.SourceReference = "registry.example/team/service@" + verification.Proof.SubjectDigest
		verification.SelectedReference = "registry.example/team/service@" + verification.SelectedDigest
	}
	signed, err := s.RecordDeploymentRegistryVerification(t.Context(), verification)
	if err != nil {
		t.Fatal(err)
	}
	digest := imagechain.Digest([]byte("app output"))
	root, err := s.PublishDeploymentRegistryRootfs(t.Context(), DeploymentRegistryRootfsInput{ID: uuid.NewString(), RegistryVerificationID: signed.ID, RegistryInputHash: signed.InputHash,
		AccountID: app.AccountID, OrgID: app.OrgID, AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Kind: "app-layer", StorageKey: "apps/" + app.Slug + "/" + dep.ID + ".ext4", RootfsPath: "/srv/" + dep.ID + ".ext4", ArtifactDigest: digest, ArtifactBytes: 10, ContentBytes: 8, LayerStart: 1, Layers: full.Layers[1:], BaseProducerID: base.ID, BaseInputHash: base.InputHash})
	if err != nil {
		t.Fatal(err)
	}
	report := &api.ScanResult{ImageDigest: dep.ImageDigest, ArtifactDigest: digest, ScannerVersion: "0.116.0", ScannerDBStatus: "valid", ScannerDBVersion: "v6.0.2", ScannerDBBuiltAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), Vulnerabilities: []api.Vulnerability{}}
	in := DeploymentArtifactScanInput{ID: uuid.NewString(), RootfsProducerID: root.ID, RootfsInputHash: root.InputHash, AccountID: app.AccountID, OrgID: app.OrgID, AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, ImageReference: dep.ImageDigest, ArtifactDigest: digest, ArtifactBytes: 10, Status: "complete", ScannerName: "grype", Report: report}
	return in, base, app, dep
}

func artifactScanBaseBinding(t *testing.T, s artifactScanBaseTestStore) {
	in, base, app, dep := artifactScanBaseFixture(t, s)
	value, err := s.PublishDeploymentArtifactScan(t.Context(), in)
	if err != nil {
		t.Fatalf("bound two-drive component scan refused: %v", err)
	}
	baseInput := base.Input
	baseInput.ID = uuid.NewString()
	if _, err := s.PublishBaseImageProducer(t.Context(), baseInput); err != nil {
		t.Fatal(err)
	}
	in.ID = uuid.NewString()
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("replaced base renewed component scan: %v", err)
	}
	got, err := s.GetCurrentDeploymentArtifactScan(t.Context(), app.AccountID, app.ID, dep.ID, "")
	if err != nil || got.ID != value.ID || !got.ScannedAt.Equal(value.ScannedAt) {
		t.Fatalf("failed renewal changed historical scan: %v", err)
	}
}

func TestMemArtifactScanBaseBinding(t *testing.T) { artifactScanBaseBinding(t, NewMemStore()) }
