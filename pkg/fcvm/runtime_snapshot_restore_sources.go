package fcvm

// adr: 595. Verify every restore input before handing bytes to a native owner.

import (
	"context"
	"errors"
	"os"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

// SnapshotRestoreInputs is private native preparation, not an advertised
// restore capability. A durable catalog review and hashed wire payload must
// authorize these inputs before a caller may execute /snapshot/load.
type SnapshotRestoreInputs struct {
	Binding  runtimeadmission.Binding
	Capture  runtimeadmission.SnapshotCapture
	Snapshot Snapshot
	Runtime  ColdBootSpec
	Sources  []runtimeadmission.ArtifactSource
}

// VerifiedSnapshotInputs holds protected sources, not a runtime receipt. The
// native lease owns them until Kill/releaseRuntimeSources confirms retirement.
type VerifiedSnapshotInputs struct {
	memory, vmstate, privateDrive string
	drives                        map[string]string
	owner                         *runtimeDriveHandoff
}

type snapshotSourceFlight struct {
	cancel  context.CancelFunc
	done    chan struct{}
	handoff *runtimeDriveHandoff
}

// PrepareSnapshotRestoreInputs retains verified bytes without starting a VM.
// It does not satisfy the disabled measured restore/promotion capability gates.
func (v *JailerVMM) PrepareSnapshotRestoreInputs(ctx context.Context, lease Lease, req SnapshotRestoreInputs) (result VerifiedSnapshotInputs, err error) {
	req.Capture = req.Capture.Clone()
	req.Sources = slices.Clone(req.Sources)
	req.Runtime = cloneSnapshotRestoreRuntime(req.Runtime)
	if err = checkSnapshotRestoreInputLayout(lease, req); err != nil {
		return result, err
	}
	if err = v.registerRuntimeDriveHandoff(lease, req.Runtime, req.Sources); err != nil {
		return result, err
	}
	ctx, flight, err := v.beginSnapshotSourceFlight(ctx, lease)
	if err != nil {
		return result, errors.Join(err, v.releaseRuntimeSources(lease.Instance))
	}
	defer func() {
		flight.finish()
		if err != nil {
			result = VerifiedSnapshotInputs{}
			err = errors.Join(err, v.releaseRuntimeSources(lease.Instance))
		}
	}()
	result, producers, err := v.sealSnapshotRestoreInputs(ctx, lease, req)
	if err != nil {
		return result, err
	}
	flight.handoff.mu.Lock()
	defer flight.handoff.mu.Unlock()
	if flight.handoff.closed {
		return VerifiedSnapshotInputs{}, runtimeadmission.ErrStale
	}
	if err = errors.Join(req.Binding.Validate(time.Now()), ctx.Err()); err != nil {
		return VerifiedSnapshotInputs{}, err
	}
	capture := req.Capture.Clone()
	flight.handoff.restoreCapture, flight.handoff.restoreProducers = &capture, producers
	result.owner = flight.handoff
	return result, nil
}

func checkSnapshotRestoreInputLayout(lease Lease, req SnapshotRestoreInputs) error {
	if lease.Instance != req.Binding.InstanceID || req.Snapshot.DeploymentID != req.Binding.DeploymentID || req.Snapshot.Stale || req.Snapshot.Networkless || req.Runtime.Networkless || req.Snapshot.FCVersion == "" || req.Snapshot.MemBytes != req.Capture.Memory.Bytes || checkColdBootArtifactSources(req.Runtime, req.Sources) != nil {
		return runtimeadmission.ErrInvalid
	}
	if req.Runtime.MemSizeMiB <= 0 || int64(req.Runtime.MemSizeMiB) > api.ApplicationStandardSnapshotMaxArtifactBytes>>20 {
		return runtimeadmission.ErrInvalid
	}
	if err := req.Capture.CheckRestoreInputs(req.Binding, req.Sources, req.Snapshot.StorageKey, req.Snapshot.VMStateStorageKey, int64(req.Runtime.MemSizeMiB)<<20, time.Now()); err != nil {
		return err
	}
	config := BuildColdBootConfig(req.Runtime, lease.Slot)
	if len(config.Drives) != len(req.Capture.Parent.ArtifactConsumption.Drives) {
		return runtimeadmission.ErrInvalid
	}
	old := map[string]runtimeadmission.ConsumedDrive{}
	for _, drive := range req.Capture.Parent.ArtifactConsumption.Drives {
		old[drive.DriveID] = drive
	}
	for _, drive := range config.Drives {
		parent, found := old[drive.DriveID]
		if !found || parent.Source.StorageKey != drive.PathOnHost || parent.ReadOnly != drive.IsReadOnly || parent.RootDevice != drive.IsRootDevice {
			return runtimeadmission.ErrStale
		}
		delete(old, drive.DriveID)
	}
	return nil
}

func (v *JailerVMM) beginSnapshotSourceFlight(ctx context.Context, lease Lease) (context.Context, *snapshotSourceFlight, error) {
	handoff, err := v.runtimeDriveHandoff(lease)
	if err != nil || handoff == nil {
		return nil, nil, errors.Join(runtimeadmission.ErrStale, err)
	}
	handoff.mu.Lock()
	defer handoff.mu.Unlock()
	if handoff.closed || handoff.restorePreparation != nil {
		return nil, nil, runtimeadmission.ErrReplay
	}
	ctx, cancel := context.WithCancel(ctx)
	flight := &snapshotSourceFlight{cancel: cancel, done: make(chan struct{}), handoff: handoff}
	handoff.restorePreparation = flight
	return ctx, flight, nil
}

func (f *snapshotSourceFlight) finish() {
	f.cancel()
	f.handoff.mu.Lock()
	defer f.handoff.mu.Unlock()
	if f.handoff.restorePreparation == f {
		f.handoff.restorePreparation = nil
		close(f.done)
	}
}

func (v *JailerVMM) sealSnapshotRestoreInputs(ctx context.Context, lease Lease, req SnapshotRestoreInputs) (VerifiedSnapshotInputs, map[string]rootfs.ArtifactIdentity, error) {
	result := VerifiedSnapshotInputs{drives: map[string]string{}}
	producers := map[string]rootfs.ArtifactIdentity{}
	byRole := map[string]runtimeadmission.ConsumedDrive{}
	for _, drive := range req.Capture.Parent.ArtifactConsumption.Drives {
		byRole[drive.Source.Role()] = drive
	}
	for _, source := range req.Sources {
		path, err := v.runtimeSources().acquire(ctx, v.storage, lease.Instance, source)
		if err != nil {
			return VerifiedSnapshotInputs{}, nil, err
		}
		actual, err := measureSealedSnapshotSource(ctx, path, source)
		if err != nil {
			return VerifiedSnapshotInputs{}, nil, err
		}
		id := byRole[source.Role()].DriveID
		result.drives[id], producers[id] = path, actual
	}
	paths := []*string{&result.memory, &result.vmstate, &result.privateDrive}
	for i, artifact := range []runtimeadmission.CapturedArtifact{req.Capture.Memory, req.Capture.VMState, req.Capture.PrivateDrive} {
		// This adapter only reuses complete-stream sealing. It never records a
		// snapshot artifact as a newly approved application producer.
		source := runtimeadmission.ArtifactSource{Kind: "full-rootfs", StorageKey: artifact.StorageKey, Digest: artifact.Digest, Bytes: artifact.Bytes}
		path, err := v.runtimeSources().acquire(ctx, v.storage, lease.Instance, source)
		if err != nil {
			return VerifiedSnapshotInputs{}, nil, err
		}
		if _, err := measureSealedSnapshotSource(ctx, path, source); err != nil {
			return VerifiedSnapshotInputs{}, nil, err
		}
		*paths[i] = path
	}
	result.drives[byRole["main"].DriveID] = result.privateDrive
	return result, producers, ctx.Err()
}

func measureSealedSnapshotSource(ctx context.Context, path string, source runtimeadmission.ArtifactSource) (rootfs.ArtifactIdentity, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0o444 {
		return rootfs.ArtifactIdentity{}, errors.Join(runtimeadmission.ErrStale, err)
	}
	file, err := os.Open(path)
	if err != nil {
		return rootfs.ArtifactIdentity{}, err
	}
	actual, info, err := measurePinnedRuntimeDrive(ctx, file, source.Bytes)
	if err == nil && (!os.SameFile(before, info) || actual.Digest != source.Digest || actual.Bytes != source.Bytes) {
		err = runtimeadmission.ErrInvalid
	}
	return actual, errors.Join(err, file.Close(), ctx.Err())
}

func pinSnapshotRestoreDrive(ctx context.Context, sandbox *os.Root, drive Drive, source runtimeadmission.ArtifactSource, handoff *runtimeDriveHandoff) (pinnedRuntimeDrive, error) {
	if handoff.restoreCapture == nil {
		return pinRuntimeDrive(ctx, sandbox, drive, source)
	}
	producer, present := handoff.restoreProducers[drive.DriveID]
	if !present || producer.Digest != source.Digest || producer.Bytes != source.Bytes {
		return pinnedRuntimeDrive{}, runtimeadmission.ErrStale
	}
	expected := source
	if source.Role() == "main" {
		expected.Digest, expected.Bytes = handoff.restoreCapture.PrivateDrive.Digest, handoff.restoreCapture.PrivateDrive.Bytes
	}
	pinned, err := pinRuntimeDrive(ctx, sandbox, drive, expected)
	if err == nil {
		pinned.observation.Source, pinned.observation.Producer = source, producer
	}
	return pinned, err
}
