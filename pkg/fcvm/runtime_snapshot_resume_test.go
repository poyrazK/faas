package fcvm

// adr: 595 The absent native process must defeat simulated historical proof.

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

func newSnapshotResumeFixture(t *testing.T) (protectedRestoreFixture, *protectedRestoreTransport) {
	t.Helper()
	f := newSnapshotRestoreSourceFixtureWithSidecar(t, true)
	if os.Getuid() == 0 {
		f.lease.UID, f.lease.GID = 20000, 20000
	}
	f.lease.Netns = "fc-" + f.lease.Instance
	f.lease.HostIP = netip.MustParseAddr("10.100.0.2")
	f.vmm = NewJailerVMM(shortChrootBase(t, "s-res"), time.Second).WithStorage(f.backend).WithRuntimeSourceRoot(f.vmm.runtimeSourceRoot)
	f.vmm.fcName = "f"
	prepared := prepareProtectedRestoreFixture(t, f, true)
	transport := installProtectedRestoreTransport(prepared, func(*http.Request) (*http.Response, error) {
		return protectedRestoreResponse(http.StatusNoContent), nil
	})
	if err := prepared.vmm.loadRestoredSnapshot(prepared.ctx, prepared.lease, prepared.root, prepared.spec, protectedRestoreBody(true)); err != nil {
		t.Fatal(err)
	}
	prepared.flight.finish()
	return prepared, transport
}

func snapshotResumePromotion(t *testing.T, f protectedRestoreFixture, drives runtimeadmission.ArtifactConsumption, snapshot runtimeadmission.SnapshotConsumption) runtimeadmission.Promotion {
	t.Helper()
	parent := runtimeadmission.Receipt{Binding: f.spec.verifiedSnapshot.request.Binding,
		NativeInputHash: strings.Repeat("8", 64), Netns: f.lease.Netns, HostIP: f.lease.HostIP.String(), LeaseUID: int32(f.lease.UID),
		Method: vmmdpb.WakeMethod_WAKE_RESTORE, Paused: true, CompletedAtUnixNano: time.Now().UnixNano(), ArtifactConsumption: drives.Clone(), SnapshotConsumption: snapshot}
	fresh := parent.Binding
	fresh.Token = uuid.NewString()
	fresh.IssuedAtUnixNano, fresh.ExpiresAtUnixNano = time.Now().UnixNano(), time.Now().Add(time.Minute).UnixNano()
	p := runtimeadmission.Promotion{Binding: fresh, Parent: parent}
	bindSnapshotResumePromotion(t, &p)
	return p
}

func bindSnapshotResumePromotion(t *testing.T, p *runtimeadmission.Promotion) {
	t.Helper()
	var err error
	p.Binding.PayloadHash, err = runtimeadmission.HashPromotionPayload(p.ToProto())
	if err != nil {
		t.Fatal(err)
	}
}

func expireSnapshotResumeLoadFixture(t *testing.T, f protectedRestoreFixture, clock time.Time) time.Time {
	t.Helper()
	plan := f.spec.verifiedSnapshot
	plan.request.Binding.IssuedAtUnixNano -= int64(2 * time.Hour)
	plan.request.Binding.ExpiresAtUnixNano -= int64(2 * time.Hour)
	plan.request.Capture.Parent.Binding.IssuedAtUnixNano -= int64(2 * time.Hour)
	plan.request.Capture.Parent.Binding.ExpiresAtUnixNano -= int64(2 * time.Hour)
	plan.request.Capture.Parent.CompletedAtUnixNano -= int64(2 * time.Hour)
	plan.request.Capture.CapturedAtUnixNano -= int64(2 * time.Hour)
	e := runtimeadmission.SnapshotRestoreEvidence{Version: runtimeadmission.SnapshotRestoreVersion, CaptureToken: plan.request.Binding.SnapshotCaptureToken, FCVersion: plan.request.Snapshot.FCVersion, Capture: plan.request.Capture}
	var err error
	plan.request.Binding.SnapshotEvidenceHash, err = e.Hash()
	if err != nil {
		t.Fatal(err)
	}
	return clock.Add(-2 * time.Hour)
}

