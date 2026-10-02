package state

// Simulated consumption tests durable authority; native KVM acceptance is separate.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type standardSnapshotTestStore interface {
	nativeArtifactTestStore
	ApplicationStandardSnapshotCaptureStore
}

func standardSnapshotFixture(t *testing.T, s standardSnapshotTestStore, mode string) (Instance, ApplicationStandardSnapshotCaptureRequest) {
	t.Helper()
	ins, r := issueConsumedNativeFixture(t, s)
	ins, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, r)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	prefix := strings.TrimSuffix(SnapshotCaptureMemKey(r.Binding.DeploymentID, mode, token), "mem")
	if mode == "park" {
		if err := s.UpdateInstanceStateWithTimestamp(t.Context(), ins.ID, string(StateSnapshotting), time.Now()); err != nil {
			t.Fatal(err)
		}
		ins.State = string(StateSnapshotting)
	}
	return ins, ApplicationStandardSnapshotCaptureRequest{Token: token, InstanceID: ins.ID, MemoryKey: prefix + "mem", VMStateKey: prefix + "vmstate", PrivateDriveKey: prefix + "drive", FCVersion: "1.12.1", Mode: mode, SourceStartedAtUnixNano: ins.StartedAt.UnixNano()}
}

func standardSnapshotAck(g runtimeadmission.SnapshotGrant) runtimeadmission.SnapshotAcknowledgment {
	now := time.Now().UnixNano()
	mainBytes := int64(0)
	for _, d := range g.Parent.ArtifactConsumption.Drives {
		if d.Source.Role() == "main" {
			mainBytes = d.InjectedBytes
		}
	}
	artifact := func(key string, bytes int64) runtimeadmission.CapturedArtifact {
		return runtimeadmission.CapturedArtifact{StorageKey: key, Digest: "sha256:" + strings.Repeat("e", 64), Bytes: bytes}
	}
	c := runtimeadmission.SnapshotCapture{Version: runtimeadmission.SnapshotCaptureVersion, Parent: g.Parent.Clone(), Memory: artifact(g.MemoryKey, 16384), VMState: artifact(g.VMStateKey, 4096), PrivateDrive: artifact(g.PrivateDriveKey, mainBytes), CapturedAtUnixNano: now}
	return runtimeadmission.SnapshotAcknowledgment{Grant: g.Clone(), Capture: c, CompletedAtUnixNano: now}
}

func standardSnapshotGet(t *testing.T, s ApplicationStandardSnapshotCaptureStore, g runtimeadmission.SnapshotGrant) ApplicationStandardSnapshotCaptureRecord {
	t.Helper()
	b := g.Parent.Binding
	r, err := s.GetApplicationStandardSnapshotCapture(t.Context(), b.AccountID, b.AppID, b.DeploymentID, g.Token)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func standardSnapshotLifecycle(t *testing.T, s standardSnapshotTestStore, mode string) {
	t.Helper()
	ins, req := standardSnapshotFixture(t, s, mode)
	g, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, req)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, req)
	if err != nil || !retry.Equal(g) {
		t.Fatalf("grant retry changed authority: %v", err)
	}
	changed := req
	changed.FCVersion = "other"
	if _, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("token reused for different capture: %v", err)
	}
	if standardSnapshotGet(t, s, g).Acknowledgment != nil {
		t.Fatal("grant invented captured bytes")
	}
	b := g.Parent.Binding
	for _, scope := range [][3]string{{uuid.NewString(), b.AppID, b.DeploymentID}, {b.AccountID, uuid.NewString(), b.DeploymentID}, {b.AccountID, b.AppID, uuid.NewString()}} {
		if _, err := s.GetApplicationStandardSnapshotCapture(t.Context(), scope[0], scope[1], scope[2], g.Token); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-scope history disclosed: %v", err)
		}
	}
	a := standardSnapshotAck(g)
	bad := a.Clone()
	bad.Capture.PrivateDrive.Bytes++
	if err := s.PublishApplicationStandardSnapshotCapture(t.Context(), bad); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("different main bytes accepted: %v", err)
	}
	if standardSnapshotGet(t, s, g).Acknowledgment != nil {
		t.Fatal("refused receipt partially published")
	}
	if err := s.PublishApplicationStandardSnapshotCapture(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishApplicationStandardSnapshotCapture(t.Context(), a); err != nil {
		t.Fatal("exact acknowledgment retry refused", err)
	}
	want := a.Clone()
	a.Grant.Parent.ArtifactConsumption.Drives[0].DriveID = "caller-edit"
	a.Capture.Parent.ArtifactConsumption.Drives[0].DriveID = "caller-edit"
	saved := standardSnapshotGet(t, s, g)
	if saved.Acknowledgment == nil || !saved.Acknowledgment.Equal(want) {
		t.Fatal("saved acknowledgment aliases caller memory")
	}
	saved.Acknowledgment.Capture.Parent.ArtifactConsumption.Drives[0].DriveID = "reader-edit"
	if !standardSnapshotGet(t, s, g).Acknowledgment.Equal(want) {
		t.Fatal("history read exposed owned slices")
	}
	if err := s.UpdateInstanceStateToTerminal(t.Context(), ins.ID, string(StateStopped), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteInstance(t.Context(), ins.ID); err != nil {
		t.Fatal(err)
	}
	if !standardSnapshotGet(t, s, g).Acknowledgment.Equal(want) {
		t.Fatal("instance cleanup erased lineage")
	}
	if err := s.PublishApplicationStandardSnapshotCapture(t.Context(), want); err != nil {
		t.Fatal("lost reply cannot recover committed history", err)
	}
	if _, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, req); err == nil {
		t.Fatal("history issued fresh capture authority after source deletion")
	}
	if _, err := s.ScheduleAppDeletion(t.Context(), ins.AppID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimAppDeletion(t.Context(), ins.AppID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAppPermanently(t.Context(), ins.AppID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetApplicationStandardSnapshotCapture(t.Context(), b.AccountID, b.AppID, b.DeploymentID, g.Token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner erasure retained catalog: %v", err)
	}
}

func standardSnapshotRestart(t *testing.T, s standardSnapshotTestStore) {
	t.Helper()
	ins, req := standardSnapshotFixture(t, s, "warm")
	g, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, req)
	if err != nil {
		t.Fatal(err)
	}
	restarted := g.Parent.Binding
	restarted.Incarnation = uuid.NewString()
	registerConsumedNativeIdentity(t, s, restarted, runtimeadmission.ArtifactProtocolVersion)
	if err := s.PublishApplicationStandardSnapshotCapture(t.Context(), standardSnapshotAck(g)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("restarted process published preceding capture: %v", err)
	}
	if _, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, req); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("restart renewed old residency authority: %v", err)
	}
	if standardSnapshotGet(t, s, g).Acknowledgment != nil {
		t.Fatal("restart refusal partially published")
	}
}

