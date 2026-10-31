package fcvm

// adr: 20261018 runtime admission re-bind, open question 1. A measured
// (ADR-595 protocol 2) guest's drive handoff across a vmmd restart.
//
// The predecessor pinned each approved drive with an open descriptor,
// measured it, and observed that Firecracker holds the same inodes. Those
// descriptors die with it. Firecracker's do not: an adopted Firecracker still
// holds every drive it booted from, and holding an inode keeps it from being
// freed and its number reused. So the adoption block records each drive's
// inode, and the successor reopens the drive through the adopted process's
// own descriptor (/proc/<pid>/fd/<n>), which reaches the inode whatever mount
// namespace this vmmd runs in; the image binds the predecessor made exist only
// in its own namespace and Firecracker's. The receipt carries the
// measurements. The rebuilt handoff then passes the same live observation a
// booted one does (observeApprovedRuntimeDrives), and capture re-measures the
// read-only drives as it always does.
//
// Only cold-booted guests are rebuilt: a restored guest's handoff also owns
// the restore load and its memory blobs, which are not recorded. Anything
// that does not check out leaves the guest adopted without a handoff, as
// before: it keeps serving and its next capture is refused.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

// resourceAdoptionDrivePin names one drive of a measured guest by the inode
// its Firecracker process holds, in the receipt's drive order.
type resourceAdoptionDrivePin struct {
	DriveID string               `json:"drive_id"`
	Path    string               `json:"path"`
	File    resourceFileIdentity `json:"file"`
}

// measuredColdBoot reports a receipt whose drive handoff a successor can
// rebuild: a protocol-2 cold boot, not a restore or a promoted resume.
func measuredColdBoot(r runtimeadmission.Receipt) bool {
	return r.Binding.ProtocolVersion == runtimeadmission.ArtifactProtocolVersion && r.Method == vmmdpb.WakeMethod_WAKE_COLD_BOOT &&
		r.SnapshotConsumption.IsZero() && r.SnapshotResumeEvidence.IsZero() && len(r.ArtifactConsumption.Drives) >= 2
}

// checkDrivePins checks pins describe r's drives, in order.
func checkDrivePins(r runtimeadmission.Receipt, pins []resourceAdoptionDrivePin) error {
	drives := r.ArtifactConsumption.Drives
	if !measuredColdBoot(r) || len(pins) != len(drives) {
		return errors.New("drive pins do not describe a measured cold boot")
	}
	for i, p := range pins {
		if p.DriveID != drives[i].DriveID || p.File.Inode == 0 || !adoptionString(p.Path) || p.Path == "" || filepath.Base(p.Path) != p.Path {
			return errors.New("drive pin does not name its receipt drive")
		}
	}
	return nil
}

// adoptionDrivePins returns the pins for lease's adoption block when its
// handoff is a cold boot that has been observed, else nil.
func (v *JailerVMM) adoptionDrivePins(lease Lease) []resourceAdoptionDrivePin {
	handoff, err := v.runtimeDriveHandoff(lease)
	if err != nil || handoff == nil {
		return nil
	}
	handoff.mu.Lock()
	defer handoff.mu.Unlock()
	if handoff.closed || handoff.observation.ProcessPID <= 0 || handoff.restoreLoad != nil || handoff.restoreCapture != nil || len(handoff.restoreBlobs) != 0 {
		return nil
	}
	pins := make([]resourceAdoptionDrivePin, 0, len(handoff.drives))
	for _, d := range handoff.drives {
		id, err := resourceFileID(d.info)
		if err != nil {
			return nil
		}
		pins = append(pins, resourceAdoptionDrivePin{DriveID: d.observation.DriveID, Path: d.path, File: id})
	}
	return pins
}

// adoptRuntimeDriveHandoff rebuilds an adopted measured guest's handoff and
// observes it. lease must be the adopted lease, whose process is registered.
func (v *JailerVMM) adoptRuntimeDriveHandoff(ctx context.Context, lease Lease, pid int, r runtimeadmission.Receipt, pins []resourceAdoptionDrivePin) error {
	if v.nativeRecovery != nil {
		return errors.New("vmm: adoption is not available with native process recovery")
	}
	handoff, err := rebuildRuntimeDriveHandoff(ctx, "/proc", pid, lease, r, pins)
	if err != nil {
		return err
	}
	v.mu.Lock()
	if v.runtimeDriveHandoffs == nil {
		v.runtimeDriveHandoffs = map[string]*runtimeDriveHandoff{}
	}
	if v.runtimeDriveHandoffs[lease.Instance] != nil {
		v.mu.Unlock()
		closePinnedDrives(handoff.drives)
		return runtimeadmission.ErrReplay
	}
	v.runtimeDriveHandoffs[lease.Instance] = handoff
	v.mu.Unlock()
	// The live observation proves the registered process holds every inode
	// with its access mode, and is the process the receipt observed.
	observed, err := v.ObservedRuntimeDrives(ctx, lease)
	if err == nil && !runtimeConsumptionFromObservation(observed).Equal(r.ArtifactConsumption) {
		err = runtimeadmission.ErrStale
	}
	if err != nil {
		return errors.Join(err, v.releaseRuntimeDriveHandoff(lease.Instance))
	}
	return nil
}