func TestSnapshotResumeHistoricalClockCannotRenewOrdinaryObservation(t *testing.T) {
	f, _ := newSnapshotResumeFixture(t)
	historical := expireSnapshotResumeLoadFixture(t, f, time.Now())
	drives, snapshot, err := f.vmm.ObservedRuntimeSnapshotConsumption(t.Context(), f.lease)
	if !errors.Is(err, runtimeadmission.ErrExpired) || !drives.IsZero() || !snapshot.IsZero() {
		t.Fatal("ordinary observation renewed historical authority", err)
	}
	drives, snapshot, err = f.vmm.observedRuntimeSnapshotConsumptionAt(t.Context(), f.lease, historical)
	want := runtimeadmission.ErrUnavailable // Native observation is unavailable on non-Linux hosts.
	if runtime.GOOS == "linux" {
		want = runtimeadmission.ErrStale
	} // The absent owned process defeats the Linux observation.
	if !errors.Is(err, want) || !drives.IsZero() || !snapshot.IsZero() {
		t.Fatal("historical observation lost its native ownership fence", err)
	}
	plan := f.spec.verifiedSnapshot
	if plan.resumeAttempted || plan.inputs.owner.closed {
		t.Fatal("observation attempted resume or retired the retained paused load")
	}
}

func simulatedPausedSnapshotConsumption(f protectedRestoreFixture) (runtimeadmission.ArtifactConsumption, runtimeadmission.SnapshotConsumption) {
	plan := f.spec.verifiedSnapshot
	observed := plan.inputs.owner.observation
	observed.Drives = make([]RuntimeDriveObservation, len(plan.inputs.owner.drives))
	for i, drive := range plan.inputs.owner.drives {
		observed.Drives[i] = drive.observation
	}
	drives := runtimeConsumptionFromObservation(observed)
	drives.ProcessPID, drives.ProcessStart = 52, "202" // Deliberately absent from VMM/procfs.
	snapshot := runtimeadmission.SnapshotConsumption{Version: runtimeadmission.SnapshotRestoreVersion,
		CaptureToken: plan.request.Binding.SnapshotCaptureToken, EvidenceHash: plan.request.Binding.SnapshotEvidenceHash,
		Memory: plan.request.Capture.Memory, VMState: plan.request.Capture.VMState, PrivateDrive: plan.request.Capture.PrivateDrive,
		MappedMemoryBytes: plan.request.Capture.Memory.Bytes}
	return drives, snapshot
}

func TestSnapshotResumeRefusesAPIOnlyLoadBeforeMutation(t *testing.T) {
	for _, fault := range []string{"no native process", "unaccepted load", "serving load", "different lease", "different retained binding", "replayed resume", "canceled"} {
		t.Run(fault, func(t *testing.T) {
			f, transport := newSnapshotResumeFixture(t)
			drives, snapshot := simulatedPausedSnapshotConsumption(f)
			p := snapshotResumePromotion(t, f, drives, snapshot)
			if err := p.CheckSnapshotResumeRequest(time.Now()); err != nil {
				t.Fatal("structural fixture invalid", err)
			}
			plan, lease := f.spec.verifiedSnapshot, f.lease
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch fault {
			case "unaccepted load":
				plan.accepted = false
			case "serving load":
				plan.keepPaused = false
			case "different lease":
				lease.processGeneration++
			case "different retained binding":
				p.Parent.Binding.Token = uuid.NewString()
				bindSnapshotResumePromotion(t, &p)
			case "replayed resume":
				plan.resumeAttempted = true
			case "canceled":
				cancel()
			}
			before := plan.resumeAttempted
			result, err := f.vmm.PromoteSnapshotVerified(ctx, lease, p)
			if err == nil || !reflect.DeepEqual(result, RuntimeSnapshotResumeObservation{}) || transport.calls.Load() != 1 || plan.resumeAttempted != before || plan.inputs.owner.closed || !plan.keepPaused && fault != "serving load" {
				t.Fatalf("invalid owner mutated or returned proof: result=%+v err=%v calls=%d", result, err, transport.calls.Load())
			}
			if fault == "replayed resume" && !errors.Is(err, runtimeadmission.ErrReplay) {
				t.Fatal("replay lost refusal classification", err)
			}
		})
	}
}

