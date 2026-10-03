package state

// adr: 435. Simulated native facts exercise durable authority, not KVM acceptance.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func nativeConsumedInputs(t *testing.T, s nativeArtifactTestStore) (Instance, runtimeadmission.Binding, InstanceApplicationStandardAdmission) {
	t.Helper()
	in, base, app, dep := artifactScanBaseFixture(t, s)
	app = manageNativeArtifactApp(t, s, app)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishBaseImageScan(t.Context(), runtimeArtifactBaseScanInput(in, base)); err != nil {
		t.Fatal(err)
	}
	publishNativeComposedScan(t, s, app, dep, in.Report)
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
	return ins, candidate, capture
}

func registerConsumedNativeIdentity(t *testing.T, s ComputeNodeRuntimeIdentityStore, b runtimeadmission.Binding, protocol uint32) {
	t.Helper()
	if err := s.RegisterComputeNodeRuntimeIdentity(t.Context(), runtimeadmission.Identity{ProtocolVersion: protocol, NodeID: b.NodeID, Incarnation: b.Incarnation}); err != nil {
		t.Fatal(err)
	}
}

func consumedNativeReceipt(b runtimeadmission.Binding, capture InstanceApplicationStandardAdmission) runtimeadmission.Receipt {
	r := nativeArtifactReceipt(b, false)
	r.ArtifactConsumption = runtimeadmission.ArtifactConsumption{ConfigHash: strings.Repeat("c", 64), ProcessPID: 42, ProcessStart: "101"}
	for _, a := range capture.RuntimeArtifacts {
		source := runtimeadmission.ArtifactSource{Kind: a.Kind, WorkloadName: a.WorkloadName, StorageKey: a.StorageKey, Digest: a.Digest, Bytes: a.Bytes}
		d := runtimeadmission.ConsumedDrive{Source: source, DriveID: "drive-" + source.Role(), ReadOnly: source.Role() != "main", RootDevice: source.Role() == "base", ProducerDigest: source.Digest, ProducerBytes: source.Bytes, InjectedDigest: source.Digest, InjectedBytes: source.Bytes}
		if !d.ReadOnly {
			d.InjectedDigest = "sha256:" + strings.Repeat("d", 64)
		}
		r.ArtifactConsumption.Drives = append(r.ArtifactConsumption.Drives, d)
	}
	return r
}

func issueConsumedNativeFixture(t *testing.T, s nativeArtifactTestStore) (Instance, runtimeadmission.Receipt) {
	t.Helper()
	ins, b, capture := nativeConsumedInputs(t, s)
	registerConsumedNativeIdentity(t, s, b, runtimeadmission.ArtifactProtocolVersion)
	b, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, b)
	if err != nil {
		t.Fatal(err)
	}
	return ins, consumedNativeReceipt(b, capture)
}

func nativeConsumptionPublication(t *testing.T, s nativeArtifactTestStore) {
	t.Helper()
	ins, candidate, capture := nativeConsumedInputs(t, s)
	if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("unregistered consumption capability issued authority: %v", err)
	}
	registerConsumedNativeIdentity(t, s, candidate, runtimeadmission.ArtifactProtocolVersion)
	bad := candidate
	bad.ArtifactSourcesHash = strings.Repeat("0", 64)
	if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, bad); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("source claim differed from captured producers: %v", err)
	}
	grant, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate)
	if err != nil {
		t.Fatal(err)
	}
	r := consumedNativeReceipt(grant, capture)
	for _, mutate := range []func(*runtimeadmission.Receipt){
		func(r *runtimeadmission.Receipt) { r.ArtifactConsumption = runtimeadmission.ArtifactConsumption{} },
		func(r *runtimeadmission.Receipt) { r.ArtifactConsumption.Drives = r.ArtifactConsumption.Drives[1:] },
		func(r *runtimeadmission.Receipt) { r.ArtifactConsumption.Drives[0].ReadOnly = false },
		func(r *runtimeadmission.Receipt) {
			r.ArtifactConsumption.Drives[0].InjectedDigest = "sha256:" + strings.Repeat("e", 64)
		},
		func(r *runtimeadmission.Receipt) { r.ArtifactConsumption.ProcessStart = "0101" },
	} {
		bad := r.Clone()
		mutate(&bad)
		if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, bad); err == nil {
			t.Fatal("incomplete or different measured evidence published")
		}
		assertNativeBootUnpublished(t, s, ins)
	}
	registerConsumedNativeIdentity(t, s, grant, runtimeadmission.ProtocolVersion)
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, r); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("capability downgrade retained uncommitted authority: %v", err)
	}
	assertNativeBootUnpublished(t, s, ins)
	registerConsumedNativeIdentity(t, s, grant, runtimeadmission.ArtifactProtocolVersion)
	actual, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, r)
	if err != nil || actual.Netns != r.Netns || actual.State != string(StateRunning) {
		t.Fatalf("measured native publication: %+v %v", actual, err)
	}
	assertStandardCaptureHistory(t, s, capture)
}

func TestMemNativeConsumptionPublication(t *testing.T) {
	nativeConsumptionPublication(t, NewMemStore())
}

func TestMemNativeConsumptionStoredReceiptOwnsDrives(t *testing.T) {
	s := NewMemStore()
	ins, r := issueConsumedNativeFixture(t, s)
	want := r.Clone()
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, r); err != nil {
		t.Fatal(err)
	}
	r.ArtifactConsumption.Drives[0].ProducerDigest = "caller-mutation"
	s.mu.Lock()
	saved := s.instanceApplicationStandardBoots[r.Binding.Token].Receipt.Clone()
	s.mu.Unlock()
	if !saved.Equal(want) {
		t.Fatal("caller mutated saved native consumption")
	}
	// A new native process cannot adopt the preceding process's measured lease.
	restarted := want.Binding
	restarted.Incarnation = uuid.NewString()
	registerConsumedNativeIdentity(t, s, restarted, runtimeadmission.ArtifactProtocolVersion)
	if err := s.MarkInstanceMigrating(t.Context(), ins.ID, ins.NodeID, uuid.NewString()); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("process restart retained native residency authority: %v", err)
	}
	if err := s.UpdateInstanceStateToTerminal(t.Context(), ins.ID, string(StateStopped), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteInstance(t.Context(), ins.ID); err != nil {
		t.Fatal(err)
	}
}
