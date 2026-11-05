package fcvm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Preset snapshots (ADR-20261027090010 §2, rollout step 9). A preset's agent VM is
// captured while guest-init waits for its run: after boot, before any
// tenant identity, input or secret enters it (the ADR-171 contract). The
// capture freezes drive1 with memory and device state (v2 keys), and the
// input drive has a fixed capacity, so every restore sees the drives it was
// captured with. A missing, stale or failed snapshot falls back to a cold
// boot of the same image (ADR-005).

// agentRunReadyMarker is the console line guest-init prints once it listens
// for its run and serves the resume hook.
const agentRunReadyMarker = "guest-init: stage agent-ready"

// agentRunReadyWindow bounds a capture boot: Wake returns once Firecracker is
// up, and guest-init still has to start.
const agentRunReadyWindow = 60 * time.Second

// AgentRunCapture names one preset snapshot capture. Wake is the preset's
// machine; it carries no relay, input or snapshot, because nothing of a run
// may enter a captured VM. Namespace is one storage path segment, such as
// "agent-preset-codex"; CaptureID makes the capture's keys unique.
type AgentRunCapture struct {
	Wake      AgentRunWakeRequest
	Namespace string
	CaptureID string
}

// CaptureAgentRunSnapshot cold-boots the preset, waits for guest-init to be
// ready, snapshots it and destroys it. The returned snapshot restores with
// WakeAgentRun.
func (m *Manager) CaptureAgentRunSnapshot(ctx context.Context, c AgentRunCapture) (*Snapshot, SnapshotInfo, error) {
	if m == nil || m.vmm == nil {
		return nil, SnapshotInfo{}, ErrAgentRunNotConfigured
	}
	if c.Wake.Relay != nil || c.Wake.InputKey != "" || c.Wake.Snapshot != nil {
		return nil, SnapshotInfo{}, errors.New("fcvm: an agent run capture carries no relay, input or snapshot")
	}
	if c.Namespace == "" || c.CaptureID == "" || strings.ContainsAny(c.Namespace+c.CaptureID, "/.") {
		return nil, SnapshotInfo{}, errors.New("fcvm: an agent run capture needs one-segment namespace and capture id")
	}
	waiter, ok := m.vmm.(interface {
		WaitAgentRunReady(context.Context, string, time.Duration) error
	})
	if !ok {
		return nil, SnapshotInfo{}, errors.New("fcvm: this VMM cannot capture agent runs")
	}
	if _, err := m.WakeAgentRun(ctx, c.Wake); err != nil {
		return nil, SnapshotInfo{}, err
	}
	defer func() {
		destroyCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), agentRunDestroyTimeout)
		defer stop()
		_ = m.Destroy(destroyCtx, c.Wake.Instance)
	}()
	if err := waiter.WaitAgentRunReady(ctx, c.Wake.Instance, agentRunReadyWindow); err != nil {
		return nil, SnapshotInfo{}, fmt.Errorf("fcvm: agent run capture: wait for guest-init: %w", err)
	}
	memKey := state.SnapshotCaptureMemKey(c.Namespace, "", c.CaptureID)
	spec := SnapshotSpec{StorageKey: memKey, VMStateStorageKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: memKey})}
	if state.SnapshotDriveKey(state.Snapshot{StorageKey: memKey}) == "" {
		return nil, SnapshotInfo{}, errors.New("fcvm: agent run capture keys must couple drive1")
	}
	info, err := m.WarmSnapshot(ctx, c.Wake.Instance, spec)
	if err != nil {
		return nil, SnapshotInfo{}, fmt.Errorf("fcvm: agent run capture: %w", err)
	}
	return &Snapshot{StorageKey: memKey, VMStateStorageKey: spec.VMStateStorageKey, FCVersion: m.fcVersion,
		MemBytes: info.MemBytes, Networkless: true}, info, nil
}

// WaitAgentRunReady waits for guest-init's ready marker on the VM's console,
// which vmmd owns, as WaitBuilderReady does for builders.
func (v *JailerVMM) WaitAgentRunReady(ctx context.Context, instance string, deadline time.Duration) error {
	v.mu.Lock()
	rec := v.recs[instance]
	v.mu.Unlock()
	if rec == nil {
		return fmt.Errorf("vmm: wait agent run ready: instance %s not found", instance)
	}
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if consoleContains(rec.consolePath, agentRunReadyMarker) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return context.DeadlineExceeded
		case <-rec.done:
			if consoleContains(rec.consolePath, agentRunReadyMarker) {
				return nil
			}
			return errors.New("vmm: wait agent run ready: the guest exited")
		case <-ticker.C:
		}
	}
}