func standardSnapshotPolicyChange(t *testing.T, s standardSnapshotTestStore) {
	t.Helper()
	ins, req := standardSnapshotFixture(t, s, "warm")
	g, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, req)
	if err != nil {
		t.Fatal(err)
	}
	policy := api.AppSecurityPolicyEnforce
	if _, err := s.UpdateApp(t.Context(), ins.AppID, UpdateAppParams{SetSecurityPolicy: true, SecurityPolicy: &policy}); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishApplicationStandardSnapshotCapture(t.Context(), standardSnapshotAck(g)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("policy change retained capture approval: %v", err)
	}
	if standardSnapshotGet(t, s, g).Acknowledgment != nil {
		t.Fatal("stale intent partially published")
	}
}

func TestMemStandardSnapshotWarmLifecycle(t *testing.T) {
	standardSnapshotLifecycle(t, NewMemStore(), "warm")
}
func TestMemStandardSnapshotParkLifecycle(t *testing.T) {
	standardSnapshotLifecycle(t, NewMemStore(), "park")
}
func TestMemStandardSnapshotProcessRestart(t *testing.T) { standardSnapshotRestart(t, NewMemStore()) }
func TestMemStandardSnapshotPolicyChange(t *testing.T) {
	standardSnapshotPolicyChange(t, NewMemStore())
}

func standardSnapshotRenewedApproval(t *testing.T, s standardSnapshotTestStore) {
	t.Helper()
	in, base, app, dep := artifactScanBaseFixture(t, s)
	app = manageNativeArtifactApp(t, s, app)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	b := runtimeArtifactBaseScanInput(in, base)
	deadline := time.Now().UTC().Truncate(time.Microsecond).Add(2 * time.Second)
	b.Report.ScannerDBBuiltAt = deadline.Add(-api.ApplicationStandardScannerDBMaxAge).Format(time.RFC3339Nano)
	if _, err := s.PublishBaseImageScan(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	ins, candidate := nativeArtifactAttempt(t, s, app, dep)
	capture, err := s.GetInstanceApplicationStandardAdmission(t.Context(), ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	candidate.ProtocolVersion = runtimeadmission.ArtifactProtocolVersion
	candidate.ArtifactSourcesHash, err = standardCapturedArtifactSourceHash(capture)
	if err != nil {
		t.Fatal(err)
	}
	registerConsumedNativeIdentity(t, s, candidate, runtimeadmission.ArtifactProtocolVersion)
	boot, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate)
	if err != nil {
		t.Fatal(err)
	}
	ins, err = s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, consumedNativeReceipt(boot, capture))
	if err != nil {
		t.Fatal(err)
	}
	req := ApplicationStandardSnapshotCaptureRequest{Token: uuid.NewString(), InstanceID: ins.ID, FCVersion: "1.12.1", Mode: "warm", SourceStartedAtUnixNano: ins.StartedAt.UnixNano()}
	setKeys := func() {
		prefix := strings.TrimSuffix(SnapshotCaptureMemKey(boot.DeploymentID, "warm", req.Token), "mem")
		req.MemoryKey, req.VMStateKey, req.PrivateDriveKey = prefix+"mem", prefix+"vmstate", prefix+"drive"
	}
	setKeys()
	g, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, req)
	if err != nil || g.ExpiresAtUnixNano != deadline.UnixNano() {
		t.Fatalf("capture outlived scanner database: %v", err)
	}
	ack := standardSnapshotAck(g)
	time.Sleep(time.Until(deadline) + 20*time.Millisecond)
	if err := s.PublishApplicationStandardSnapshotCapture(t.Context(), ack); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("expired approval published capture: %v", err)
	}
	if _, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, req); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("resident historical boot substituted for fresh approval: %v", err)
	}
	b.ID, b.Report.ScannerDBBuiltAt = uuid.NewString(), time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := s.PublishBaseImageScan(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, req); !errors.Is(err, ErrConflict) {
		t.Fatalf("renewal resurrected expired namespace: %v", err)
	}
	req.Token = uuid.NewString()
	setKeys()
	fresh, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, req)
	if err != nil || fresh.ExpiresAtUnixNano <= g.ExpiresAtUnixNano || fresh.Parent.Binding.Validate(time.Now()) == nil {
		t.Fatalf("fresh approval failed against expired historical parent: %v", err)
	}
	if err := s.PublishApplicationStandardSnapshotCapture(t.Context(), standardSnapshotAck(fresh)); err != nil {
		t.Fatal(err)
	}
}

func TestMemStandardSnapshotRenewedApproval(t *testing.T) {
	standardSnapshotRenewedApproval(t, NewMemStore())
}
