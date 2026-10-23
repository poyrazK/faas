//go:build linux || darwin

// adr: 20261022184951 — runtime park/warm captures bind their own durable start.
package fcvm

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

const runtimeCaptureBootID = "d3124d9a-657b-4f37-a9df-f5dfb2e50764"

func runtimeCaptureAuthorityFixture(t *testing.T, tier string) nativeRuntimeCaptureAuthority {
	t.Helper()
	lease := leaseForSlot("runtime-capture", 4)
	lease.Plan = api.PlanHobby
	deployment := uuid.NewString()
	now := time.Now().UTC()
	return nativeRuntimeCaptureAuthority{Version: 1, InstanceID: lease.Instance, DeploymentID: deployment,
		NativeGeneration: uuid.NewString(), KernelBootID: runtimeCaptureBootID, NativeLease: lease,
		Keys:       nativeRuntimeCaptureKeys(state.SnapshotCaptureMemKey(deployment, tier, uuid.NewString())),
		AcceptedAt: now, Deadline: now.Add(time.Minute)}
}

func runtimeCaptureRecordFixture(t *testing.T, authority nativeRuntimeCaptureAuthority) nativeRuntimeCaptureRecord {
	t.Helper()
	return nativeRuntimeCaptureRecord{Version: 1, Authority: authority, Capture: nativeQualificationCaptureRecord{Version: 1,
		InstanceID: authority.InstanceID, CaptureID: nativeRuntimeCaptureID(authority.Keys.Memory),
		NativeGeneration: authority.NativeGeneration, KernelBootID: authority.KernelBootID, FCVersion: "1.7.0",
		StartedAt: authority.AcceptedAt}}
}

func completeRuntimeCapture(record nativeRuntimeCaptureRecord) nativeRuntimeCaptureRecord {
	record.Capture.Info = SnapshotInfo{MemBytes: 128 << 20, VMStateBytes: 4096, StoredBytes: 64 << 20}
	record.Capture.Backing = BackingIdentity{Version: backingIdentityVersion, Kernel: "sha256:" + strings.Repeat("a", 64),
		Base: "sha256:" + strings.Repeat("b", 64), Timer: guestTimerArgs}
	record.Capture.CompletedAt = record.Capture.StartedAt.Add(time.Second)
	return record
}

func TestNativeRuntimeCaptureIDComesFromTheV2Namespace(t *testing.T) {
	deployment, capture := uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		name, key, want string
	}{
		{name: "init", key: state.SnapshotCaptureMemKey(deployment, state.SnapshotTierInit, capture), want: capture},
		{name: "warm", key: state.SnapshotCaptureMemKey(deployment, state.SnapshotTierWarm, capture), want: capture},
		{name: "legacy", key: state.SnapVMStateKey(deployment)},
		{name: "empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nativeRuntimeCaptureID(tc.key); got != tc.want {
				t.Fatalf("nativeRuntimeCaptureID(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

func TestNativeRuntimeCaptureAuthorityRequiresAFreshCoupledNamespace(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*nativeRuntimeCaptureAuthority)
		valid  bool
	}{
		{name: "init capture", valid: true},
		{name: "uncoupled device state", mutate: func(a *nativeRuntimeCaptureAuthority) { a.Keys.VMState += "-other" }},
		{name: "legacy namespace", mutate: func(a *nativeRuntimeCaptureAuthority) {
			a.Keys = nativeRuntimeCaptureKeys(state.SnapVMStateKey(a.DeploymentID))
		}},
		{name: "another deployment", mutate: func(a *nativeRuntimeCaptureAuthority) { a.DeploymentID = uuid.NewString() }},
		{name: "deadline beyond the limit", mutate: func(a *nativeRuntimeCaptureAuthority) {
			a.Deadline = a.AcceptedAt.Add(api.NativeRuntimeCaptureMaxDuration + time.Second)
		}},
		{name: "expired before acceptance", mutate: func(a *nativeRuntimeCaptureAuthority) { a.Deadline = a.AcceptedAt }},
		{name: "no native generation", mutate: func(a *nativeRuntimeCaptureAuthority) { a.NativeGeneration = "" }},
		{name: "lease of another VM", mutate: func(a *nativeRuntimeCaptureAuthority) { a.InstanceID = "other-vm" }},
		{name: "builder lease", mutate: func(a *nativeRuntimeCaptureAuthority) { a.NativeLease.IsBuilder = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := runtimeCaptureAuthorityFixture(t, state.SnapshotTierInit)
			if tc.mutate != nil {
				tc.mutate(&a)
			}
			if err := a.validate(); (err == nil) != tc.valid {
				t.Fatalf("validate() = %v, want valid %v", err, tc.valid)
			}
		})
	}
	if err := runtimeCaptureAuthorityFixture(t, state.SnapshotTierWarm).validate(); err != nil {
		t.Fatalf("warm capture authority: %v", err)
	}
}

func TestNativeRuntimeCaptureJournalStartsCompletesAndNeverReplays(t *testing.T) {
	j := nativeJournalFixture(filepath.Join(t.TempDir(), "journal")).runtimeCaptures()
	start := runtimeCaptureRecordFixture(t, runtimeCaptureAuthorityFixture(t, state.SnapshotTierWarm))
	instance := start.Authority.InstanceID
	if err := j.write(completeRuntimeCapture(start)); err == nil {
		t.Fatal("a completion without a durable start was accepted")
	}
	if err := j.write(start); err != nil {
		t.Fatal(err)
	}
	if got, err := j.read(instance); err != nil || got.Authority != start.Authority || !got.Capture.Equal(start.Capture) {
		t.Fatalf("read start = %+v, %v", got, err)
	}
	other := runtimeCaptureRecordFixture(t, runtimeCaptureAuthorityFixture(t, state.SnapshotTierInit))
	if err := j.write(other); err == nil {
		t.Fatal("a second capture replaced an uncertain one")
	}
	if err := j.remove(start); err == nil {
		t.Fatal("an incomplete capture record was removed")
	}
	done := completeRuntimeCapture(start)
	if err := j.write(done); err != nil {
		t.Fatal(err)
	}
	reused := start
	reused.Capture.StartedAt = reused.Capture.StartedAt.Add(time.Millisecond)
	if err := j.write(reused); err == nil {
		t.Fatal("a completed capture namespace was reused")
	}
	if err := j.write(other); err != nil {
		t.Fatalf("a fresh capture after completion: %v", err)
	}
	if err := j.remove(done); err == nil {
		t.Fatal("removal accepted a record that had been replaced")
	}
	otherDone := completeRuntimeCapture(other)
	if err := j.write(otherDone); err != nil {
		t.Fatal(err)
	}
	if err := j.remove(otherDone); err != nil {
		t.Fatal(err)
	}
	if _, err := j.read(instance); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read after removal err = %v", err)
	}
}

