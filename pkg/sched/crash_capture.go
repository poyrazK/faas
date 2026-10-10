package sched

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
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
	bytes, sealedKey, err := e.warmSnapshotCapture(captureCtx, ins, capture.ID, memKey, vmstateKey)
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
		MemBytes: bytes.MemBytes, CapturedAt: now, ExpiresAt: now.Add(crashCaptureRetention(capture.Trigger)),
		SealedKey: sealedKey,
	}, nil
}

// crashCaptureSealer is the VMM capability behind sealed captures.
type crashCaptureSealer interface {
	WarmSnapshotSealed(ctx context.Context, nodeID, instance, storageKey, vmstateStorageKey, captureID string) (SnapshotBytes, []byte, error)
}

// WithSealedCrashCaptures makes every crash capture sealed at the source
// (ADR-733): required on a remote storage backend, where a plaintext
// capture would reach the shared store and nodes' read-through caches.
func (e *Engine) WithSealedCrashCaptures(sealed bool) *Engine {
	e.sealCrashCaptures = sealed
	return e
}

func (e *Engine) warmSnapshotCapture(ctx context.Context, ins state.Instance, captureID, memKey, vmstateKey string) (SnapshotBytes, []byte, error) {
	if !e.sealCrashCaptures {
		bytes, err := e.vmm.WarmSnapshot(ctx, ins.NodeID, ins.ID, memKey, vmstateKey)
		return bytes, nil, err
	}
	sealer, ok := e.vmm.(crashCaptureSealer)
	if !ok {
		return SnapshotBytes{}, nil, ErrCrashStorageRemote
	}
	return sealer.WarmSnapshotSealed(ctx, ins.NodeID, ins.ID, memKey, vmstateKey, captureID)
}

// crashCaptureRetention keeps a live fork's capture only as long as a fork
// can live, and a crash snapshot for its full retention.
func crashCaptureRetention(trigger string) time.Duration {
	if trigger == state.CrashTriggerLiveFork {
		return api.LiveForkCaptureRetention
	}
	return api.CrashCaptureRetention
}

// CrashCaptureRuntime is the engine surface the crash capture coordinator
// drives.
type CrashCaptureRuntime interface {
	CaptureCrash(ctx context.Context, capture state.CrashCapture) (state.CompleteCrashCaptureParams, error)
}

// ErrCrashStorageRemote refuses a capture on a remote storage backend when
// the node's vmmd cannot seal it at the source (ADR-733): published in
// plaintext, a node's read-through cache could keep a copy nothing purges.
var ErrCrashStorageRemote = errors.New("sched: crash captures on remote storage need a vmmd that seals them")

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
	metrics *wire.CrashForkMetrics
}

// WithMetrics records capture outcomes on m.
func (c *CrashCaptureCoordinator) WithMetrics(m *wire.CrashForkMetrics) *CrashCaptureCoordinator {
	c.metrics = m
	return c
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
	stale, err := c.store.FailStaleCrashCaptures(ctx, now.Add(-api.CrashCaptureTimeout), now)
	if err != nil {
		c.log.Warn("crash capture coordinator: fail stale", "err", err)
	}
	for _, capture := range stale {
		c.metrics.CrashCaptureFinished(capture.Trigger, "capture_timeout", 0)
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
	started := c.now()
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
		c.metrics.CrashCaptureFinished(capture.Trigger, code, 0)
		if _, ferr := c.store.FailCrashCapture(ctx, capture.ID, code, message, c.now().UTC()); ferr != nil {
			c.log.Warn("crash capture coordinator: record failure", "capture", capture.ID, "err", ferr)
		}
		return
	}
	if _, err := c.store.CompleteCrashCapture(ctx, done); err != nil {
		c.log.Warn("crash capture coordinator: complete", "capture", capture.ID, "err", err)
		return
	}
	c.metrics.CrashCaptureFinished(capture.Trigger, "ready", c.now().Sub(started))
}
