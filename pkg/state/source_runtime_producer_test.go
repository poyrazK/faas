package state

// adr: 435. Portable signed fixtures do not prove native mounting or Grype.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/buildpublisher"
)

type sourceRuntimeTestStore interface {
	sourceRootfsTestStore
	DeploymentRuntimeScanStore
	DeploymentRuntimeArtifactInputStore
	BuildExportPublicationHistoryStore
}

func sourceRuntimeFixture(t *testing.T, s sourceRuntimeTestStore) (sourceRootfsFixture, SourceBuildRootfs, DeploymentRuntimeProducerInputs) {
	t.Helper()
	f := sourceBuildRootfsFixture(t, s)
	root, err := s.PublishSourceBuildRootfs(t.Context(), f.Input)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	return f, root, inputs
}

func sourceRuntimeRenewal(t *testing.T, s sourceRuntimeTestStore) {
	t.Helper()
	f, root, before := sourceRuntimeFixture(t, s)
	if len(before.Artifacts) != 2 || before.Artifacts[0].ProducerID != f.Base.ID || before.Artifacts[1] != runtimeArtifactFromSourceRootfs(root) || !before.ExpiresAt.Equal(f.Parent.ExpiresAt) {
		t.Fatal("source/bootstrap lineage lost")
	}
	retained, err := s.GetLatestBuildExportPublication(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID, f.Build.ID)
	if err != nil {
		t.Fatal(err)
	}
	retained.Input.ID = uuid.NewString()
	proof, err := s.RecordBuildExportPublication(t.Context(), retained.Input)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID)
	if err != nil || before.InputHash != after.InputHash || !reflect.DeepEqual(before.Artifacts, after.Artifacts) || !after.ExpiresAt.Equal(proof.ExpiresAt) {
		t.Fatal("same-claim renewal changed source identity", err)
	}
	history, err := s.GetCurrentSourceBuildRootfs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID)
	if err != nil || !reflect.DeepEqual(root, history) {
		t.Fatal("renewal rewrote conversion", err)
	}
	if _, err := s.GetLatestBuildExportPublication(t.Context(), uuid.NewString(), f.App.ID, f.Dep.ID, f.Build.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("history crossed owner", err)
	}
	if err := s.DeleteAppTrustedSigner(t.Context(), f.App.AccountID, f.App.ID, "company"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID); !errors.Is(err, buildpublisher.ErrInvalid) {
		t.Fatal("revocation retained source approval", err)
	}
	if _, err := s.GetLatestBuildExportPublication(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID, f.Build.ID); err != nil {
		t.Fatal("revocation erased retained proof", err)
	}
}

func sourceRuntimeScanLifecycle(t *testing.T, s sourceRuntimeTestStore) {
	t.Helper()
	f, _, inputs := sourceRuntimeFixture(t, s)
	if _, err := s.GetFreshDeploymentRuntimeScan(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("bootstrap fabricated scan", err)
	}
	value, err := s.PublishDeploymentRuntimeScan(t.Context(), runtimeScanInputFixture(t, inputs))
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := s.GetFreshDeploymentRuntimeScan(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID)
	if err != nil || fresh.Scan.ID != value.ID || fresh.ExpiresAt.After(inputs.ExpiresAt) {
		t.Fatal("source scan lost producer binding", err)
	}
	if _, err := s.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("scan fabricated component/native approval", err)
	}
	command := "/changed/start"
	if _, err := s.UpdateApp(t.Context(), f.App.ID, UpdateAppParams{StartCommand: &command}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentRuntimeScan(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("changed intent retained fresh scan", err)
	}
	value.Input.ID = uuid.NewString()
	if _, err := s.PublishDeploymentRuntimeScan(t.Context(), value.Input); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("late intent changed scan publication", err)
	}
	if old, err := s.GetCurrentDeploymentRuntimeScan(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID); err != nil || old.ID != value.ID {
		t.Fatal("failed publication changed history", err)
	}
}

func sourceRuntimePublisherReplacement(t *testing.T, s sourceRuntimeTestStore) {
	t.Helper()
	f, root, before := sourceRuntimeFixture(t, s)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpsertAppTrustedSigner(t.Context(), f.App.AccountID, f.App.ID, "company", der, f.App.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID); !errors.Is(err, buildpublisher.ErrInvalid) {
		t.Fatal("old key retained approval", err)
	}
	in := f.Parent.Input
	in.ID = uuid.NewString()
	in.Proof, err = buildpublisher.Sign(t.Context(), in.Claims, "company", key)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := s.RecordBuildExportPublication(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID)
	if err != nil || after.InputHash != before.InputHash || !after.ExpiresAt.Equal(proof.ExpiresAt) || after.Artifacts[1].ProducerID != root.ID {
		t.Fatal("current publisher could not renew same export", err)
	}
}

func sourceRuntimeInvalidation(t *testing.T, s sourceRuntimeTestStore, mode string) {
	t.Helper()
	f, _, _ := sourceRuntimeFixture(t, s)
	switch mode {
	case "base":
		in := f.Base.Input
		in.ID = uuid.NewString()
		if _, err := s.PublishBaseImageProducer(t.Context(), in); err != nil {
			t.Fatal(err)
		}
	case "build":
		if _, err := s.CreateBuild(t.Context(), f.Dep.ID, f.Dep.Kind, 1, ""); err != nil {
			t.Fatal(err)
		}
	case "metadata":
		if err := s.SetDeploymentRootfs(t.Context(), f.Dep.ID, "/other.ext4", "other.ext4", 1); err != nil {
			t.Fatal(err)
		}
	case "manifest":
		manifest := f.App.Manifest
		manifest.Env = map[string]string{"CHANGED": "yes"}
		if _, err := s.UpdateApp(t.Context(), f.App.ID, UpdateAppParams{Manifest: &manifest}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("changed source input approved", mode, err)
	}
	if present, err := s.HasDeploymentRuntimeProducers(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID); err != nil || !present {
		t.Fatal("stale source permitted fallback", err)
	}
}

func TestMemSourceRuntimeRenewal(t *testing.T)       { sourceRuntimeRenewal(t, NewMemStore()) }
func TestMemSourceRuntimeScanLifecycle(t *testing.T) { sourceRuntimeScanLifecycle(t, NewMemStore()) }
func TestMemSourceRuntimePublisherReplacement(t *testing.T) {
	sourceRuntimePublisherReplacement(t, NewMemStore())
}
func TestMemSourceRuntimeInvalidation(t *testing.T) {
	for _, mode := range []string{"base", "build", "metadata", "manifest"} {
		t.Run(mode, func(t *testing.T) { sourceRuntimeInvalidation(t, NewMemStore(), mode) })
	}
}

func TestMemSourceRuntimeNativeCaptureRetainsSourceIdentity(t *testing.T) {
	s := NewMemStore()
	f, _, inputs := sourceRuntimeFixture(t, s)
	var err error
	f.Dep, err = s.DeploymentByID(t.Context(), f.Dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if identity, err := s.runtimeArtifactIdentityLocked(f.App, f.Dep); err != nil || identity == nil || !reflect.DeepEqual(*identity, inputs.deploymentRuntimeArtifactIdentity) {
		t.Fatal("source capture lost distinct producer identity", err)
	}
}
