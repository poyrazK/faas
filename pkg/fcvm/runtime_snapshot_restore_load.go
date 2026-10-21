package fcvm

// adr: 595. Protected snapshot loading remains private until measured acceptance.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type verifiedSnapshotRestore struct {
	request            SnapshotRestoreInputs
	inputs             VerifiedSnapshotInputs
	accepted           bool // API acknowledgment only; never a native consumption receipt.
	observation        *RuntimeSnapshotHandoffObservation
	keepPaused         bool
	started, attempted bool
	resumeAttempted    bool
	resumeEvidence     runtimeadmission.SnapshotResumeEvidence
}

// RestoreSnapshotVerified uses retained catalog bytes through the native load
// path. Callers still need kernel/backing qualification. No public capability,
// paused-promotion authority or measured snapshot receipt is enabled here.
func (v *JailerVMM) RestoreSnapshotVerified(ctx context.Context, lease Lease, req SnapshotRestoreInputs, keepPaused bool) (err error) {
	ctx, spec, flight, err := v.prepareVerifiedSnapshotLoad(ctx, lease, req, keepPaused)
	if err != nil {
		return err
	}
	defer func() {
		flight.finish()
		if err != nil {
			err = errors.Join(err, v.Kill(context.WithoutCancel(ctx), lease))
		}
	}()
	return v.Restore(ctx, lease, spec)
}

func (v *JailerVMM) prepareVerifiedSnapshotLoad(ctx context.Context, lease Lease, req SnapshotRestoreInputs, keepPaused bool) (context.Context, RestoreSpec, *snapshotSourceFlight, error) {
	req.Capture, req.Sources = req.Capture.Clone(), slices.Clone(req.Sources)
	req.Runtime = cloneSnapshotRestoreRuntime(req.Runtime)
	if err := checkVerifiedSnapshotLoadRequest(lease, req); err != nil {
		return nil, RestoreSpec{}, nil, err
	}
	inputs, err := v.PrepareSnapshotRestoreInputs(ctx, lease, req)
	if err != nil {
		return nil, RestoreSpec{}, nil, err
	}
	ctx, flight, err := v.beginSnapshotSourceFlight(ctx, lease)
	if err != nil {
		return nil, RestoreSpec{}, nil, errors.Join(err, v.releaseRuntimeSources(lease.Instance))
	}
	plan := &verifiedSnapshotRestore{request: req, inputs: inputs, keepPaused: keepPaused}
	flight.handoff.mu.Lock()
	if flight.handoff.closed || inputs.owner != flight.handoff || flight.handoff.restoreLoad != nil {
		flight.handoff.mu.Unlock()
		flight.finish()
		return nil, RestoreSpec{}, nil, errors.Join(runtimeadmission.ErrStale, v.releaseRuntimeSources(lease.Instance))
	}
	flight.handoff.restoreLoad = plan
	flight.handoff.mu.Unlock()
	spec := snapshotRestoreSpec(lease, req, keepPaused)
	spec.verifiedSnapshot = plan
	return ctx, spec, flight, nil
}

func checkVerifiedSnapshotLoadRequest(lease Lease, req SnapshotRestoreInputs) error {
	return checkVerifiedSnapshotLoadRequestAt(lease, req, time.Now())
}

func checkVerifiedSnapshotLoadRequestAt(lease Lease, req SnapshotRestoreInputs, clock time.Time) error {
	if lease.IsBuilder || req.Runtime.SkipReady || req.Runtime.AppTask || req.Binding.SnapshotCaptureToken == "" {
		return runtimeadmission.ErrInvalid
	}
	if err := checkSnapshotRestoreInputLayoutAt(lease, req, clock); err != nil {
		return err
	}
	evidence := runtimeadmission.SnapshotRestoreEvidence{Version: runtimeadmission.SnapshotRestoreVersion, CaptureToken: req.Binding.SnapshotCaptureToken, FCVersion: req.Snapshot.FCVersion, Capture: req.Capture}
	if err := evidence.Check(req.Binding, req.Sources, req.Snapshot.StorageKey, req.Snapshot.VMStateStorageKey, req.Snapshot.FCVersion, int64(req.Runtime.MemSizeMiB)<<20, clock); err != nil {
		return err
	}
	for i, workload := range req.Runtime.Workloads {
		expected := DriveLayerMain
		if i > 0 {
			expected = fmt.Sprintf("%s%d", DriveSidecarPrefix, i-1)
		}
		if workload.DriveID != "" && workload.DriveID != expected {
			return runtimeadmission.ErrUnavailable
		}
	}
	return nil
}