func TestSnapshotResumeFlightsExcludeCaptureAndSourcePreparation(t *testing.T) {
	f, _ := newSnapshotResumeFixture(t)
	handoff := f.spec.verifiedSnapshot.inputs.owner
	ctx, flight, err := f.vmm.beginSnapshotSourceFlight(t.Context(), f.lease)
	if err != nil {
		t.Fatal(err)
	}
	defer flight.finish()
	if _, _, err := f.vmm.beginSnapshotSourceFlight(ctx, f.lease); !errors.Is(err, runtimeadmission.ErrReplay) {
		t.Fatal("a second source/resume operation shared ownership", err)
	}
	drives, snapshot := simulatedPausedSnapshotConsumption(f)
	parent := snapshotResumePromotion(t, f, drives, snapshot).Parent
	parent.Paused = false
	parent.ArtifactConsumption.ConfigHash = runtimeadmission.SnapshotLoadCommandHash(false)
	key := state.SnapshotCaptureMemKey(parent.Binding.DeploymentID, state.SnapshotTierInit, uuid.NewString())
	spec := SnapshotSpec{StorageKey: key, VMStateStorageKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: key}), admittedParent: parent}
	if _, err := f.vmm.beginNativeSnapshot(ctx, f.lease, spec); !errors.Is(err, runtimeadmission.ErrReplay) {
		t.Fatal("capture shared an owned source/resume flight", err)
	}
	flight.finish()
	captureCtx, cancel := context.WithCancel(t.Context())
	capture := &nativeSnapshotFlight{ctx: captureCtx, cancel: cancel, done: make(chan struct{}), handoff: handoff}
	handoff.snapshot = capture
	defer capture.finish()
	if _, _, err := f.vmm.beginSnapshotSourceFlight(t.Context(), f.lease); !errors.Is(err, runtimeadmission.ErrReplay) {
		t.Fatal("source/resume shared an owned capture flight", err)
	}
}

func TestSnapshotResumeObservationOwnsAndChecksCompleteEvidence(t *testing.T) {
	f, _ := newSnapshotResumeFixture(t)
	drives, snapshot := simulatedPausedSnapshotConsumption(f)
	p := snapshotResumePromotion(t, f, drives, snapshot)
	hash, err := runtimeadmission.HashSnapshotResumeParent(p.Parent)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now().UnixNano()
	o := RuntimeSnapshotResumeObservation{Request: p.Clone(), ArtifactConsumption: drives.Clone(), SnapshotConsumption: snapshot,
		ResumeEvidence: runtimeadmission.SnapshotResumeEvidence{Version: runtimeadmission.SnapshotResumeEvidenceVersion,
			Binding: p.Binding, ParentBinding: p.Parent.Binding, ParentCompletedAtUnixNano: p.Parent.CompletedAtUnixNano, ParentReceiptHash: hash, ResumeCommandHash: runtimeadmission.SnapshotResumeCommandHash(), ResumeHookPayloadHash: strings.Repeat("a", 64),
			CommandCompletedAtUnixNano: clock, HostTimeUnixNano: clock, HookCompletedAtUnixNano: clock, CompletedAtUnixNano: clock}}
	// This checks the contract only; the simulated process must still be refused
	// by PromoteSnapshotVerified's actual owner observation before any command.
	if err := o.Check(p, time.Now()); err != nil {
		t.Fatal("complete simulated contract refused", err)
	}
	for _, fault := range []string{"request", "parent hash", "grant", "command", "drive", "mapping"} {
		t.Run(fault, func(t *testing.T) {
			changed := o.Clone()
			switch fault {
			case "request":
				changed.Request.Parent.ArtifactConsumption.Drives[0].DriveID = "caller-edit"
			case "parent hash":
				changed.ResumeEvidence.ParentReceiptHash = strings.Repeat("0", 64)
			case "grant":
				changed.ResumeEvidence.Binding.Token = uuid.NewString()
			case "command":
				changed.ResumeEvidence.ResumeCommandHash = strings.Repeat("0", 64)
			case "drive":
				changed.ArtifactConsumption.Drives[0].DriveID = "caller-edit"
			case "mapping":
				changed.SnapshotConsumption.MappedMemoryBytes--
			}
			if changed.Check(p, time.Now()) == nil || o.Check(p, time.Now()) != nil {
				t.Fatal("substituted evidence accepted or caller edit changed retained facts")
			}
		})
	}
	if _, err := f.vmm.PromoteSnapshotVerified(t.Context(), f.lease, p); err == nil {
		t.Fatal("structural evidence replaced actual native process ownership")
	}
}
