package fcvm

// adr: 435. Producer bytes, injected bytes and native handles are distinct facts.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

// RuntimeDriveObservation contains native-derived drive facts only. It is not
// a protocol-1 receipt or durable application-standard consumer acknowledgment.
type RuntimeDriveObservation struct {
	Source     runtimeadmission.ArtifactSource
	DriveID    string
	ReadOnly   bool
	RootDevice bool
	Producer   rootfs.ArtifactIdentity
	Injected   rootfs.ArtifactIdentity
}

type RuntimeDriveHandoffObservation struct {
	InstanceID, ConfigHash, ProcessStart string
	LeaseUID, ProcessPID                 int
	Drives                               []RuntimeDriveObservation
}

type pinnedRuntimeDrive struct {
	observation RuntimeDriveObservation
	path        string
	file        *os.File
	info        os.FileInfo
}

type runtimeDriveHandoff struct {
	mu          sync.Mutex
	lease       Lease
	sources     map[string]runtimeadmission.ArtifactSource
	drives      []pinnedRuntimeDrive
	observation RuntimeDriveHandoffObservation
	closed      bool
	snapshot    *nativeSnapshotFlight
}

func (v *JailerVMM) registerRuntimeDriveHandoff(lease Lease, spec ColdBootSpec, sources []runtimeadmission.ArtifactSource) error {
	config := BuildColdBootConfig(spec, lease.Slot)
	if len(config.Drives) != len(sources) || lease.Instance == "" {
		return runtimeadmission.ErrInvalid
	}
	byRole := map[string]runtimeadmission.ArtifactSource{}
	for _, source := range sources {
		byRole[source.Role()] = source
	}
	handoff := &runtimeDriveHandoff{lease: lease, sources: map[string]runtimeadmission.ArtifactSource{}}
	for i, drive := range config.Drives {
		role := "base"
		if i == 1 {
			role = "main"
		} else if i > 1 {
			role = "sidecar:" + spec.Workloads[i-1].Name
		}
		if _, duplicate := handoff.sources[drive.DriveID]; duplicate || drive.DriveID == "" {
			return runtimeadmission.ErrInvalid
		}
		handoff.sources[drive.DriveID] = byRole[role]
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.runtimeDriveHandoffs == nil {
		v.runtimeDriveHandoffs = map[string]*runtimeDriveHandoff{}
	}
	if v.runtimeDriveHandoffs[lease.Instance] != nil {
		return runtimeadmission.ErrReplay
	}
	v.runtimeDriveHandoffs[lease.Instance] = handoff
	return nil
}

func (v *JailerVMM) runtimeDriveHandoff(lease Lease) (*runtimeDriveHandoff, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	handoff := v.runtimeDriveHandoffs[lease.Instance]
	if handoff != nil && handoff.lease != lease {
		return nil, runtimeadmission.ErrStale
	}
	return handoff, nil
}

func (v *JailerVMM) pinApprovedRuntimeDrives(ctx context.Context, lease Lease, root string, config VMConfig) error {
	handoff, err := v.runtimeDriveHandoff(lease)
	if err != nil || handoff == nil {
		return err
	}
	handoff.mu.Lock()
	defer handoff.mu.Unlock()
	if handoff.closed || len(handoff.drives) != 0 || len(config.Drives) != len(handoff.sources) {
		return runtimeadmission.ErrStale
	}
	sandbox, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer sandbox.Close()
	seen := map[string]bool{}
	for _, drive := range config.Drives {
		source, found := handoff.sources[drive.DriveID]
		if !found || seen[drive.DriveID] || !source.Valid() || drive.IsReadOnly != (source.Role() != "main") || drive.IsRootDevice != (source.Role() == "base") {
			return runtimeadmission.ErrInvalid
		}
		pinned, err := pinRuntimeDrive(ctx, sandbox, drive, source)
		if err != nil {
			return err
		}
		handoff.drives = append(handoff.drives, pinned)
		seen[drive.DriveID] = true
	}
	return distinctWritableRuntimeDrive(handoff.drives)
}

func pinRuntimeDrive(ctx context.Context, sandbox *os.Root, drive Drive, source runtimeadmission.ArtifactSource) (pinnedRuntimeDrive, error) {
	var pinned pinnedRuntimeDrive
	if drive.PathOnHost == "" || filepath.Base(drive.PathOnHost) != drive.PathOnHost || filepath.IsAbs(drive.PathOnHost) {
		return pinned, runtimeadmission.ErrInvalid
	}
	info, err := sandbox.Lstat(drive.PathOnHost)
	if err != nil || !info.Mode().IsRegular() || info.Size() != source.Bytes {
		return pinned, errors.Join(runtimeadmission.ErrInvalid, err)
	}
	file, err := sandbox.Open(drive.PathOnHost)
	if err != nil {
		return pinned, err
	}
	actual, finalInfo, err := measurePinnedRuntimeDrive(ctx, file, source.Bytes)
	if err == nil && (!os.SameFile(info, finalInfo) || actual.Digest != source.Digest || actual.Bytes != source.Bytes) {
		err = runtimeadmission.ErrInvalid
	}
	if err != nil {
		return pinned, errors.Join(err, file.Close())
	}
	return pinnedRuntimeDrive{observation: RuntimeDriveObservation{Source: source, DriveID: drive.DriveID, ReadOnly: drive.IsReadOnly, RootDevice: drive.IsRootDevice, Producer: actual}, path: drive.PathOnHost, file: file, info: finalInfo}, nil
}

func measurePinnedRuntimeDrive(ctx context.Context, file *os.File, size int64) (rootfs.ArtifactIdentity, os.FileInfo, error) {
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() != size {
		return rootfs.ArtifactIdentity{}, nil, errors.Join(runtimeadmission.ErrInvalid, err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return rootfs.ArtifactIdentity{}, nil, err
	}
	actual, err := rootfs.ReadArtifactIdentity(ctx, io.LimitReader(file, size+1))
	if err != nil {
		return actual, nil, err
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || after.Size() != size || !before.ModTime().Equal(after.ModTime()) || actual.Bytes != size {
		return actual, nil, errors.Join(runtimeadmission.ErrInvalid, err)
	}
	return actual, after, ctx.Err()
}

func distinctWritableRuntimeDrive(drives []pinnedRuntimeDrive) error {
	for i, drive := range drives {
		if drive.observation.ReadOnly {
			continue
		}
		for j, other := range drives {
			if i != j && os.SameFile(drive.info, other.info) {
				return runtimeadmission.ErrInvalid
			}
		}
	}
	return nil
}

func (v *JailerVMM) measureFinalRuntimeDrives(ctx context.Context, lease Lease, root string, config []byte) error {
	handoff, err := v.runtimeDriveHandoff(lease)
	if err != nil || handoff == nil {
		return err
	}
	handoff.mu.Lock()
	defer handoff.mu.Unlock()
	if handoff.closed || len(handoff.drives) != len(handoff.sources) {
		return runtimeadmission.ErrStale
	}
	sandbox, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer sandbox.Close()
	for i := range handoff.drives {
		drive := &handoff.drives[i]
		info, err := sandbox.Lstat(drive.path)
		if err != nil || !info.Mode().IsRegular() || !os.SameFile(info, drive.info) {
			return errors.Join(runtimeadmission.ErrStale, err)
		}
		actual, info, err := measurePinnedRuntimeDrive(ctx, drive.file, drive.observation.Source.Bytes)
		if err != nil || drive.observation.ReadOnly && actual != drive.observation.Producer {
			return errors.Join(runtimeadmission.ErrInvalid, err)
		}
		drive.observation.Injected, drive.info = actual, info
	}
	hash := sha256.Sum256(config)
	handoff.observation = RuntimeDriveHandoffObservation{InstanceID: lease.Instance, LeaseUID: lease.UID, ConfigHash: hex.EncodeToString(hash[:])}
	return nil
}

func (v *JailerVMM) ObservedRuntimeDrives(ctx context.Context, lease Lease) (RuntimeDriveHandoffObservation, error) {
	if err := v.observeApprovedRuntimeDrives(ctx, lease); err != nil {
		return RuntimeDriveHandoffObservation{}, err
	}
	handoff, err := v.runtimeDriveHandoff(lease)
	if err != nil || handoff == nil {
		return RuntimeDriveHandoffObservation{}, errors.Join(runtimeadmission.ErrUnavailable, err)
	}
	handoff.mu.Lock()
	defer handoff.mu.Unlock()
	if handoff.closed || handoff.observation.ProcessPID <= 0 {
		return RuntimeDriveHandoffObservation{}, runtimeadmission.ErrUnavailable
	}
	copy := handoff.observation
	copy.Drives = slices.Clone(copy.Drives)
	return copy, nil
}

func (v *JailerVMM) releaseRuntimeDriveHandoff(instance string) error {
	v.mu.Lock()
	handoff := v.runtimeDriveHandoffs[instance]
	delete(v.runtimeDriveHandoffs, instance)
	v.mu.Unlock()
	if handoff == nil {
		return nil
	}
	handoff.mu.Lock()
	handoff.closed = true
	flight := handoff.snapshot
	if flight != nil {
		flight.cancel()
	}
	handoff.mu.Unlock()
	if flight != nil {
		<-flight.done
	}
	handoff.mu.Lock()
	defer handoff.mu.Unlock()
	var err error
	for _, drive := range handoff.drives {
		err = errors.Join(err, drive.file.Close())
	}
	handoff.drives = nil
	handoff.observation = RuntimeDriveHandoffObservation{}
	return err
}