func TestNativeRuntimeCaptureJournalRejectsAnotherKernelBoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "journal")
	record := runtimeCaptureRecordFixture(t, runtimeCaptureAuthorityFixture(t, state.SnapshotTierInit))
	if err := nativeJournalFixture(root).runtimeCaptures().write(record); err != nil {
		t.Fatal(err)
	}
	rebooted := &nativeLaunchJournal{root: root, bootID: func() (string, error) { return uuid.NewString(), nil }}
	if _, err := rebooted.runtimeCaptures().read(record.Authority.InstanceID); err == nil {
		t.Fatal("a capture from an earlier kernel boot was accepted")
	}
}

func TestNativeSnapshotCapturePermitServesEitherAuthority(t *testing.T) {
	authority := runtimeCaptureAuthorityFixture(t, state.SnapshotTierWarm)
	record := runtimeCaptureRecordFixture(t, authority)
	runtime := nativeSnapshotCapturePermit{Runtime: authority, Capture: record.Capture}
	if !runtime.runtime() || runtime.instanceID() != authority.InstanceID || runtime.nativeGeneration() != authority.NativeGeneration ||
		runtime.kernelBootID() != authority.KernelBootID || !runtime.deadline().Equal(authority.Deadline) || runtime.keys() != authority.Keys {
		t.Fatalf("runtime permit accessors = %+v", runtime)
	}
	if err := runtime.validateStarted(); err != nil {
		t.Fatal(err)
	}
	completed := runtime
	completed.Capture = completeRuntimeCapture(record).Capture
	if err := completed.validateStarted(); err == nil {
		t.Fatal("a completed capture authorized more effects")
	}
	mixed := runtime
	mixed.Incoming = nativeQualificationRecord{Version: 2}
	if err := mixed.validateCapture(); err == nil {
		t.Fatal("one capture carried two authorities")
	}
	if (nativeSnapshotCapturePermit{}).runtime() {
		t.Fatal("a qualification permit reported a runtime authority")
	}
}

func TestNativeSnapshotCapturePermitChecksTheDurableRuntimeStart(t *testing.T) {
	journal := nativeJournalFixture(filepath.Join(t.TempDir(), "journal"))
	record := runtimeCaptureRecordFixture(t, runtimeCaptureAuthorityFixture(t, state.SnapshotTierInit))
	permit := nativeSnapshotCapturePermit{Runtime: record.Authority, Capture: record.Capture}
	if err := permit.requireCurrent(journal, true); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing durable start err = %v", err)
	}
	if err := journal.runtimeCaptures().write(record); err != nil {
		t.Fatal(err)
	}
	if err := permit.requireCurrent(journal, true); err != nil {
		t.Fatal(err)
	}
	changed := permit
	changed.Capture.StartedAt = changed.Capture.StartedAt.Add(time.Millisecond)
	if err := changed.requireCurrent(journal, true); err == nil {
		t.Fatal("a changed capture start passed the durable check")
	}
}

