package fcvm

// adr: 431. Capture facts come from a retained native boot and pinned files.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type nativeSnapshotFlight struct {
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	handoff *runtimeDriveHandoff
	parent  runtimeadmission.Receipt
}

func (m *Manager) snapshotInstance(instance string, spec SnapshotSpec) (*Instance, SnapshotSpec, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst := m.live[instance]
	if inst == nil {
		return nil, SnapshotSpec{}, fmt.Errorf("not live: %w", runtimeadmission.ErrStale)
	}
	spec.admittedParent = inst.runtimeAdmissionReceipt.Clone()
	parent := spec.admittedParent
	if parent.Binding.ProtocolVersion == 0 || parent.Binding.ProtocolVersion == runtimeadmission.ProtocolVersion {
		spec.admittedParent = runtimeadmission.Receipt{}
		return inst, spec, nil
	}
	if parent.Binding.ProtocolVersion != runtimeadmission.ArtifactProtocolVersion {
		return nil, SnapshotSpec{}, runtimeadmission.ErrUnavailable
	}
	if parent.Binding.InstanceID != instance || parent.Binding.AppID != inst.AppID || parent.Binding.AccountID != inst.AccountID || parent.Binding.DeploymentID != inst.DeploymentID || parent.Netns != inst.Net.Netns || parent.HostIP != inst.Lease.HostIP.String() || parent.LeaseUID != int32(inst.Lease.UID) || inst.Paused {
		return nil, SnapshotSpec{}, runtimeadmission.ErrStale
	}
	if err := m.checkAdmittedSnapshotSpecLocked(instance, spec, parent); err != nil {
		return nil, SnapshotSpec{}, err
	}
	return inst, spec, nil
}

func (m *Manager) pauseMeasuredSnapshotProbes(inst *Instance, spec SnapshotSpec) func(bool) {
	if spec.admittedParent.Binding.ProtocolVersion == 0 {
		return func(bool) {}
	}
	m.DeleteLivenessConsecutiveFailures(inst.Lease.Instance)
	m.cancelLivenessLoop(inst.Lease.Instance)
	m.cancelReadinessLoop(inst.Lease.Instance)
	m.cancelFrameworkReadyLoop(inst.Lease.Instance)
	return func(resumed bool) {
		m.mu.Lock()
		live := m.live[inst.Lease.Instance] == inst
		m.mu.Unlock()
		if !resumed || !live {
			return
		}
		ctx := context.Background()
		m.startLivenessLoop(ctx, inst.Lease.Instance, inst.Lease.Slot, inst.LivenessProbe)
		m.startReadinessLoop(ctx, inst.Lease.Instance, inst.Lease.Slot, inst.ReadinessProbe)
		m.startFrameworkReadyLoop(ctx, inst.Lease.Instance)
		// Destroy can win between the initial check and probe registration.
		m.mu.Lock()
		live = m.live[inst.Lease.Instance] == inst
		m.mu.Unlock()
		if !live {
			m.cancelLivenessLoop(inst.Lease.Instance)
			m.cancelReadinessLoop(inst.Lease.Instance)
			m.cancelFrameworkReadyLoop(inst.Lease.Instance)
		}
	}
}

func checkedNativeSnapshotResult(ctx context.Context, spec SnapshotSpec, info SnapshotInfo, err error) (SnapshotInfo, error) {
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return SnapshotInfo{}, err
	}
	if spec.admittedParent.Binding.ProtocolVersion == 0 {
		if !info.Capture.IsZero() {
			return SnapshotInfo{}, runtimeadmission.ErrInvalid
		}
		return info, nil
	}
	capture := info.Capture
	if capture.Check(time.Now()) != nil || !capture.Parent.Equal(spec.admittedParent) || capture.Memory.StorageKey != spec.StorageKey || capture.VMState.StorageKey != spec.VMStateStorageKey || capture.Memory.Bytes != info.MemBytes || capture.VMState.Bytes != info.VMStateBytes || info.StoredBytes < 0 {
		return SnapshotInfo{}, runtimeadmission.ErrInvalid
	}
	info.Capture = capture.Clone()
	return info, nil
}

