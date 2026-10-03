package fcvm

import (
	"context"
	"errors"
	"path/filepath"
	"time"
)

// Compatibility staging methods have no caller context. Bound their lock wait;
// boot/restore pass the original RPC context through the private owner methods.
func (v *JailerVMM) driveStagingContext() (context.Context, context.CancelFunc) {
	budget := v.readyTimeout
	if budget <= 0 {
		budget = 30 * time.Second
	}
	return context.WithTimeout(context.Background(), budget)
}

func (v *JailerVMM) nativeDriveStagingOwner(ctx context.Context, instance string) (nativeLaunchRecord, error) {
	r := v.nativeRecovery
	if r == nil {
		return nativeLaunchRecord{}, nil
	}
	if err := r.acquireDaemonOwnership(ctx); err != nil {
		return nativeLaunchRecord{}, err
	}
	owner, err := r.journal.read(instance)
	if err != nil {
		return nativeLaunchRecord{}, err
	}
	if owner.Generation != r.generation(instance) || owner.Revoked || owner.Authorized || owner.ResourcesRemoved {
		return nativeLaunchRecord{}, errors.New("native loop mount: instance has no local prepared drive writer")
	}
	return owner, nil
}

func (v *JailerVMM) driveStagingSession(ctx context.Context, owner nativeLaunchRecord, instance, drive, prefix string, fn func(string) error) error {
	if v.nativeRecovery == nil {
		return loopMountSession(drive, prefix, fn)
	}
	if owner.Lease.Instance != instance || drive != filepath.Join(v.chrootRoot(instance), layerImageName) {
		return errors.New("native loop mount: staging requires the original canonical writable drive")
	}
	j := nativeLoopMountJournal{owner: v.nativeRecovery.journal, backend: v.nativeRecovery.loopMounts, helperGroups: v.nativeRecovery.helperGroups}
	return j.session(ctx, owner, drive, fn)
}
