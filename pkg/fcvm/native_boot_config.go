package fcvm

// adr: 643
import (
	"context"
	"errors"
)

// Requested dimensions are checked against retained provenance before staging
// or starting a guest. Historical regional records cannot borrow today's plan
// as their original vCPU footprint. Legacy records retain their RAM-only guard.
func checkNativeBootDimensions(owner nativeLaunchRecord, machine Machine) error {
	if machine.MemSizeMib <= 0 || machine.VcpuCount <= 0 || machine.MemSizeMib != owner.Lease.MemoryMaxMiB {
		return errors.New("native boot: config RAM differs from original lease or dimensions are invalid")
	}
	if owner.RegionalRuntime != nil && owner.RegionalResources == nil {
		return errors.New("native boot: historical regional owner has no original guest shape")
	}
	if resources := owner.RegionalResources; resources != nil && (machine.MemSizeMib != resources.RAMMB || machine.VcpuCount != resources.VCPU) {
		return errors.New("native boot: config shape differs from original regional footprint")
	}
	return nil
}

func (v *JailerVMM) nativeBootStagingOwner(ctx context.Context, lease Lease, machine Machine) (owner nativeLaunchRecord, err error) {
	r := v.nativeRecovery
	if r == nil {
		return owner, nil
	}
	if err := r.acquireDaemonOwnership(ctx); err != nil {
		return owner, err
	}
	lock, err := r.journal.lock(ctx, lease.Instance)
	if err != nil {
		return owner, err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	owner, err = v.nativeDriveStagingOwner(ctx, lease.Instance)
	if err != nil {
		return owner, err
	}
	if !sameNativePhysicalLease(owner.Lease, lease) {
		return owner, errors.New("native boot: config has no original physical lease")
	}
	return owner, errors.Join(checkNativeBootDimensions(owner, machine), ctx.Err())
}

// Restore fallback may need to replace a confirmed prior incarnation. The
// replacement preserves the original footprint; preflight never derives one.
func (v *JailerVMM) preflightNativeBootConfig(ctx context.Context, lease Lease, machine Machine) error {
	if v.nativeRecovery == nil {
		return nil
	}
	if err := v.ensureNativeLaunch(ctx, lease); err != nil {
		return err
	}
	_, err := v.nativeBootStagingOwner(ctx, lease, machine)
	return err
}

// Hold the original journal lock and task pin through config release and the
// actual running-machine observation. Validation failures write no config.
func (v *JailerVMM) releaseNativeBootConfig(ctx context.Context, lease Lease, machine Machine, path string, body []byte) error {
	if v.nativeRecovery == nil {
		return writeConfigFIFO(ctx, path, body)
	}
	return v.withNativeMachineAPI(ctx, lease, func(s *nativeMachineAPI) error {
		if err := checkNativeBootDimensions(s.owner, machine); err != nil {
			return err
		}
		if err := writeConfigFIFOChecked(ctx, path, body, configFIFOReaderFallbackTimeout, func() error { return s.check(ctx, false) }); err != nil {
			return err
		}
		_, err := s.shape(ctx, "Running", &machine)
		return err
	})
}
