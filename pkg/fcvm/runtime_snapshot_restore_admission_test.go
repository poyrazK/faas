package fcvm

// adr: 595 Portable forwarding tests simulate process/load facts explicitly.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type consumedSnapshotTestVMM struct {
	*consumedRuntimeVMM
	version             uint32
	loads, witnesses    int
	inputs              SnapshotRestoreInputs
	ownerLease          Lease
	loadErr             error
	editProof           func(*runtimeadmission.ArtifactConsumption, *runtimeadmission.SnapshotConsumption)
	observeSnapshot     func(context.Context) error
	allowPaused, paused bool
	resumeCalls         int
	resumeErr           error
	editResume          func(*RuntimeSnapshotResumeObservation)
}

func (v *consumedSnapshotTestVMM) RuntimeSnapshotRestoreVersion() uint32 { return v.version }

func (v *consumedSnapshotTestVMM) BootColdBootVerified(ctx context.Context, l Lease, spec ColdBootSpec, sources []runtimeadmission.ArtifactSource) error {
	v.ownerLease = l
	return v.consumedRuntimeVMM.BootColdBootVerified(ctx, l, spec, sources)
}

func (v *consumedSnapshotTestVMM) ObservedRuntimeDrives(ctx context.Context, l Lease) (RuntimeDriveHandoffObservation, error) {
	if l != v.ownerLease {
		return RuntimeDriveHandoffObservation{}, runtimeadmission.ErrStale
	}
	return v.consumedRuntimeVMM.ObservedRuntimeDrives(ctx, l)
}

func (v *consumedSnapshotTestVMM) RestoreSnapshotVerified(ctx context.Context, lease Lease, req SnapshotRestoreInputs, paused bool) error {
	v.loads++
	if paused && !v.allowPaused {
		return runtimeadmission.ErrUnavailable
	}
	if err := checkVerifiedSnapshotLoadRequest(lease, req); err != nil {
		return err
	}
	v.inputs = req
	v.paused = paused
	v.ownerLease = lease
	v.inputs.Capture, v.inputs.Runtime = req.Capture.Clone(), cloneSnapshotRestoreRuntime(req.Runtime)
	if v.loadErr != nil {
		return v.loadErr
	}
	v.sources, v.spec = req.Sources, req.Runtime
	return v.fakeVMM.Restore(ctx, lease, snapshotRestoreSpec(lease, req, paused))
}

func (v *consumedSnapshotTestVMM) ObservedRuntimeSnapshotConsumption(ctx context.Context, lease Lease) (runtimeadmission.ArtifactConsumption, runtimeadmission.SnapshotConsumption, error) {
	v.witnesses++
	if v.observeSnapshot != nil {
		if err := v.observeSnapshot(ctx); err != nil {
			return runtimeadmission.ArtifactConsumption{}, runtimeadmission.SnapshotConsumption{}, err
		}
	}
	o, err := v.ObservedRuntimeDrives(ctx, lease)
	if err != nil {
		return runtimeadmission.ArtifactConsumption{}, runtimeadmission.SnapshotConsumption{}, err
	}
	drives := runtimeConsumptionFromObservation(o)
	drives.ConfigHash = runtimeadmission.SnapshotLoadCommandHash(v.paused)
	b, c := v.inputs.Binding, v.inputs.Capture
	proof := runtimeadmission.SnapshotConsumption{Version: runtimeadmission.SnapshotRestoreVersion, CaptureToken: b.SnapshotCaptureToken, EvidenceHash: b.SnapshotEvidenceHash, Memory: c.Memory, VMState: c.VMState, PrivateDrive: c.PrivateDrive, MappedMemoryBytes: c.Memory.Bytes}
	if v.editProof != nil {
		v.editProof(&drives, &proof)
	}
	return drives, proof, nil
}

