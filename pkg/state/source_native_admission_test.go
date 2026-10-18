package state

// adr: 435. Portable authority tests use simulated receipts, never physical ACKs.

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/buildpublisher"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type sourceNativeTestStore interface {
	sourceRuntimeTestStore
	nativeArtifactTestStore
}

func sourceNativeFixture(t *testing.T, s sourceNativeTestStore, runtime string, scan bool) (sourceRootfsFixture, Instance, runtimeadmission.Binding, InstanceApplicationStandardAdmission) {
	t.Helper()
	f := sourceBuildRootfsFixtureRuntime(t, s, runtime)
	if _, err := s.PublishSourceBuildRootfs(t.Context(), f.Input); err != nil {
		t.Fatal(err)
	}
	f.App = manageNativeArtifactApp(t, s, f.App)
	var err error
	f.Dep, err = s.DeploymentByID(t.Context(), f.Dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if scan {
		inputs, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.PublishDeploymentRuntimeScan(t.Context(), runtimeScanInputFixture(t, inputs)); err != nil {
			t.Fatal(err)
		}
	}
	ins, b := nativeArtifactAttempt(t, s, f.App, f.Dep)
	capture, err := s.GetInstanceApplicationStandardAdmission(t.Context(), ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	b = sourceNativeBinding(t, s, b, capture)
	return f, ins, b, capture
}

func sourceNativeBinding(t *testing.T, s sourceNativeTestStore, b runtimeadmission.Binding, capture InstanceApplicationStandardAdmission) runtimeadmission.Binding {
	t.Helper()
	b.ProtocolVersion = runtimeadmission.ArtifactProtocolVersion
	b.Incarnation = uuid.NewString()
	var err error
	b.ArtifactSourcesHash, err = standardCapturedArtifactSourceHash(capture)
	if err != nil {
		t.Fatal(err)
	}
	registerConsumedNativeIdentity(t, s, b, runtimeadmission.ArtifactProtocolVersion)
	return b
}

func sourceNativeAdmission(t *testing.T, s sourceNativeTestStore, runtime string) {
	t.Helper()
	f, ins, b, capture := sourceNativeFixture(t, s, runtime, true)
	if !capture.Managed || len(capture.RuntimeArtifacts) != 2 || capture.RuntimeArtifacts[1].Kind != f.Input.Kind || capture.RuntimeArtifacts[0].StorageKey == capture.RuntimeArtifacts[1].StorageKey {
		t.Fatal("source identity was flattened or cast to registry")
	}
	grant, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, b)
	if err != nil || grant.ExpiresAtUnixNano > f.Parent.ExpiresAt.UnixNano() {
		t.Fatal("source native authority lacked current publisher lease", err)
	}
	retry, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, b)
	if err != nil || retry != grant {
		t.Fatal("source retry renewed grant", err)
	}
	receipt := consumedNativeReceipt(grant, capture)
	bad := receipt.Clone()
	bad.ArtifactConsumption.Drives[1].Source.Kind = "app-layer"
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, bad); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("registry cast receipt retained source authority", err)
	}
	assertNativeBootUnpublished(t, s, ins)
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, receipt); err != nil {
		t.Fatal("simulated exact source receipt rejected", err)
	}
	assertStandardCaptureHistory(t, s, capture)
	e, err := s.GetApplicationStandardEnrollment(t.Context(), f.App.OrgID, f.App.ID)
	if err != nil || e.ObservedRevision != 0 {
		t.Fatal("simulated receipt fabricated policy observation", err)
	}
}

func sourceNativeMissingScan(t *testing.T, s sourceNativeTestStore) {
	t.Helper()
	_, ins, b, _ := sourceNativeFixture(t, s, "node22", false)
	if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, b); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("source conversion alone authorized native boot", err)
	}
	assertNativeBootUnpublished(t, s, ins)
}

func sourceNativeRefusesCapabilityDowngrade(t *testing.T, s sourceNativeTestStore) {
	t.Helper()
	_, ins, b, _ := sourceNativeFixture(t, s, "node22", true)
	b.ProtocolVersion, b.ArtifactSourcesHash = runtimeadmission.ProtocolVersion, ""
	b.Incarnation = uuid.NewString()
	registerConsumedNativeIdentity(t, s, b, runtimeadmission.ProtocolVersion)
	if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, b); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("source producer admitted a consumer without byte capability", err)
	}
	assertNativeBootUnpublished(t, s, ins)
}

