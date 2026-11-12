package fcvm

// Warm session snapshots (ADR-20261027090013 §2, §12; ADR-20261027090015
// §1). A session VM is captured once guest-init's sandbox server and resume
// hook are up: before any session identity, call, file or template command
// enters it (the ADR-171 contract), so every restore starts as clean as a
// cold boot and its processes begin after the reseed (invariant 5). A
// template's snapshot is taken before its start command (owner decision,
// 2026-11-10); restored sessions run the commands by template_start. The
// capture couples drive1 (v2 keys), so a restore sees the exact drive the
// memory was captured with. A missing, stale or failed snapshot falls back
// to a cold boot (ADR-005).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// sandboxReadyMarker is the console line guest-init prints once the sandbox
// server listens and the resume hook is served.
const sandboxReadyMarker = "guest-init: stage sandbox-ready"

// SandboxCapture names one warm session snapshot capture. Wake is the
// session machine with no snapshot and no template commands; Namespace is
// one storage path segment and CaptureID makes the capture's keys unique.
type SandboxCapture struct {
	Wake      SandboxWakeRequest
	Namespace string
	CaptureID string
}

// CaptureSandboxSnapshot cold-boots a session VM, waits for guest-init to be
// ready, snapshots it and destroys it. The snapshot restores with
// WakeSandbox on the same drives, machine shape and network mode.
func (m *Manager) CaptureSandboxSnapshot(ctx context.Context, c SandboxCapture) (*Snapshot, SnapshotInfo, error) {
	if m == nil || m.vmm == nil {
		return nil, SnapshotInfo{}, errors.New("fcvm: sandbox sessions are not configured")
	}
	if c.Wake.Snapshot != nil || c.Wake.StartCmd != "" || c.Wake.ReadyCmd != "" || c.Wake.QuietPauseAfter != 0 {
		return nil, SnapshotInfo{}, errors.New("fcvm: a sandbox capture carries no snapshot, template command or quiet pause")
	}
	if c.Namespace == "" || c.CaptureID == "" || strings.ContainsAny(c.Namespace+c.CaptureID, "/.") {
		return nil, SnapshotInfo{}, errors.New("fcvm: a sandbox capture needs one-segment namespace and capture id")
	}
	waiter, ok := m.vmm.(interface {
		WaitSandboxReady(context.Context, string, time.Duration) error
	})
	if !ok {
		return nil, SnapshotInfo{}, errors.New("fcvm: this VMM cannot capture sandbox sessions")
	}
	if _, err := m.WakeSandbox(ctx, c.Wake); err != nil {
		return nil, SnapshotInfo{}, err
	}
	defer func() {
		destroyCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), agentRunDestroyTimeout)
		defer stop()
		_ = m.Destroy(destroyCtx, c.Wake.Instance)
	}()
	if err := waiter.WaitSandboxReady(ctx, c.Wake.Instance, agentRunReadyWindow); err != nil {
		return nil, SnapshotInfo{}, fmt.Errorf("fcvm: sandbox capture: wait for guest-init: %w", err)
	}
	memKey := state.SnapshotCaptureMemKey(c.Namespace, "", c.CaptureID)
	spec := SnapshotSpec{StorageKey: memKey, VMStateStorageKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: memKey})}
	if state.SnapshotDriveKey(state.Snapshot{StorageKey: memKey}) == "" {
		return nil, SnapshotInfo{}, errors.New("fcvm: sandbox capture keys must couple drive1")
	}
	info, err := m.WarmSnapshot(ctx, c.Wake.Instance, spec)
	if err != nil {
		return nil, SnapshotInfo{}, fmt.Errorf("fcvm: sandbox capture: %w", err)
	}
	return &Snapshot{StorageKey: memKey, VMStateStorageKey: spec.VMStateStorageKey, FCVersion: m.fcVersion,
		MemBytes: info.MemBytes, Networkless: c.Wake.Network != api.SandboxNetworkRegistries}, info, nil
}

// WaitSandboxReady waits for guest-init's sandbox-ready console line.
func (v *JailerVMM) WaitSandboxReady(ctx context.Context, instance string, deadline time.Duration) error {
	return v.waitConsoleMarker(ctx, instance, sandboxReadyMarker, deadline)
}
