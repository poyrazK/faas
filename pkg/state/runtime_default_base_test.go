package state

// adr: 435. Stored byte/report/receipt fixtures are not native acceptance.

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
)

func runtimeDefaultBaseFixture(t *testing.T, s nativeArtifactTestStore) (DeploymentRegistryRootfs, BaseImageProducer, App, Deployment) {
	t.Helper()
	image := baseProducerFixture(t, "base/unpublished-full.ext4", "independent application")
	proof, app, dep := registryVerificationFixtureWithChain(t, s, false, image.ImageChain)
	signed, err := s.RecordDeploymentRegistryVerification(t.Context(), proof)
	if err != nil {
		t.Fatal(err)
	}
	base, err := s.PublishBaseImageProducer(t.Context(), baseProducerFixture(t, RuntimeBaseKeyForArch(app.Runtime, "amd64"), "runtime default"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := s.PublishDeploymentRegistryRootfs(t.Context(), DeploymentRegistryRootfsInput{
		ID: uuid.NewString(), RegistryVerificationID: signed.ID, RegistryInputHash: signed.InputHash,
		AccountID: app.AccountID, OrgID: app.OrgID, AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope,
		Kind: "full-rootfs", StorageKey: "apps/" + dep.ID + ".ext4", RootfsPath: "/srv/" + dep.ID + ".ext4",
		ArtifactDigest: imagechain.Digest([]byte("full application ext4")), ArtifactBytes: 8192, ContentBytes: 8,
		Layers: image.Layers, BaseProducerID: base.ID, BaseInputHash: base.InputHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, err = s.DeploymentByID(t.Context(), dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	return root, base, app, dep
}

func runtimeDefaultBaseLifecycle(t *testing.T, s nativeArtifactTestStore) {
	t.Helper()
	root, base, app, dep := runtimeDefaultBaseFixture(t, s)
	inputs, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || len(inputs.Artifacts) != 2 || inputs.Artifacts[0].ProducerID != base.ID || inputs.Artifacts[1].Kind != "full-rootfs" || inputs.Artifacts[1].BaseProducerID != base.ID {
		t.Fatal("full-rootfs lost its separate default drive", err)
	}
	in := runtimeScanInputFixture(t, inputs)
	first, err := s.PublishDeploymentRuntimeScan(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, dep.ID); err != nil {
		t.Fatal(err)
	}
	replacement := base.Input
	replacement.ID = uuid.NewString()
	if _, err := s.PublishBaseImageProducer(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("replaced default drive retained scan inputs", err)
	}
	if _, err := s.GetFreshDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("replaced default drive retained approval", err)
	}
	in.ID = uuid.NewString()
	if _, err := s.PublishDeploymentRuntimeScan(t.Context(), in); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("old default drive renewed approval", err)
	}
	history, err := s.GetCurrentDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || history.ID != first.ID || !history.ExpiresAt.Equal(first.ExpiresAt) {
		t.Fatal("failed renewal changed history", err)
	}
	retained, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), app.AccountID, app.ID, dep.ID, "")
	if err != nil || retained.ID != root.ID || retained.Input.BaseInputHash != base.InputHash {
		t.Fatal("default-base replacement rewrote conversion", err)
	}
}

func runtimeDefaultBaseRefusals(t *testing.T, newStore func(*testing.T) nativeArtifactTestStore) {
	t.Helper()
	for _, mode := range []string{"wrong hash", "wrong runtime key", "missing guest init", "suffix masquerading as full", "sidecar binding"} {
		t.Run(mode, func(t *testing.T) {
			s := newStore(t)
			root, base, app, dep := runtimeDefaultBaseFixture(t, s)
			in, expected := runtimeDefaultBaseRejectedInput(t, s, root, base, mode)
			if _, err := s.PublishDeploymentRegistryRootfs(t.Context(), in); !errors.Is(err, expected) {
				t.Fatal("invalid default binding published", err)
			}
			actual, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), app.AccountID, app.ID, dep.ID, "")
			if err != nil || actual.ID != root.ID {
				t.Fatal("refused binding changed selection", err)
			}
		})
	}
}