func sourceNativeRenewal(t *testing.T, s sourceNativeTestStore) {
	t.Helper()
	f, ins, b, before := sourceNativeFixture(t, s, "node22", true)
	if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, b); err != nil {
		t.Fatal(err)
	}
	in := f.Parent.Input
	in.ID = uuid.NewString()
	if _, err := s.RecordBuildExportPublication(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	_, after := createRuntimeArtifactCapture(t, s, f.App, f.Dep)
	if before.InputHash != after.InputHash || before.NativeInputHash != after.NativeInputHash || before.ArtifactInputHash != after.ArtifactInputHash || !reflect.DeepEqual(before.RuntimeArtifacts, after.RuntimeArtifacts) {
		t.Fatal("fresh same-claim approval changed source capture")
	}
	if err := s.DeleteAppTrustedSigner(t.Context(), f.App.AccountID, f.App.ID, "company"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, b); err == nil {
		t.Fatal("source revocation retained a pending boot grant")
	}
	assertNativeBootUnpublished(t, s, ins)
}

func sourceNativeLateChange(t *testing.T, s sourceNativeTestStore, mode string) {
	t.Helper()
	f, ins, b, capture := sourceNativeFixture(t, s, "node22", true)
	grant, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, b)
	if err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "command":
		command := "/changed/start"
		_, err = s.UpdateApp(t.Context(), f.App.ID, UpdateAppParams{StartCommand: &command})
	case "base":
		in := f.Base.Input
		in.ID = uuid.NewString()
		_, err = s.PublishBaseImageProducer(t.Context(), in)
	case "build":
		_, err = s.CreateBuild(t.Context(), f.Dep.ID, f.Dep.Kind, 1, "")
	case "metadata":
		err = s.SetDeploymentRootfs(t.Context(), f.Dep.ID, "/other.ext4", "other.ext4", 1)
	case "publisher":
		err = s.DeleteAppTrustedSigner(t.Context(), f.App.AccountID, f.App.ID, "company")
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, b); !errors.Is(err, ErrApplicationStandardRuntimeStale) && !errors.Is(err, buildpublisher.ErrInvalid) {
		t.Fatal("changed source inputs retained boot authority", mode, err)
	}
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, consumedNativeReceipt(grant, capture)); err == nil {
		t.Fatal("late source change published runtime", mode)
	}
	assertNativeBootUnpublished(t, s, ins)
}

func TestMemSourceNativeAdmission(t *testing.T) {
	for _, runtime := range []string{"", "node22"} {
		t.Run("runtime="+runtime, func(t *testing.T) { sourceNativeAdmission(t, NewMemStore(), runtime) })
	}
}
func TestMemSourceNativeMissingScan(t *testing.T) { sourceNativeMissingScan(t, NewMemStore()) }
func TestMemSourceNativeRenewal(t *testing.T)     { sourceNativeRenewal(t, NewMemStore()) }
func TestMemSourceNativeCapabilityDowngrade(t *testing.T) {
	sourceNativeRefusesCapabilityDowngrade(t, NewMemStore())
}
func TestMemSourceNativeLateChange(t *testing.T) {
	for _, mode := range []string{"command", "base", "build", "metadata", "publisher"} {
		t.Run(mode, func(t *testing.T) { sourceNativeLateChange(t, NewMemStore(), mode) })
	}
}

func TestMemSourceNativeExpiredApprovalCaptureIsHistorical(t *testing.T) {
	s := NewMemStore()
	f, ins, b, before := sourceNativeFixture(t, s, "node22", true)
	s.mu.Lock()
	proof := s.buildExportPublications[f.Parent.ID]
	proof.ExpiresAt = time.Now().Add(-time.Hour)
	proof.VerifiedAt = proof.ExpiresAt.Add(-24 * time.Hour)
	s.buildExportPublications[proof.ID] = proof
	root := s.sourceBuildRootfs[f.Input.ID]
	root.PublishedAt, root.ExpiresAt = proof.VerifiedAt.Add(time.Minute), proof.ExpiresAt
	s.sourceBuildRootfs[root.ID] = root
	s.mu.Unlock()
	_, after := createRuntimeArtifactCapture(t, s, f.App, f.Dep)
	if before.ArtifactInputHash != after.ArtifactInputHash {
		t.Fatal("expiry rewrote immutable capture")
	}
	if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, b); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("historical capture granted expired authority", err)
	}
}
