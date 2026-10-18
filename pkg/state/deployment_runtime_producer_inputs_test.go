package state

// adr: 435. Real producer stores, without fabricated component scan approval.

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/imagepublisher"
)

type runtimeProducerInputTestStore interface {
	runtimeArtifactInputTestStore
	DeploymentRuntimeProducerInputStore
}

func runtimeProducerInputsLifecycle(t *testing.T, s runtimeProducerInputTestStore) {
	t.Helper()
	in, base, app, dep := artifactScanBaseFixture(t, s)
	before, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || len(before.Artifacts) != 2 || before.Artifacts[0].ProducerID != base.ID || before.Artifacts[1].ProducerID != in.RootfsProducerID || before.InputHash == "" || !before.ExpiresAt.After(before.CheckedAt) {
		t.Fatalf("scanner could not bootstrap an explicit two-drive producer set: %v", err)
	}
	if _, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("bootstrap conferred component approval: %v", err)
	}
	if _, err := s.GetCurrentDeploymentArtifactScan(t.Context(), app.AccountID, app.ID, dep.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bootstrap fabricated a component scan: %v", err)
	}
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishBaseImageScan(t.Context(), runtimeArtifactBaseScanInput(in, base)); err != nil {
		t.Fatal(err)
	}
	approved, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || approved.InputHash != before.InputHash || !reflect.DeepEqual(approved.Artifacts, before.Artifacts) {
		t.Fatalf("bootstrap changed the immutable producer identity: %v", err)
	}
	root, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), app.AccountID, app.ID, dep.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	origin, err := s.GetDeploymentRegistryVerificationByID(t.Context(), app.AccountID, app.ID, dep.ID, root.Input.RegistryVerificationID)
	if err != nil || !before.ExpiresAt.Equal(origin.ExpiresAt) {
		t.Fatalf("bootstrap did not retain signature expiry: %v", err)
	}
	origin.Input.ID = uuid.NewString()
	proof, err := s.RecordDeploymentRegistryVerification(t.Context(), origin.Input)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || after.InputHash != before.InputHash || !after.ExpiresAt.Equal(proof.ExpiresAt) || !reflect.DeepEqual(after.Artifacts, before.Artifacts) {
		t.Fatalf("current signature renewal changed producer bytes or lost its deadline: %v", err)
	}
	after.Artifacts[0].StorageKey = "returned mutation"
	again, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || again.InputHash != before.InputHash || again.Artifacts[0].StorageKey == "returned mutation" {
		t.Fatalf("bootstrap aliased private producer state: %v", err)
	}
	failed := in
	failed.ID, failed.Status, failed.Report, failed.ScannerName, failed.Failure = uuid.NewString(), "failed", nil, "", "scanner_unavailable"
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), failed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("failed component retained approval: %v", err)
	}
	if got, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID); err != nil || got.InputHash != before.InputHash {
		t.Fatalf("failed component prevented bounded rescanning: %v", err)
	}
	if _, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), uuid.NewString(), app.ID, dep.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bootstrap crossed account scope: %v", err)
	}
	_, _, other := registryVerificationFixture(t, s, false)
	if _, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bootstrap disclosed another application's deployment: %v", err)
	}
	if _, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), app.AccountID, app.ID, other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("component approval disclosed another application's deployment: %v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.GetFreshDeploymentRuntimeProducerInputs(cancelled, app.AccountID, app.ID, dep.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled bootstrap returned evidence: %v", err)
	}
	if err := s.DeleteAppTrustedSigner(t.Context(), app.AccountID, app.ID, "company"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, imagepublisher.ErrSignatureInvalid) {
		t.Fatalf("revoked publisher still permitted bootstrap: %v", err)
	}
}

func runtimeProducerInputsRejectIncomplete(t *testing.T, s runtimeProducerInputTestStore) {
	t.Helper()
	_, app, dep := registryVerificationFixture(t, s, false)
	if _, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrDeploymentArtifactScanEvidenceAbsent) {
		t.Fatalf("clean absence was confused with retained incomplete lineage: %v", err)
	}
	_, _, app, dep = artifactScanFixture(t, s, false)
	if _, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("full-rootfs invented an unbound runtime-default base: %v", err)
	}
	_, _, app, dep = artifactScanFixture(t, s, true)
	if _, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("sidecar alone supplied a main producer: %v", err)
	}
	_, base, app, dep := artifactScanBaseFixture(t, s)
	if err := s.SetDeploymentRootfs(t.Context(), dep.ID, "/changed.ext4", "apps/changed.ext4", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("stale producer metadata became bootstrap or legacy: %v", err)
	}
	_, base, app, dep = artifactScanBaseFixture(t, s)
	replaced := base.Input
	replaced.ID = uuid.NewString()
	if _, err := s.PublishBaseImageProducer(t.Context(), replaced); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("replaced base retained a complete producer set: %v", err)
	}
}

func runtimeProducerInputsReplacement(t *testing.T, s runtimeProducerInputTestStore) {
	t.Helper()
	in, _, app, dep := artifactScanBaseFixture(t, s)
	before, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	root, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), app.AccountID, app.ID, dep.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	replacement := root.Input
	replacement.ID, replacement.ArtifactDigest = uuid.NewString(), imagechain.Digest([]byte("different same-sized producer"))
	newRoot, err := s.PublishDeploymentRegistryRootfs(t.Context(), replacement)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || after.InputHash == before.InputHash || after.Artifacts[1].ProducerID != newRoot.ID || after.Artifacts[1].Bytes != in.ArtifactBytes {
		t.Fatalf("same-sized replacement retained a scanner input identity: %v", err)
	}
}

func TestMemRuntimeProducerInputsLifecycle(t *testing.T) {
	runtimeProducerInputsLifecycle(t, NewMemStore())
}
func TestMemRuntimeProducerInputsRejectIncomplete(t *testing.T) {
	runtimeProducerInputsRejectIncomplete(t, NewMemStore())
}
func TestMemRuntimeProducerInputsReplacement(t *testing.T) {
	runtimeProducerInputsReplacement(t, NewMemStore())
}

func TestRuntimeProducerInputsRefuseExpiryAtFinalCut(t *testing.T) {
	s := NewMemStore()
	_, _, app, dep := artifactScanBaseFixture(t, s)
	dep, err := s.DeploymentByID(t.Context(), dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	identity, parents, err := s.runtimeProducerSetLocked(app, dep, time.Now().UTC())
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := finishRuntimeProducerInputs(identity, parents, parents[0].ExpiresAt); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("signature expiring at final storage cut returned evidence: %v", err)
	}
}