func (v *JailerVMM) beginNativeSnapshot(ctx context.Context, lease Lease, spec SnapshotSpec) (*nativeSnapshotFlight, error) {
	parent := spec.admittedParent
	if parent.Binding.ProtocolVersion == 0 {
		return nil, nil
	}
	driveKey := state.SnapshotDriveKey(state.Snapshot{StorageKey: spec.StorageKey})
	if parent.Binding.ProtocolVersion != runtimeadmission.ArtifactProtocolVersion || parent.Check(parent.Binding, time.Unix(0, parent.CompletedAtUnixNano)) != nil || parent.Binding.InstanceID != lease.Instance || parent.LeaseUID != int32(lease.UID) || v.storage == nil {
		return nil, runtimeadmission.ErrInvalid
	}
	if err := runtimeadmission.CheckSnapshotCaptureKeys(parent.Binding.DeploymentID, spec.StorageKey, spec.VMStateStorageKey, driveKey); err != nil {
		return nil, err
	}
	handoff, err := v.runtimeDriveHandoff(lease)
	if err != nil || handoff == nil {
		return nil, errors.Join(runtimeadmission.ErrUnavailable, err)
	}
	handoff.mu.Lock()
	if handoff.closed || handoff.snapshot != nil {
		handoff.mu.Unlock()
		return nil, runtimeadmission.ErrReplay
	}
	flightCtx, cancel := context.WithCancel(ctx)
	flight := &nativeSnapshotFlight{ctx: flightCtx, cancel: cancel, done: make(chan struct{}), handoff: handoff, parent: parent.Clone()}
	handoff.snapshot = flight
	handoff.mu.Unlock()
	if err := v.checkSnapshotParent(flight.ctx, lease, parent); err != nil {
		flight.finish()
		return nil, err
	}
	if err := v.checkFreshSnapshotNamespace(flight.ctx, spec); err != nil {
		flight.finish()
		return nil, err
	}
	return flight, nil
}

func (f *nativeSnapshotFlight) finish() {
	f.cancel()
	f.handoff.mu.Lock()
	defer f.handoff.mu.Unlock()
	if f.handoff.snapshot == f {
		f.handoff.snapshot = nil
		close(f.done)
	}
}

func (v *JailerVMM) cancelNativeSnapshot(instance string) {
	v.mu.Lock()
	handoff := v.runtimeDriveHandoffs[instance]
	v.mu.Unlock()
	if handoff == nil {
		return
	}
	handoff.mu.Lock()
	flight := handoff.snapshot
	if flight != nil {
		flight.cancel()
	}
	handoff.mu.Unlock()
	if flight != nil {
		<-flight.done
	}
}

func (v *JailerVMM) checkSnapshotParent(ctx context.Context, lease Lease, parent runtimeadmission.Receipt) error {
	observation, err := v.ObservedRuntimeDrives(ctx, lease)
	if err != nil {
		return err
	}
	if observation.InstanceID != parent.Binding.InstanceID || observation.LeaseUID != int(parent.LeaseUID) || !runtimeConsumptionFromObservation(observation).Equal(parent.ArtifactConsumption) {
		return runtimeadmission.ErrStale
	}
	return ctx.Err()
}

// This prevents retry cleanup from deleting an already published capture.
// Cross-node ownership still requires the separate durable capture grant;
// these byte facts do not authorize restore or make a mutable key immutable.
func (v *JailerVMM) checkFreshSnapshotNamespace(ctx context.Context, spec SnapshotSpec) error {
	snap := state.Snapshot{StorageKey: spec.StorageKey}
	for _, key := range []string{spec.StorageKey, spec.VMStateStorageKey, state.SnapshotDriveKey(snap)} {
		exists, supported, err := storage.Exists(ctx, v.storage, key)
		if err != nil {
			return err
		}
		if supported {
			if exists {
				return runtimeadmission.ErrReplay
			}
			continue
		}
		reader, err := v.storage.Get(ctx, key)
		if reader != nil {
			closeErr := reader.Close()
			return errors.Join(runtimeadmission.ErrReplay, closeErr)
		}
		if !storage.IsNotFound(err) {
			return errors.Join(runtimeadmission.ErrInvalid, err)
		}
	}
	return ctx.Err()
}