// Simulates the measured native owner; actual transport/process acceptance is
// covered separately and never inferred from these Manager tests.
func (v *consumedSnapshotTestVMM) PromoteSnapshotVerified(ctx context.Context, lease Lease, p runtimeadmission.Promotion) (RuntimeSnapshotResumeObservation, error) {
	v.resumeCalls++
	if v.resumeErr != nil {
		return RuntimeSnapshotResumeObservation{}, v.resumeErr
	}
	drives, snapshot, err := v.ObservedRuntimeSnapshotConsumption(ctx, lease)
	if err != nil {
		return RuntimeSnapshotResumeObservation{}, err
	}
	hash, err := runtimeadmission.HashSnapshotResumeParent(p.Parent)
	if err != nil {
		return RuntimeSnapshotResumeObservation{}, err
	}
	clock := time.Now().UnixNano()
	o := RuntimeSnapshotResumeObservation{Request: p.Clone(), ArtifactConsumption: drives, SnapshotConsumption: snapshot,
		ResumeEvidence: runtimeadmission.SnapshotResumeEvidence{Version: runtimeadmission.SnapshotResumeEvidenceVersion,
			Binding: p.Binding, ParentBinding: p.Parent.Binding, ParentCompletedAtUnixNano: p.Parent.CompletedAtUnixNano,
			ParentReceiptHash: hash, ResumeCommandHash: runtimeadmission.SnapshotResumeCommandHash(), ResumeHookPayloadHash: p.Binding.PayloadHash,
			CommandCompletedAtUnixNano: clock, HostTimeUnixNano: clock, HookCompletedAtUnixNano: clock, CompletedAtUnixNano: clock}}
	if v.editResume != nil {
		v.editResume(&o)
	}
	return o, ctx.Err()
}

func admittedSnapshotFixture(t *testing.T, sidecar bool) (*Manager, *consumedSnapshotTestVMM, AdmittedWakeRequest) {
	t.Helper()
	f := newSnapshotRestoreSourceFixtureWithSidecar(t, sidecar)
	bindSnapshotRestoreFixture(t, &f)
	m, cold, req := consumedRuntimeFixture(t)
	v := &consumedSnapshotTestVMM{consumedRuntimeVMM: cold, version: runtimeadmission.SnapshotRestoreVersion}
	m.vmm = v
	identity, err := m.RuntimeAdmissionIdentity()
	if err != nil {
		t.Fatal(err)
	}
	b := f.request.Binding
	b.NodeID, b.Incarnation = identity.NodeID, identity.Incarnation
	if err := m.UpdateAppEgressPolicy(t.Context(), b.AppID, b.EgressRevision, nil, nil); err != nil {
		t.Fatal(err)
	}
	req.Binding = b
	req.Request.Instance, req.Request.AppID, req.Request.DeploymentID, req.Request.AccountID = b.InstanceID, b.AppID, b.DeploymentID, b.AccountID
	req.Request.EgressAllowlist, req.Request.EgressPorts = nil, nil
	req.Request.BaseKey, req.Request.LayerKey = f.request.Runtime.BaseKey, "rootfs/main.ext4"
	req.Request.MemSizeMiB = f.request.Runtime.MemSizeMiB
	req.Request.Sidecars = nil
	if sidecar {
		req.Request.Sidecars = f.request.Runtime.Workloads[1:]
	}
	req.Request.ArtifactSources = f.request.Sources
	snapshot := f.request.Snapshot
	req.Request.Snapshot = &snapshot
	evidence := runtimeadmission.SnapshotRestoreEvidence{Version: runtimeadmission.SnapshotRestoreVersion, CaptureToken: b.SnapshotCaptureToken, FCVersion: snapshot.FCVersion, Capture: f.request.Capture.Clone()}
	req.Request.SnapshotRestore = &evidence
	req.NativeInputHash, err = NativeWakeInputHash(req.Request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), b.InstanceID) })
	return m, v, req
}

