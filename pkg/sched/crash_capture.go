package sched

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ErrCrashInstanceGone means the instance a crash capture targeted is no
// longer a running serving instance of the app.
var ErrCrashInstanceGone = errors.New("sched: crash capture instance is gone")

// CaptureCrash captures the still-running instance a crash capture names
// (ADR-733) with the pause → snapshot → resume path, into a fresh capture
// key under the deployment's capture directory. The instance keeps serving.
// The capture is never a snapshots row, so no wake can restore it.
func (e *Engine) CaptureCrash(ctx context.Context, capture state.CrashCapture) (state.CompleteCrashCaptureParams, error) {
	release := e.lockApp(capture.AppID)
	defer release()
	ins, err := e.store.InstanceByID(ctx, capture.InstanceID)
	if err != nil || ins.AppID != capture.AppID || state.IsFork(ins.Mode) || ins.State != string(state.StateRunning) || ins.NodeID == "" {
		return state.CompleteCrashCaptureParams{}, ErrCrashInstanceGone
	}
	memKey := state.SnapshotCaptureMemKey(ins.DeploymentID, state.SnapshotTierWarm, capture.ID)
	vmstateKey := state.SnapshotVMStateKey(state.Snapshot{StorageKey: memKey})
	captureCtx, cancel := context.WithTimeout(ctx, SnapshotBudgetFor(ins.RAMMB))
	bytes, err := e.vmm.WarmSnapshot(captureCtx, ins.NodeID, ins.ID, memKey, vmstateKey)
	cancel()
	if err != nil {
		// Same posture as the warm capture path: after a failed
		// pause/snapshot the VM's state is unknown, so it is destroyed
		// rather than left serving.
		if derr := e.vmm.Destroy(ctx, ins.NodeID, ins.ID); derr != nil {
			e.log.Warn("sched: crash capture: destroy after snapshot failure", "instance", ins.ID, "err", derr)
		}
		e.ledger.Release(ins.ID)
		e.transitionWithKind(ctx, ins.ID, ins.AppID, state.StateStopped, "crash_capture_error", "crash_snapshot_failed")
		return state.CompleteCrashCaptureParams{}, fmt.Errorf("sched: crash capture: %w", err)
	}
	now := time.Now().UTC()
	return state.CompleteCrashCaptureParams{
		ID: capture.ID, StorageKey: memKey, VMStateStorageKey: vmstateKey, FCVersion: e.fcVer,
		MemBytes: bytes.MemBytes, CapturedAt: now, ExpiresAt: now.Add(api.CrashCaptureRetention),
	}, nil
}

// CrashCaptureRuntime is the engine surface the crash capture coordinator
// drives.
type CrashCaptureRuntime interface {
	CaptureCrash(ctx context.Context, capture state.CrashCapture) (state.CompleteCrashCaptureParams, error)
}

// ErrCrashStorageRemote refuses captures on a remote storage backend
// (ADR-733): imaged encrypts captures through the backend, but a node's
// read-through cache could keep a plaintext copy it cannot purge.
var ErrCrashStorageRemote = errors.New("sched: crash captures need the local storage backend")

type refusingCrashRuntime struct{ err error }

func (r refusingCrashRuntime) CaptureCrash(context.Context, state.CrashCapture) (state.CompleteCrashCaptureParams, error) {
	return state.CompleteCrashCaptureParams{}, r.err
}

// RefuseCrashCaptures is a runtime that fails every capture with err, so
// requests end failed instead of holding the app's one in-flight slot.
func RefuseCrashCaptures(err error) CrashCaptureRuntime { return refusingCrashRuntime{err: err} }

// CrashCaptureCoordinator claims requested crash captures and captures them
// one at a time (ADR-733). Captures pause a serving instance, so they are
// never run in parallel on one scheduler.
type CrashCaptureCoordinator struct {
	store   state.CrashCaptureStore
	runtime CrashCaptureRuntime
	poll    time.Duration
	log     *slog.Logger
	now     func() time.Time
}

// NewCrashCaptureCoordinator builds a coordinator polling every poll.
func NewCrashCaptureCoordinator(store state.CrashCaptureStore, runtime CrashCaptureRuntime, poll time.Duration, log *slog.Logger) *CrashCaptureCoordinator {
	if poll <= 0 {
		poll = 2 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &CrashCaptureCoordinator{store: store, runtime: runtime, poll: poll, log: log, now: time.Now}
}

// Run ticks until ctx ends.
func (c *CrashCaptureCoordinator) Run(ctx context.Context) {
	ticker := time.NewTicker(c.poll)
	defer ticker.Stop()
	for {
		c.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Tick fails captures stuck past the timeout, then captures every requested
// one.
func (c *CrashCaptureCoordinator) Tick(ctx context.Context) {
	now := c.now().UTC()
	if _, err := c.store.FailStaleCrashCaptures(ctx, now.Add(-api.CrashCaptureTimeout), now); err != nil {
		c.log.Warn("crash capture coordinator: fail stale", "err", err)
	}
	for ctx.Err() == nil {
		capture, err := c.store.ClaimNextCrashCapture(ctx, c.now().UTC())
		if errors.Is(err, state.ErrNotFound) {
			return
		}
		if err != nil {
			c.log.Warn("crash capture coordinator: claim", "err", err)
			return
		}
		c.capture(ctx, capture)
	}
}

func (c *CrashCaptureCoordinator) capture(ctx context.Context, capture state.CrashCapture) {
	done, err := c.runtime.CaptureCrash(ctx, capture)
	if err != nil {
		code, message := "capture_failed", "the instance could not be captured"
		switch {
		case errors.Is(err, ErrCrashInstanceGone):
			code, message = "instance_gone", "the instance had stopped before it could be captured"
		case errors.Is(err, ErrCrashStorageRemote):
			code, message = "storage_unsupported", "crash snapshots are not available on this node's storage yet"
		}
		c.log.Warn("crash capture coordinator: capture failed", "capture", capture.ID, "code", code, "err", err)
		if _, ferr := c.store.FailCrashCapture(ctx, capture.ID, code, message, c.now().UTC()); ferr != nil {
			c.log.Warn("crash capture coordinator: record failure", "capture", capture.ID, "err", ferr)
		}
		return
	}
	if _, err := c.store.CompleteCrashCapture(ctx, done); err != nil {
		c.log.Warn("crash capture coordinator: complete", "capture", capture.ID, "err", err)
	}
}