// rebuildRuntimeDriveHandoff opens each pinned drive through the descriptor
// process pid holds on its recorded inode. The observation starts as the
// receipt's, so a later observation of another process is stale.
func rebuildRuntimeDriveHandoff(ctx context.Context, procRoot string, pid int, lease Lease, r runtimeadmission.Receipt, pins []resourceAdoptionDrivePin) (*runtimeDriveHandoff, error) {
	consumption := r.ArtifactConsumption
	if err := checkDrivePins(r, pins); err != nil {
		return nil, errors.Join(runtimeadmission.ErrInvalid, err)
	}
	if pid <= 0 || uint64(pid) != uint64(consumption.ProcessPID) || r.Binding.InstanceID != lease.Instance || r.LeaseUID != int32(lease.UID) || !runtimeadmission.ValidHash(consumption.ConfigHash) {
		return nil, runtimeadmission.ErrStale
	}
	files, err := openHeldDrives(ctx, filepath.Join(procRoot, strconv.Itoa(pid), "fd"), pins)
	if err != nil {
		return nil, err
	}
	handoff := &runtimeDriveHandoff{lease: lease, sources: map[string]runtimeadmission.ArtifactSource{},
		observation: RuntimeDriveHandoffObservation{InstanceID: lease.Instance, ConfigHash: consumption.ConfigHash, LeaseUID: lease.UID,
			ProcessPID: pid, ProcessStart: consumption.ProcessStart}}
	for i, d := range consumption.Drives {
		f := files[i]
		info, err := f.Stat()
		if err == nil && (!info.Mode().IsRegular() || info.Size() != d.InjectedBytes) {
			err = runtimeadmission.ErrStale
		}
		if err != nil {
			closePinnedDrives(handoff.drives)
			closeFiles(files[i:])
			return nil, err
		}
		handoff.sources[d.DriveID] = d.Source
		handoff.drives = append(handoff.drives, pinnedRuntimeDrive{
			observation: RuntimeDriveObservation{Source: d.Source, DriveID: d.DriveID, ReadOnly: d.ReadOnly, RootDevice: d.RootDevice,
				Producer: rootfs.ArtifactIdentity{Digest: d.ProducerDigest, Bytes: d.ProducerBytes},
				Injected: rootfs.ArtifactIdentity{Digest: d.InjectedDigest, Bytes: d.InjectedBytes}},
			path: pins[i].Path, file: f, info: info,
		})
	}
	if err := distinctWritableRuntimeDrive(handoff.drives); err != nil {
		closePinnedDrives(handoff.drives)
		return nil, err
	}
	return handoff, ctx.Err()
}

// openHeldDrives opens, for each pin, the inode a descriptor in fdDir names.
// A pin no descriptor holds is stale: its inode may have been freed.
func openHeldDrives(ctx context.Context, fdDir string, pins []resourceAdoptionDrivePin) ([]*os.File, error) {
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		return nil, err
	}
	if len(entries) > 4096 {
		return nil, runtimeadmission.ErrInvalid
	}
	files := make([]*os.File, len(pins))
	fail := func(err error) ([]*os.File, error) {
		closeFiles(files)
		return nil, err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		path := filepath.Join(fdDir, entry.Name())
		info, err := os.Stat(path)
		if err != nil {
			continue // An unrelated descriptor may close during the walk.
		}
		id, err := resourceFileID(info)
		if err != nil {
			continue
		}
		for i, p := range pins {
			if files[i] != nil || id != p.File {
				continue
			}
			f, err := os.Open(path)
			if err != nil {
				return fail(err)
			}
			opened, err := f.Stat()
			if err != nil || !os.SameFile(info, opened) {
				_ = f.Close()
				return fail(errors.Join(runtimeadmission.ErrStale, err))
			}
			files[i] = f
		}
	}
	for _, f := range files {
		if f == nil {
			return fail(runtimeadmission.ErrStale)
		}
	}
	return files, nil
}

func closePinnedDrives(drives []pinnedRuntimeDrive) {
	for _, d := range drives {
		_ = d.file.Close()
	}
}

func closeFiles(files []*os.File) {
	for _, f := range files {
		if f != nil {
			_ = f.Close()
		}
	}
}