func TestNativeSnapshotAdmissionForwardsServingLoadAndCoupledReceipt(t *testing.T) {
	for _, sidecar := range []bool{false, true} {
		t.Run(map[bool]string{false: "main", true: "sidecar"}[sidecar], func(t *testing.T) {
			m, v, req := admittedSnapshotFixture(t, sidecar)
			inst, receipt, err := m.WakeAdmitted(t.Context(), req, nil)
			if err != nil {
				t.Fatal(err)
			}
			if inst.Method != WakeRestore || v.loads != 1 || v.witnesses != 1 || v.verifiedCalls != 0 || receipt.SnapshotConsumption.IsZero() || receipt.Check(req.Binding, time.Now()) != nil {
				t.Fatal("serving restore did not retain its measured facts")
			}
			if v.inputs.Binding != req.Binding || v.inputs.Runtime.Tap != inst.Net.Tap || v.inputs.Runtime.MemSizeMiB != wakeGuestMemoryMiB(req.Request) || len(v.inputs.Sources) != len(req.Request.ArtifactSources) {
				t.Fatal("protected load lost current runtime inputs")
			}
			original := inst.runtimeAdmissionReceipt.Clone()
			receipt.SnapshotConsumption.Memory.Digest = "reader-change"
			req.Request.SnapshotRestore.Capture.Parent.ArtifactConsumption.Drives[0].Source.StorageKey = "caller-change"
			if !inst.runtimeAdmissionReceipt.Equal(original) || v.inputs.Capture.Parent.ArtifactConsumption.Drives[0].Source.StorageKey == "caller-change" {
				t.Fatal("receipt or catalog shares caller-owned state")
			}
			if _, _, err := m.WakeAdmitted(t.Context(), req, nil); err == nil {
				t.Fatal("restore authority replayed")
			}
		})
	}
}

func TestNativeSnapshotAdmissionRefusesAlteredProofAndDestroysRuntime(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*runtimeadmission.ArtifactConsumption, *runtimeadmission.SnapshotConsumption)
	}{
		{"partial memory", func(_ *runtimeadmission.ArtifactConsumption, p *runtimeadmission.SnapshotConsumption) {
			p.MappedMemoryBytes--
		}},
		{"substituted blob", func(_ *runtimeadmission.ArtifactConsumption, p *runtimeadmission.SnapshotConsumption) {
			p.Memory.Digest = p.VMState.Digest
		}},
		{"wrong command", func(d *runtimeadmission.ArtifactConsumption, _ *runtimeadmission.SnapshotConsumption) {
			d.ConfigHash = runtimeadmission.SnapshotLoadCommandHash(true)
		}},
		{"no process", func(d *runtimeadmission.ArtifactConsumption, _ *runtimeadmission.SnapshotConsumption) {
			d.ProcessPID = 0
		}},
		{"different capture", func(_ *runtimeadmission.ArtifactConsumption, p *runtimeadmission.SnapshotConsumption) {
			p.CaptureToken = uuid.NewString()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, v, req := admittedSnapshotFixture(t, false)
			v.editProof = tc.edit
			inst, receipt, err := m.WakeAdmitted(t.Context(), req, nil)
			if err == nil || inst != nil || receipt.Binding.Token != "" || m.LiveCount() != 0 || m.LeasedCount() != 0 || v.witnesses != 1 {
				t.Fatal("invalid restore witness retained resources", err)
			}
		})
	}
}

