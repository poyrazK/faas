//go:build linux || darwin

// adr: 796 — qualification capture stays inside one original pinned task session.
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/state"
)

type nativeSnapshotPlacementBackend interface {
	CheckSnapshotDirectory(context.Context, *os.File, string) error
}

type nativeSnapshotCaptureBackend interface {
	nativeSnapshotCaptureFileBackend
	nativeSnapshotOutputBackend
	nativeSnapshotPlacementBackend
}

func (v *JailerVMM) checkNativeSnapshotCaptureSupport() error {
	r := v.nativeRecovery
	if r == nil || r.journal == nil || r.snapshotMemory == nil || v.storage == nil {
		return fmt.Errorf("native snapshot: original capture producers are unavailable: %w", state.ErrConflict)
	}
	if _, ok := r.imageSources.(nativeSnapshotCaptureBackend); !ok {
		return fmt.Errorf("native snapshot: complete image producers are unavailable: %w", state.ErrConflict)
	}
	return errors.Join(r.snapshotMemory.CheckSupport(), v.checkNativeSnapshotDirectory())
}

func (v *JailerVMM) nativeSnapshotDirectory() string {
	if v.nativeRecovery.snapshotDirectory != "" {
		return v.nativeRecovery.snapshotDirectory
	}
	return os.TempDir()
}

func (v *JailerVMM) checkNativeSnapshotDirectory() error {
	directory := v.nativeSnapshotDirectory()
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || directory == "/" {
		return errors.New("native snapshot: canonical protected disk directory is required")
	}
	return checkNativeEffectDirectory(directory)
}

func (v *JailerVMM) preflightNativeSnapshotCapture(ctx context.Context, lease Lease, spec SnapshotSpec) error {
	permit, ok := ctx.Value(nativeSnapshotCaptureContextKey{}).(nativeSnapshotCapturePermit)
	if !ok || !sameNativePhysicalLease(permit.Incoming.NativeLease, lease) || spec.RegionalMigrationSource != nil || spec.MigrationLeaseToken != "" ||
		spec.BeforeCheckpoint || !spec.ResumeBeforePublish || spec.BeforeResume == nil || spec.StageMemPath != "" || spec.VMStatePath != "" {
		return fmt.Errorf("native snapshot: original qualification warm capture is required: %w", state.ErrConflict)
	}
	keys := qualificationSnapshotProof(permit.Incoming, SnapshotInfo{})
	if spec.StorageKey != keys.StorageKey || spec.VMStateStorageKey != keys.VMStateStorageKey {
		return fmt.Errorf("native snapshot: object keys differ from original capture: %w", state.ErrConflict)
	}
	if err := v.checkNativeSnapshotCaptureSupport(); err != nil {
		return err
	}
	r := v.nativeRecovery
	j := nativeImageSourceJournal{owner: r.journal, backend: r.imageSources}
	return v.withNativeSnapshotDriveInput(ctx, lease, func(input *os.File) error {
		owner, err := r.journal.read(lease.Instance)
		if err != nil {
			return err
		}
		if err := j.captureOutputAuthority(ctx, permit.Physical, owner, permit); err != nil {
			return err
		}
		return r.imageSources.(nativeSnapshotPlacementBackend).CheckSnapshotDirectory(ctx, input, v.nativeSnapshotDirectory())
	})
}

