package fcvm

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// ExecutionTemplateCapture can boot only a new, payload-free platform guest.
// Existing instances, source, input, environment and broker grants are absent.
type ExecutionTemplateCapture struct {
	AccountID, Runtime, BaseKey, LayerKey string
	KernelDigest, BaseDigest              string
	MemoryMB, EphemeralDiskMB             int
}

// CaptureExecutionTemplate owns the complete boot/capture/retirement flight.
// Its UUID is not returned until the guest is physically gone. The template
// kind also refuses ExecuteExecution even to another trusted local caller.
func (m *Manager) CaptureExecutionTemplate(ctx context.Context, c ExecutionTemplateCapture) (snap *Snapshot, info SnapshotInfo, result error) {
	if m == nil || m.vmm == nil || m.storage == nil {
		return nil, info, ErrExecutionNotConfigured
	}
	if uuid.Validate(c.AccountID) != nil || !api.ExecutionRuntime(c.Runtime).Valid() || !api.ValidExecutionMemoryMB(c.MemoryMB) || !api.ValidExecutionEphemeralDiskMB(c.EphemeralDiskMB) || !platformTemplateKey(c.BaseKey, "base/") || !platformTemplateKey(c.LayerKey, "layers/run-") || !validBackingDigest(c.KernelDigest) || !validBackingDigest(c.BaseDigest) {
		return nil, info, errors.New("fcvm: invalid execution template envelope")
	}
	waiter, ok := m.vmm.(interface {
		WaitExecutionTemplateReady(context.Context, string, time.Duration) error
	})
	if !ok {
		return nil, info, ErrExecutionNotConfigured
	}
	if m.executionTemplateCaptureActive.Add(1) > api.ExecutionTemplateCaptureConcurrency {
		m.executionTemplateCaptureActive.Add(-1)
		return nil, info, ErrInstanceBusy
	}
	defer m.executionTemplateCaptureActive.Add(-1)
	ctx, cancel := context.WithTimeout(ctx, api.ExecutionTemplateCaptureTimeout)
	defer cancel()
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		return nil, info, err
	}
	id := uuid.NewString()
	key := state.SnapshotCaptureMemKey(uuid.Nil.String(), "", uuid.NewString())
	vmKey := state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})
	factory, ok := m.vmm.(interface {
		executionTemplateStorage(context.Context, string) (storage.StorageBackend, error)
	})
	if !ok {
		return nil, info, ErrExecutionNotConfigured
	}
	captureStore, err := factory.executionTemplateStorage(ctx, key)
	if err != nil {
		return nil, info, err
	}
	owner, ok := m.vmm.(interface {
		beginExecutionTemplateFlight(context.Context, string, string) (context.Context, error)
		finishExecutionTemplateFlight(context.Context, string, string) error
	})
	if !ok {
		return nil, info, ErrExecutionNotConfigured
	}
	ctx, err = owner.beginExecutionTemplateFlight(ctx, id, key)
	if err != nil {
		return nil, info, err
	}
	var inst *Instance
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer stop()
		if inst != nil {
			result = errors.Join(result, m.Destroy(cleanup, id))
		}
		result = errors.Join(result, owner.finishExecutionTemplateFlight(cleanup, id, key))
		if result == nil {
			m.log.Info("execution template captured", "storage_key", key, "runtime", c.Runtime, "kernel_digest", c.KernelDigest, "base_digest", c.BaseDigest)
		} else {
			snap = nil
			result = errors.Join(result, m.retireExecutionTemplate(cleanup, key))
		}
	}()
	if err := executionTemplateCaptureCheckpoint(ctx, "flight", id, key); err != nil {
		return nil, info, err
	}
	inst, err = m.Wake(ctx, WakeRequest{
		Instance: id, ExecutionOnly: true, executionTemplateOnly: true,
		AccountID: c.AccountID, Plan: api.PlanScale, Runtime: c.Runtime,
		BaseKey: c.BaseKey, LayerKey: c.LayerKey, VcpuCount: api.SizeVCPUs(c.MemoryMB),
		MemSizeMiB: c.MemoryMB, CPUMillicores: api.SizeVCPUs(c.MemoryMB) * 1000,
	})
	if err != nil {
		return nil, info, err
	}
	if err := waiter.WaitExecutionTemplateReady(ctx, id, api.ExecutionTemplateCaptureTimeout); err != nil {
		return nil, info, fmt.Errorf("fcvm: execution template readiness: %w", err)
	}
	backing, ok := m.awaitInstanceBacking(ctx, id)
	if !ok || backing.Kernel != "sha256:"+c.KernelDigest || backing.Base != "sha256:"+c.BaseDigest {
		return nil, info, ErrSnapshotBackingChanged
	}
	// This ACK proves the pristine resume contract without touching the
	// one-shot execution port (whose Accept revokes the pristine fence).
	if err := m.vmm.TriggerResumeHook(ctx, inst.Lease, time.Now().UnixNano()); err != nil {
		return nil, info, fmt.Errorf("fcvm: execution template pristine fence: %w", err)
	}
	if err := executionTemplateCaptureCheckpoint(ctx, "pristine", id, key); err != nil {
		return nil, info, err
	}
	spec := SnapshotSpec{StorageKey: key, VMStateStorageKey: vmKey, Storage: captureStore}
	if m.nativeVMM() != nil {
		// The native producer owns its backend, original descriptors and all
		// four receipts. Never wrap it in the legacy storage/cleanup path.
		spec.Storage = nil
	}
	info, err = m.WarmSnapshot(ctx, id, spec)
	if err != nil {
		return nil, info, err
	}
	if err := executionTemplateCaptureCheckpoint(ctx, "complete", id, key); err != nil {
		return nil, info, err
	}
	snap = &Snapshot{StorageKey: key, VMStateStorageKey: vmKey, FCVersion: m.fcVersion, MemBytes: info.MemBytes, Networkless: true}
	if verdict, err := m.CheckSnapshotBackingCurrent(ctx, snap, c.BaseKey); err != nil || verdict != SnapshotBackingCurrent {
		return nil, info, errors.Join(ErrSnapshotBackingUnverified, err)
	}
	return snap, info, nil
}

func validBackingDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (v *JailerVMM) WaitExecutionTemplateReady(ctx context.Context, instance string, timeout time.Duration) error {
	return v.waitConsoleMarker(ctx, instance, "guest-init: stage execution-ready", timeout)
}

func platformTemplateKey(key, prefix string) bool {
	return strings.HasPrefix(key, prefix) && path.Clean(key) == key && !strings.ContainsAny(key, "\\\x00") && !strings.Contains(key, "/../")
}