func (v *JailerVMM) measurePausedSnapshotMain(ctx context.Context, lease Lease, f *nativeSnapshotFlight) (rootfs.ArtifactIdentity, error) {
	if err := v.checkSnapshotParent(ctx, lease, f.parent); err != nil {
		return rootfs.ArtifactIdentity{}, err
	}
	f.handoff.mu.Lock()
	drives := slices.Clone(f.handoff.drives)
	closed := f.handoff.closed
	f.handoff.mu.Unlock()
	if closed {
		return rootfs.ArtifactIdentity{}, runtimeadmission.ErrStale
	}
	var main rootfs.ArtifactIdentity
	for _, drive := range drives {
		actual, _, err := measurePinnedRuntimeDrive(ctx, drive.file, drive.observation.Injected.Bytes)
		if err != nil || drive.observation.ReadOnly && actual != drive.observation.Producer {
			return rootfs.ArtifactIdentity{}, errors.Join(runtimeadmission.ErrInvalid, err)
		}
		if !drive.observation.ReadOnly {
			main = actual
		}
	}
	if main.Bytes <= 0 {
		return main, runtimeadmission.ErrInvalid
	}
	return main, ctx.Err()
}

func (v *JailerVMM) publishNativeSnapshot(lease Lease, spec SnapshotSpec, root, frozen string, f *nativeSnapshotFlight) (SnapshotInfo, error) {
	main, err := v.measurePausedSnapshotMain(f.ctx, lease, f)
	if err != nil {
		return SnapshotInfo{}, err
	}
	files, err := pinCapturedSnapshotFiles(f.ctx, spec, root, frozen)
	if err != nil {
		return SnapshotInfo{}, err
	}
	defer closeCapturedSnapshotFiles(files)
	if files[2].identity != main {
		return SnapshotInfo{}, runtimeadmission.ErrStale
	}
	if err := distinctSnapshotPrivateDrive(f.handoff, files[2].info); err != nil {
		return SnapshotInfo{}, err
	}
	current, err := v.measurePausedSnapshotMain(f.ctx, lease, f)
	if err != nil || current != main {
		return SnapshotInfo{}, errors.Join(runtimeadmission.ErrStale, err)
	}
	capture := measuredSnapshotCapture(f.parent, files, time.Now())
	if err := capture.Check(time.Now()); err != nil {
		return SnapshotInfo{}, err
	}
	if spec.ResumeBeforePublish {
		if err := v.ResumeVM(f.ctx, lease); err != nil {
			return SnapshotInfo{}, err
		}
	}
	return v.publishCapturedSnapshotFiles(f.ctx, files, capture)
}

func distinctSnapshotPrivateDrive(handoff *runtimeDriveHandoff, frozen os.FileInfo) error {
	handoff.mu.Lock()
	defer handoff.mu.Unlock()
	if handoff.closed {
		return runtimeadmission.ErrStale
	}
	for _, drive := range handoff.drives {
		if os.SameFile(drive.info, frozen) {
			return runtimeadmission.ErrInvalid
		}
	}
	return nil
}

func measuredSnapshotCapture(parent runtimeadmission.Receipt, files []capturedSnapshotFile, now time.Time) runtimeadmission.SnapshotCapture {
	artifact := func(file capturedSnapshotFile) runtimeadmission.CapturedArtifact {
		return runtimeadmission.CapturedArtifact{StorageKey: file.key, Digest: file.identity.Digest, Bytes: file.identity.Bytes}
	}
	return runtimeadmission.SnapshotCapture{Version: runtimeadmission.SnapshotCaptureVersion, Parent: parent.Clone(), Memory: artifact(files[0]), VMState: artifact(files[1]), PrivateDrive: artifact(files[2]), CapturedAtUnixNano: now.UnixNano()}
}

func (v *JailerVMM) publishCapturedSnapshotFiles(ctx context.Context, files []capturedSnapshotFile, capture runtimeadmission.SnapshotCapture) (SnapshotInfo, error) {
	var storedBytes int64
	for _, file := range files {
		if err := publishMeasuredSnapshotFile(ctx, v.storage, file); err != nil {
			return SnapshotInfo{}, err
		}
		storedBytes += allocatedBytesOrLogical(v.publishedLocalPath(file.key, file.path), file.identity.Bytes)
	}
	if err := ctx.Err(); err != nil {
		return SnapshotInfo{}, err
	}
	return SnapshotInfo{MemBytes: files[0].identity.Bytes, VMStateBytes: files[1].identity.Bytes, StoredBytes: storedBytes, Capture: capture.Clone()}, nil
}

func snapshotFilePaths(root, frozen string) []string {
	return []string{filepath.Join(root, "mem"), filepath.Join(root, "vmstate"), frozen}
}