func TestNativeSnapshotAdmissionRefusesBeforeAllocation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*consumedSnapshotTestVMM, *AdmittedWakeRequest)
	}{
		{"disabled", func(v *consumedSnapshotTestVMM, _ *AdmittedWakeRequest) { v.version = 0 }},
		{"missing envelope", func(_ *consumedSnapshotTestVMM, r *AdmittedWakeRequest) { r.Request.SnapshotRestore = nil }},
		{"changed capture", func(_ *consumedSnapshotTestVMM, r *AdmittedWakeRequest) {
			r.Request.SnapshotRestore.Capture.Memory.Digest = r.Request.SnapshotRestore.Capture.VMState.Digest
		}},
		{"unbound envelope", func(_ *consumedSnapshotTestVMM, r *AdmittedWakeRequest) {
			r.Binding.SnapshotCaptureToken, r.Binding.SnapshotEvidenceHash = "", ""
		}},
		{"unadvertised paused", func(v *consumedSnapshotTestVMM, r *AdmittedWakeRequest) { r.Request.KeepPaused = true; v.version = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, v, r := admittedSnapshotFixture(t, false)
			tc.edit(v, &r)
			r.NativeInputHash, _ = NativeWakeInputHash(r.Request)
			inst, receipt, err := m.WakeAdmitted(t.Context(), r, nil)
			if err == nil || inst != nil || receipt.Binding.Token != "" || m.LeasedCount() != 0 || v.loads != 0 || len(m.runtimeAdmissionTokens) != 0 {
				t.Fatal("invalid restore consumed authority", err)
			}
		})
	}
}

func TestNativeSnapshotAdmissionLoadFailureKeepsVerifiedColdFallback(t *testing.T) {
	m, v, req := admittedSnapshotFixture(t, false)
	v.loadErr = errors.New("simulated native load refusal")
	inst, receipt, err := m.WakeAdmitted(t.Context(), req, nil)
	if err != nil || inst == nil || inst.Method != WakeColdBoot || receipt.Check(req.Binding, time.Now()) != nil || !receipt.SnapshotConsumption.IsZero() || v.loads != 1 || v.witnesses != 0 || v.verifiedCalls != 1 {
		t.Fatal("failed restore lost verified cold fallback", err)
	}
}

func TestNativeSnapshotAdmissionMatchesCompanionGuestMemory(t *testing.T) {
	m, v, req := admittedSnapshotFixture(t, true)
	req.Request.MemSizeMiB = 128
	req.Request.Sidecars[0].RamMB = 64
	req.Request.Snapshot.MemBytes = 192 << 20
	req.Request.SnapshotRestore.Capture.Memory.Bytes = 192 << 20
	req.Binding.SnapshotEvidenceHash, _ = req.Request.SnapshotRestore.Hash()
	req.NativeInputHash, _ = NativeWakeInputHash(req.Request)
	inst, receipt, err := m.WakeAdmitted(t.Context(), req, nil)
	if err != nil || inst == nil || v.inputs.Runtime.MemSizeMiB != 192 || receipt.SnapshotConsumption.MappedMemoryBytes != 192<<20 {
		t.Fatal("companion guest memory differed across forwarding", err)
	}
}

func TestNativeSnapshotAdmissionDestroyJoinsCoupledObservation(t *testing.T) {
	m, v, req := admittedSnapshotFixture(t, false)
	entered := make(chan struct{})
	v.observeSnapshot = func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() }
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		inst, receipt, err := m.WakeAdmitted(ctx, req, nil)
		if inst != nil || !receipt.SnapshotConsumption.IsZero() {
			done <- errors.New("cancelled restore produced receipt")
			return
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("restore did not reach observation")
	}
	if err := m.Destroy(ctx, req.Request.Instance); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || m.LeasedCount() != 0 || m.LiveCount() != 0 {
			t.Fatal("cancelled restore retained authority", err)
		}
	case <-ctx.Done():
		t.Fatal("destroy did not join observation")
	}
}

func TestNativeSnapshotAdmissionCannotEnterOrdinaryWakeOrAdvertiseUnacceptedLoader(t *testing.T) {
	m, _, req := admittedSnapshotFixture(t, false)
	if _, err := m.Wake(t.Context(), req.Request); !errors.Is(err, runtimeadmission.ErrInvalid) || m.LeasedCount() != 0 {
		t.Fatal("ordinary wake accepted catalog authority", err)
	}
	if (&JailerVMM{}).RuntimeSnapshotRestoreVersion() != 0 {
		t.Fatal("unaccepted native restore advertised")
	}
}