func (v *JailerVMM) nativeSnapshotKeepAlive(ctx context.Context, lease Lease, spec SnapshotSpec) (info SnapshotInfo, err error) {
	permit := ctx.Value(nativeSnapshotCaptureContextKey{}).(nativeSnapshotCapturePermit)
	directory := v.nativeSnapshotDirectory()
	names, err := v.stageNativeSnapshotOutputs(ctx, lease, directory)
	if err != nil {
		return info, err
	}
	err = v.withNativeMachineAPI(ctx, lease, func(s *nativeMachineAPI) (result error) {
		j := nativeImageSourceJournal{owner: v.nativeRecovery.journal, backend: v.nativeRecovery.imageSources}
		check := func(ctx context.Context) error {
			if err := s.check(ctx, true); err != nil {
				return err
			}
			owner, err := j.owner.read(lease.Instance)
			if err != nil {
				return err
			}
			return j.captureOutputAuthority(ctx, permit.Physical, owner, permit)
		}
		if err := check(ctx); err != nil {
			return err
		}
		fence, err := v.nativeRecovery.snapshotMemory.Open(ctx, s.owner)
		if fence != nil {
			defer func() {
				cleanup := context.WithoutCancel(ctx)
				if restoreErr := fence.Restore(cleanup); restoreErr != nil {
					result = errors.Join(result, restoreErr, v.quarantineNativeSnapshot(s.owner))
				}
				result = errors.Join(result, fence.Close())
			}()
		}
		if err != nil || fence == nil {
			return errors.Join(err, errors.New("native snapshot: original memory fence is unavailable"))
		}
		machine, version, err := s.createNativeSnapshot(ctx, names, fence)
		if err != nil {
			return err
		}
		return j.withCaptureFilesLocked(ctx, s.owner, permit, v.chrootRoot(lease.Instance), directory, check, func(files nativeSnapshotCaptureFiles) error {
			// Freeze completed before this callback. Observe the original paused
			// dimensions again before restoring limits and resuming the guest.
			if err := s.checkSnapshotMachine(ctx, machine, version); err != nil {
				return err
			}
			if err := fence.Restore(ctx); err != nil {
				return errors.Join(err, v.quarantineNativeSnapshot(s.owner))
			}
			if err := spec.BeforeResume(ctx); err != nil {
				return err
			}
			if err := s.resume(ctx); err != nil {
				return err
			}
			keys := qualificationSnapshotProof(permit.Incoming, SnapshotInfo{})
			var publishErr error
			info, publishErr = v.publishNativeSnapshotFiles(ctx, files, machine, version, keys.StorageKey, keys.VMStateStorageKey, keys.DriveStorageKey)
			return publishErr
		})
	})
	if err != nil {
		return SnapshotInfo{}, err
	}
	return info, nil
}

func (s *nativeMachineAPI) createNativeSnapshot(ctx context.Context, names nativeSnapshotOutputNames, fence nativeSnapshotMemoryFence) (machine Machine, version string, err error) {
	if state, err := s.state(ctx); err != nil || state != "Running" {
		return machine, version, errors.Join(err, errors.New("native snapshot: original VM is not running"))
	}
	if _, err := s.shape(ctx, "Running", nil); err != nil {
		return machine, version, err
	}
	if _, err := s.request(ctx, http.MethodPatch, "/vm", map[string]any{"state": "Paused"}, http.StatusNoContent); err != nil {
		return machine, version, err
	}
	machine, err = s.shape(ctx, "Paused", nil)
	if err != nil {
		return machine, version, err
	}
	vmState, version, err := s.stateVersion(ctx)
	if err != nil || vmState != "Paused" {
		return machine, version, errors.Join(err, ErrSnapshotBackingChanged)
	}
	if err := fence.Widen(ctx); err != nil {
		return machine, version, err
	}
	if _, err := s.request(ctx, http.MethodPut, "/snapshot/create", map[string]any{
		"snapshot_type": "Full", "snapshot_path": names.VMState, "mem_file_path": names.Memory,
	}, http.StatusNoContent); err != nil {
		return machine, version, err
	}
	return machine, version, s.checkSnapshotMachine(ctx, machine, version)
}

func (s *nativeMachineAPI) checkSnapshotMachine(ctx context.Context, machine Machine, version string) error {
	if _, err := s.shape(ctx, "Paused", &machine); err != nil {
		return err
	}
	state, currentVersion, err := s.stateVersion(ctx)
	if err != nil || state != "Paused" || currentVersion != version {
		return errors.Join(err, ErrSnapshotBackingChanged)
	}
	return nil
}

// Called under the original physical lock. Forgetting local authority also
// blocks the Manager's error-resume if persisting revocation itself fails.
func (v *JailerVMM) quarantineNativeSnapshot(expected nativeLaunchRecord) error {
	r := v.nativeRecovery
	r.mu.Lock()
	delete(r.owned, expected.Lease.Instance)
	r.mu.Unlock()
	owner, err := r.journal.read(expected.Lease.Instance)
	if err != nil || !sameNativeSnapshotProcess(owner, expected) {
		return errors.Join(err, errors.New("native snapshot: original memory fence outcome is uncertain"))
	}
	owner.Revoked = true
	return errors.Join(errors.New("native snapshot: VM quarantined after memory fence failure"), r.journal.write(owner))
}