func runtimeDefaultBaseRejectedInput(t *testing.T, s nativeArtifactTestStore, root DeploymentRegistryRootfs, base BaseImageProducer, mode string) (DeploymentRegistryRootfsInput, error) {
	t.Helper()
	in := root.Input
	in.ID = uuid.NewString()
	expected := ErrApplicationStandardRuntimeStale
	switch mode {
	case "wrong hash":
		in.BaseInputHash = strings.Repeat("0", 64)
	case "wrong runtime key", "missing guest init":
		other := base.Input
		other.ID = uuid.NewString()
		if mode == "wrong runtime key" {
			other.Artifact.StorageKey = "base/other.ext4"
		} else {
			other.GuestInitDigest = ""
		}
		value, err := s.PublishBaseImageProducer(t.Context(), other)
		if err != nil {
			t.Fatal(err)
		}
		in.BaseProducerID, in.BaseInputHash = value.ID, value.InputHash
	case "suffix masquerading as full":
		in.LayerStart, in.Layers = 1, nil
	case "sidecar binding":
		in.Kind, in.WorkloadName, in.RootfsPath = "sidecar-layer", "metrics", ""
		expected = ErrInvalidArgument
	}
	return in, expected
}

func runtimeDefaultBaseNativeAuthority(t *testing.T, s nativeArtifactTestStore) {
	t.Helper()
	_, base, app, dep := runtimeDefaultBaseFixture(t, s)
	app = manageNativeArtifactApp(t, s, app)
	value := publishCleanRuntimeDefaultScan(t, s, app, dep)
	var err error
	policy := api.AppSecurityPolicyEnforce
	app, err = s.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetSecurityPolicy: true, SecurityPolicy: &policy})
	if err != nil {
		t.Fatal(err)
	}
	ins, candidate := nativeArtifactAttempt(t, s, app, dep)
	capture, err := s.GetInstanceApplicationStandardAdmission(t.Context(), ins.ID)
	if err != nil || len(capture.RuntimeArtifacts) != 2 || capture.RuntimeArtifacts[0].ProducerID != base.ID || capture.RuntimeArtifacts[1].Kind != "full-rootfs" {
		t.Fatal("native capture omitted a physical source", err)
	}
	grant, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate)
	if err != nil || grant.ExpiresAtUnixNano != value.ExpiresAt.UnixNano() {
		t.Fatal("full-rootfs composed authority refused", err)
	}
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, nativeArtifactReceipt(grant, false)); err != nil {
		t.Fatal(err)
	}
	e, err := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
	if err != nil || e.ObservedRevision != 0 {
		t.Fatal("simulated byte receipt fabricated adoption", err)
	}
}

func publishCleanRuntimeDefaultScan(t *testing.T, s nativeArtifactTestStore, app App, dep Deployment) DeploymentRuntimeScan {
	t.Helper()
	inputs, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := runtimeScanInputFixture(t, inputs)
	for i := range in.Reports {
		in.Reports[i].Report.Vulnerabilities, in.Reports[i].Report.SeverityCounts = []api.Vulnerability{}, api.SeverityCounts{}
	}
	value, err := s.PublishDeploymentRuntimeScan(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestMemRuntimeDefaultBaseLifecycle(t *testing.T) { runtimeDefaultBaseLifecycle(t, NewMemStore()) }
func TestMemRuntimeDefaultBaseRefusals(t *testing.T) {
	runtimeDefaultBaseRefusals(t, func(*testing.T) nativeArtifactTestStore { return NewMemStore() })
}
func TestMemRuntimeDefaultBaseNativeAuthority(t *testing.T) {
	runtimeDefaultBaseNativeAuthority(t, NewMemStore())
}
