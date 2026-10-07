package fcvm

// adr: 595 Forward a catalog-bound serving restore through its measured owner.

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type consumedSnapshotVMM interface {
	consumedSourceVMM
	RuntimeSnapshotRestoreVersion() uint32
	RestoreSnapshotVerified(context.Context, Lease, SnapshotRestoreInputs, bool) error
	ObservedRuntimeSnapshotConsumption(context.Context, Lease) (runtimeadmission.ArtifactConsumption, runtimeadmission.SnapshotConsumption, error)
}

type resumedSnapshotVMM interface {
	consumedSnapshotVMM
	PromoteSnapshotVerified(context.Context, Lease, runtimeadmission.Promotion) (RuntimeSnapshotResumeObservation, error)
}

// Physical restore/promotion acceptance is still required before advertising.
func (v *JailerVMM) RuntimeSnapshotRestoreVersion() uint32 { return 0 }

func (m *Manager) runtimeSnapshotRestoreVersion() uint32 {
	if v, ok := m.vmm.(consumedSnapshotVMM); ok && v.SupportsRuntimeArtifactConsumption() {
		return v.RuntimeSnapshotRestoreVersion()
	}
	return 0
}

func checkAdmittedSnapshotRestore(b runtimeadmission.Binding, req WakeRequest) error {
	if req.SnapshotRestore == nil {
		if b.SnapshotCaptureToken != "" {
			return runtimeadmission.ErrInvalid
		}
		return nil
	}
	if req.Snapshot == nil {
		return runtimeadmission.ErrUnavailable
	}
	return req.SnapshotRestore.Check(b, req.ArtifactSources, req.Snapshot.StorageKey, req.Snapshot.VMStateStorageKey, req.Snapshot.FCVersion, int64(wakeGuestMemoryMiB(req))<<20, time.Now())
}

func (m *Manager) restoreWithAdmission(ctx context.Context, lease Lease, nc netns.Config, req WakeRequest, spec RestoreSpec, discoveryIP string) error {
	if req.SnapshotRestore == nil {
		return m.vmm.Restore(ctx, lease, spec)
	}
	v, ok := m.vmm.(consumedSnapshotVMM)
	if !ok || m.runtimeSnapshotRestoreVersion() != runtimeadmission.SnapshotRestoreVersion || req.admission == nil {
		return runtimeadmission.ErrUnavailable
	}
	if err := checkAdmittedSnapshotRestore(*req.admission, req); err != nil {
		return err
	}
	if req.KeepPaused {
		if _, ok := m.vmm.(resumedSnapshotVMM); !ok {
			return runtimeadmission.ErrUnavailable
		}
	}
	inputs := SnapshotRestoreInputs{Binding: *req.admission, Capture: req.SnapshotRestore.Capture.Clone(), Snapshot: *req.Snapshot,
		Runtime: m.coldBootSpecForWake(nc, req, discoveryIP), Sources: req.ArtifactSources}
	return v.RestoreSnapshotVerified(ctx, lease, inputs, req.KeepPaused)
}

func (m *Manager) admittedRuntimeConsumption(ctx context.Context, binding runtimeadmission.Binding, req WakeRequest, inst *Instance) (runtimeadmission.ArtifactConsumption, runtimeadmission.SnapshotConsumption, error) {
	if inst.Method != WakeRestore || req.SnapshotRestore == nil {
		drives, err := m.admittedArtifactConsumption(ctx, binding, inst)
		return drives, runtimeadmission.SnapshotConsumption{}, err
	}
	v, ok := m.vmm.(consumedSnapshotVMM)
	if !ok || m.runtimeSnapshotRestoreVersion() != runtimeadmission.SnapshotRestoreVersion {
		return runtimeadmission.ArtifactConsumption{}, runtimeadmission.SnapshotConsumption{}, runtimeadmission.ErrUnavailable
	}
	drives, proof, err := v.ObservedRuntimeSnapshotConsumption(ctx, inst.Lease)
	if err == nil {
		err = proof.CheckEvidence(binding, drives, inst.Paused, *req.SnapshotRestore, time.Now())
	}
	if err = errors.Join(err, ctx.Err()); err != nil {
		return runtimeadmission.ArtifactConsumption{}, runtimeadmission.SnapshotConsumption{}, err
	}
	return drives, proof, nil
}

func (m *Manager) coldBootSpecForWake(nc netns.Config, req WakeRequest, serviceDiscoveryIP string) ColdBootSpec {
	return ColdBootSpec{
		KernelKey: m.paths.Kernel,
		BaseKey:   req.BaseKey,
		// LayerKey is the legacy single-workload path. When
		// Workloads is non-empty (PR-B / sidecars present),
		// buildWorkloadsForColdBoot copies req.LayerKey into
		// Workloads[0].StorageKey; spec.LayerKey must be empty
		// here so the ColdBootSpec.Validate() "LayerKey must be
		// empty when Workloads is set" check doesn't reject
		// the spec. The Validate contract is the load-bearing
		// guard against double-spec'ing the main workload.
		LayerKey:   layerKeyForColdBoot(req),
		VcpuCount:  req.VcpuCount,
		MemSizeMiB: wakeGuestMemoryMiB(req),
		Tap:        nc.Tap,
		// Per-deployment readiness action. The HTTP path and gRPC
		// mode/service are forwarded together; both target :8080.
		HealthcheckPath:        req.HealthcheckPath,
		HealthcheckGRPC:        req.HealthcheckGRPC,
		HealthcheckGRPCService: req.HealthcheckGRPCService,
		StartupDeadlineS:       req.StartupDeadlineS,
		ExecutionMode:          req.ExecutionMode,
		// One-shot guests use a vsock dispatch protocol rather than the app
		// HTTP listener. App tasks remain networked; executions do not.
		SkipReady: req.ExportDir != "" || req.ExecutionOnly || req.AppTaskOnly,
		// Issue #463 / ADR-069 / PR-B: per-workload drives
		// (main + sidecars). buildWorkloadsForColdBoot emits an
		// empty slice on the legacy single-workload path so
		// BootColdBoot falls through to the LayerKey branch.
		Workloads:          buildWorkloadsForColdBoot(req),
		SecretsEnvJSON:     req.preparedSecretsEnvJSON,
		APIEnvJSON:         req.preparedAPIEnvJSON,
		ServiceDiscoveryIP: serviceDiscoveryIP,
		Networkless:        req.ExecutionOnly,
		AppTask:            req.AppTaskOnly,
	}
}
