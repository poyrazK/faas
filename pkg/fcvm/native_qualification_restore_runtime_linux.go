//go:build linux

package fcvm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// RestoreNativeQualification is the private native restore lifecycle. It uses
// only the original capture journal and receipt cohort, never generic Restore
// or its cold-boot fallback, and it does not register ordinary guest receivers.
func (v *JailerVMM) RestoreNativeQualification(ctx context.Context, frame state.EnvironmentQualificationExecution, lease Lease, req WakeRequest, backingCandidates [2]string, runningFCVersion, serviceDiscoveryIP string) (result error) {
	r := v.nativeRecovery
	target, ok := ctx.Value(nativeQualificationRestoreContextKey{}).(nativeQualificationRestoreRecord)
	if r == nil || r.journal == nil || !ok || target.Execution != frame || frame.InstanceID != lease.Instance ||
		lease.MemoryMaxMiB != frame.RAMMB || lease.Networkless || lease.IsBuilder || !lease.Plan.Valid() ||
		len(req.Sidecars) != 0 || len(req.MainDependsOn) != 0 {
		return fmt.Errorf("native qualification restore: original target capability and single-workload lease are required: %w", state.ErrConflict)
	}
	if err := validateNativeQualificationRunningFCVersion(runningFCVersion); err != nil {
		return err
	}
	lock, producer, err := r.journal.lockQualificationProducer(ctx, lease.Instance)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	if producer == nil || producer.restore == nil || *producer.restore != target || !sameNativePhysicalLease(lease, producer.NativeLease) ||
		producer.NativeGeneration == "" || r.generation(lease.Instance) != producer.NativeGeneration {
		return fmt.Errorf("native qualification restore: target process differs from the original attempt: %w", state.ErrConflict)
	}
	ctx, cancel := context.WithDeadline(ctx, target.Deadline)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	j := r.journal.qualifications(frame.NodeID).restores()
	capture, err := j.requireCapture(ctx, target)
	if err != nil {
		return err
	}
	if err := validateNativeQualificationRestoreFCVersion(capture.FCVersion, runningFCVersion); err != nil {
		return err
	}
	physicalLock, err := r.journal.lock(ctx, lease.Instance)
	if err != nil {
		return err
	}
	owner, readErr := r.journal.read(lease.Instance)
	physicalLockErr := physicalLock.Close()
	if err := errors.Join(readErr, physicalLockErr); err != nil {
		return err
	}
	if owner.Generation != target.NativeGeneration || owner.KernelBootID != target.KernelBootID ||
		!sameNativePhysicalLease(owner.Lease, lease) || owner.Authorized || owner.Revoked || owner.ResourcesRemoved {
		return fmt.Errorf("native qualification restore: prepared physical owner changed: %w", state.ErrConflict)
	}
	root, err := v.mkChrootForOwner(ctx, owner, lease.Instance)
	if err != nil {
		return err
	}
	defer func() {
		if result != nil {
			result = errors.Join(result, v.Kill(context.WithoutCancel(ctx), lease))
		}
	}()
	if err := v.stageNativeQualificationRestoreBackings(ctx, owner, backingCandidates); err != nil {
		return fmt.Errorf("stage captured kernel/base: %w", err)
	}
	if err := v.stageNativeQualificationRestore(ctx, owner); err != nil {
		return fmt.Errorf("stage receipt-bound snapshot inputs: %w", err)
	}
	workloads := buildWorkloadsForRestore(req)
	if err := v.stagePreBootFilesForOwner(ctx, owner, lease.Instance, workloads, req.preparedSecretsEnvJSON,
		req.preparedAPIEnvJSON, serviceDiscoveryIP, false); err != nil {
		return fmt.Errorf("stage frozen qualification runtime: %w", err)
	}
	if err := v.ownChrootRoot(root, lease); err != nil {
		return err
	}
	if err := v.stageMountHelper(root); err != nil {
		return err
	}
	if err := v.bindTunSourceForOwner(ctx, owner, root, lease.Instance); err != nil {
		return err
	}
	_ = v.registerRing(lease.Instance)
	if err := v.startJailer(ctx, lease); err != nil {
		return err
	}
	if _, err := v.bindTunDeviceInJailerForOwner(ctx, owner, root, lease.Instance, lease.UID, lease.GID); err != nil {
		return err
	}
	fenceLease := lease
	var startupCPU startupCPUProfile
	trackStartupCPU := shouldApplyStartupCPUBoost(lease, true)
	if trackStartupCPU {
		startupCPU, err = resolveStartupCPUProfile(lease.Plan, lease.CPUMillicores)
		if err != nil {
			return fmt.Errorf("resolve qualification restore CPU profile: %w", err)
		}
		fenceLease.CPUMillicores = startupCPU.StartupMillicores
	}
	if err := v.applyPreBootCgroupFence(fenceLease, workloads); err != nil {
		return fmt.Errorf("apply qualification restore cgroup fence: %w", err)
	}
	if _, err := v.loadNativeQualificationRestore(ctx, lease, runningFCVersion); err != nil {
		return fmt.Errorf("load and resume original capture: %w", err)
	}
	channels, err := v.prepareRegisteredNativeQualificationRestoreChannels(ctx, lease)
	if err != nil {
		return fmt.Errorf("prepare private qualification platform channels: %w", err)
	}
	defer channels.Close()
	if err := v.waitReadyWithProbe(ctx, lease, req.HealthcheckPath, req.HealthcheckGRPC, req.HealthcheckGRPCService, req.StartupDeadlineS); err != nil {
		return fmt.Errorf("qualification restore readiness: %w", err)
	}
	if trackStartupCPU {
		if startupCPU.StartupMillicores > startupCPU.ConfiguredMillicores {
			v.scheduleStartupCPUBoostTail(ctx, lease, workloads, startupCPU, time.Now(), StartupCPUBoostTailDuration)
		} else if err := v.restoreConfiguredCPUFence(lease, workloads, startupCPU.ConfiguredMillicores); err != nil {
			return fmt.Errorf("restore qualification CPU fence: %w", err)
		}
	}
	return nil
}
