package fcvm

import (
	"context"
	"errors"
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
		return nativeLaunchRecord{}, errors.New("native drive edit: instance has no local prepared drive writer")
	}
	if err := r.journal.requireRepairProducerContext(ctx, owner); err != nil {
		return nativeLaunchRecord{}, err
	}
	return owner, nil
}