func TestNativeRuntimePublicationIntentIsAVersion2Record(t *testing.T) {
	j, lease := preparedNativeJournal(t)
	physical, err := j.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	authority := runtimeCaptureAuthorityFixture(t, state.SnapshotTierInit)
	authority.InstanceID, authority.NativeLease, authority.NativeGeneration = lease.Instance, physical.Lease, physical.Generation
	record := runtimeCaptureRecordFixture(t, authority)
	intent := nativeSnapshotPublicationIntent{Version: 2, Directory: nativeLoopIdentity{Device: 1, Inode: 2}, File: nativeLoopIdentity{Device: 1, Inode: 3},
		JailBase: "/srv/fc/jail", Runtime: authority, Capture: record.Capture, Physical: physical, Keys: authority.Keys}
	body, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"incoming"`) || !strings.Contains(string(body), `"runtime"`) {
		t.Fatalf("version 2 intent fields: %s", body)
	}
	var decoded nativeSnapshotPublicationIntent
	if err := json.Unmarshal(body, &decoded); err != nil || decoded.Runtime != intent.Runtime || !decoded.Capture.Equal(intent.Capture) {
		t.Fatalf("decoded = %+v, err = %v", decoded, err)
	}
	withIncoming := strings.Replace(string(body), `"runtime":`, `"incoming":{},"runtime":`, 1)
	if err := json.Unmarshal([]byte(withIncoming), &decoded); err == nil {
		t.Fatal("a version 2 intent also carried a qualification incoming record")
	}
	mislabelled := intent
	mislabelled.Version = 1
	if err := mislabelled.validate(); err == nil {
		t.Fatal("a version 1 intent carried a runtime authority")
	}
}

type runtimeCaptureVMM struct {
	VMM
	enabled bool
}

func (v runtimeCaptureVMM) nativeRuntimeCaptureEnabled() bool { return v.enabled }

func TestManagerRequestsNativeRuntimeCaptureOnlyForOrdinaryCaptures(t *testing.T) {
	inst := &Instance{Lease: leaseForSlot("runtime-capture", 4), DeploymentID: uuid.NewString(), nativeGeneration: uuid.NewString()}
	m := &Manager{vmm: runtimeCaptureVMM{enabled: true}, fcVersion: "1.7.0", instanceBacking: map[string]BackingIdentity{}}
	req := m.nativeRuntimeCapture(inst, SnapshotSpec{})
	if req == nil || req.DeploymentID != inst.DeploymentID || req.NativeGeneration != inst.nativeGeneration || req.FCVersion != "1.7.0" {
		t.Fatalf("ordinary capture request = %+v", req)
	}
	admitted := SnapshotSpec{admittedParent: runtimeadmission.Receipt{Binding: runtimeadmission.Binding{ProtocolVersion: 2}}}
	migration := SnapshotSpec{functionMigration: &FunctionMigrationBinding{}}
	sealedStore, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sealed := SnapshotSpec{Storage: sealedStore}
	for name, spec := range map[string]SnapshotSpec{"admitted": admitted, "migration": migration, "sealed": sealed} {
		if req := m.nativeRuntimeCapture(inst, spec); req != nil {
			t.Fatalf("%s capture requested native runtime capture: %+v", name, req)
		}
	}
	m.vmm = runtimeCaptureVMM{}
	if req := m.nativeRuntimeCapture(inst, SnapshotSpec{}); req != nil {
		t.Fatalf("disabled VMM requested native runtime capture: %+v", req)
	}
}

func TestSnapshotKeepAliveUnderNativeRecoveryRefusesWithoutARuntimeRequest(t *testing.T) {
	for name, v := range map[string]*JailerVMM{
		"setting off": {nativeRecovery: &nativeProcessRecoveryRuntime{}},
		"setting on":  {nativeRecovery: &nativeProcessRecoveryRuntime{}, nativeRuntimeCapture: true},
	} {
		if _, err := v.SnapshotKeepAlive(t.Context(), leaseForSlot("runtime-capture", 4), SnapshotSpec{}); !errors.Is(err, state.ErrConflict) ||
			!strings.Contains(err.Error(), "snapshot publication producer is unavailable") {
			t.Fatalf("%s: SnapshotKeepAlive err = %v", name, err)
		}
	}
}