func snapshotRestoreSpec(lease Lease, req SnapshotRestoreInputs, keepPaused bool) RestoreSpec {
	r := cloneSnapshotRestoreRuntime(req.Runtime)
	return RestoreSpec{KernelKey: r.KernelKey, BaseKey: r.BaseKey, LayerKey: r.LayerKey, Tap: r.Tap,
		Workloads: r.Workloads, SecretsEnvJSON: r.SecretsEnvJSON, APIEnvJSON: r.APIEnvJSON,
		ServiceDiscoveryIP: r.ServiceDiscoveryIP, VsockDevice: NewVsockDevice(lease.Slot),
		StorageKey: req.Snapshot.StorageKey, VMStateStorageKey: req.Snapshot.VMStateStorageKey,
		HealthcheckPath: r.HealthcheckPath, HealthcheckGRPC: r.HealthcheckGRPC,
		HealthcheckGRPCService: r.HealthcheckGRPCService, StartupDeadlineS: r.StartupDeadlineS,
		KeepPaused: keepPaused}
}

func (v *JailerVMM) checkVerifiedSnapshotLoad(ctx context.Context, lease Lease, spec RestoreSpec) error {
	return v.checkVerifiedSnapshotLoadAt(ctx, lease, spec, time.Now())
}

func (v *JailerVMM) checkVerifiedSnapshotLoadAt(ctx context.Context, lease Lease, spec RestoreSpec, clock time.Time) error {
	plan := spec.verifiedSnapshot
	if plan == nil {
		handoff, err := v.runtimeDriveHandoff(lease)
		if err != nil {
			return err
		}
		if handoff != nil {
			handoff.mu.Lock()
			protected := handoff.restoreCapture != nil
			handoff.mu.Unlock()
			if protected {
				return runtimeadmission.ErrUnavailable
			}
		}
		return ctx.Err()
	}
	handoff, err := v.runtimeDriveHandoff(lease)
	if err != nil || handoff == nil || handoff != plan.inputs.owner {
		return errors.Join(runtimeadmission.ErrStale, err)
	}
	handoff.mu.Lock()
	defer handoff.mu.Unlock()
	// The preparation flight ends on return from RestoreSnapshotVerified;
	// accepted backing stays owned for later receipt observation and retirement.
	if handoff.closed || handoff.restoreLoad != plan || handoff.restorePreparation == nil && !plan.accepted {
		return runtimeadmission.ErrStale
	}
	expected := snapshotRestoreSpec(lease, plan.request, plan.keepPaused)
	expected.verifiedSnapshot = plan
	if !reflect.DeepEqual(spec, expected) {
		return runtimeadmission.ErrStale
	}
	return errors.Join(checkVerifiedSnapshotLoadRequestAt(lease, plan.request, clock), ctx.Err())
}

func (v *JailerVMM) cancelSnapshotRestoreLoad(lease Lease) error {
	handoff, err := v.runtimeDriveHandoff(lease)
	if err != nil || handoff == nil {
		return err
	}
	handoff.mu.Lock()
	flight := handoff.restorePreparation
	if flight != nil {
		flight.cancel()
	}
	handoff.mu.Unlock()
	if flight != nil {
		<-flight.done
	}
	return nil
}

func (v *JailerVMM) beginProtectedNativeRestore(ctx context.Context, lease Lease, spec RestoreSpec) error {
	if err := v.checkVerifiedSnapshotLoad(ctx, lease, spec); err != nil {
		return err
	}
	plan := spec.verifiedSnapshot
	if plan == nil {
		return nil
	}
	plan.inputs.owner.mu.Lock()
	defer plan.inputs.owner.mu.Unlock()
	if plan.started {
		return runtimeadmission.ErrReplay
	}
	plan.started = true
	return nil
}

func cloneSnapshotRestoreRuntime(in ColdBootSpec) ColdBootSpec {
	out := in
	out.SecretsEnvJSON, out.APIEnvJSON = slices.Clone(in.SecretsEnvJSON), slices.Clone(in.APIEnvJSON)
	out.Workloads = slices.Clone(in.Workloads)
	for i := range out.Workloads {
		w := &out.Workloads[i]
		w.Cmd, w.Entrypoint, w.DependsOn = slices.Clone(w.Cmd), slices.Clone(w.Entrypoint), slices.Clone(w.DependsOn)
		w.SealedEnv, w.SealedSecrets = cloneSnapshotRestoreSealedEnv(w.SealedEnv), cloneSnapshotRestoreSealedEnv(w.SealedSecrets)
		w.GrantedEnvNames, w.preparedEnvJSON = slices.Clone(w.GrantedEnvNames), slices.Clone(w.preparedEnvJSON)
		w.StartupProbe, w.LivenessProbe, w.ReadinessProbe = cloneSnapshotRestoreProbe(w.StartupProbe), cloneSnapshotRestoreProbe(w.LivenessProbe), cloneSnapshotRestoreProbe(w.ReadinessProbe)
	}
	return out
}

func cloneSnapshotRestoreProbe(in *api.SidecarProbe) *api.SidecarProbe {
	out := cloneWorkloadProbe(in)
	if out != nil && out.ImageTiming != nil {
		timing := *out.ImageTiming
		out.ImageTiming = &timing
	}
	return out
}

func cloneSnapshotRestoreSealedEnv(in []SealedEnvEntry) []SealedEnvEntry {
	out := slices.Clone(in)
	for i := range out {
		out[i].Ciphertext = slices.Clone(out[i].Ciphertext)
	}
	return out
}
