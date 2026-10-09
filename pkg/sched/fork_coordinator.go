package sched

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// ForkRuntime is the engine surface the fork coordinator drives (ADR-732).
type ForkRuntime interface {
	RestoreFork(ctx context.Context, fork state.AppFork) (ForkRestore, error)
	DestroyForkInstance(ctx context.Context, appID, instanceID string) error
}

// ForkCoordinatorConfig tunes the coordinator. Zero values take the
// defaults below.
type ForkCoordinatorConfig struct {
	Owner         string
	Lease         time.Duration
	Poll          time.Duration
	MaxConcurrent int
	// Metrics is optional; nil records nothing.
	Metrics *wire.CrashForkMetrics
}

const (
	defaultForkLease         = 30 * time.Second
	defaultForkPoll          = 2 * time.Second
	defaultForkMaxConcurrent = 8
	forkTeardownBatch        = 50
)

// ForkCoordinator owns every fork from claim to teardown (ADR-732):
//
//   - expire queued forks that ran out of time or were cancelled unclaimed;
//   - destroy the instance of every held fork that reached its TTL or was
//     cancelled, then finish the row;
//   - take over forks whose scheduler stopped: a running fork is adopted
//     and lives out its TTL, a fork caught mid-restore is destroyed and
//     failed;
//   - renew the leases it holds;
//   - claim queued forks up to MaxConcurrent and restore them.
//
// All fork state changes are lease-guarded in the store, so two schedds can
// run coordinators without ever acting on the same fork.
type ForkCoordinator struct {
	store   state.AppForkLifecycleStore
	runtime ForkRuntime
	cfg     ForkCoordinatorConfig
	log     *slog.Logger
	now     func() time.Time

	mu   sync.Mutex
	held map[string]string // fork id -> lease token
}

// NewForkCoordinator builds a coordinator; Run starts it.
func NewForkCoordinator(store state.AppForkLifecycleStore, runtime ForkRuntime, cfg ForkCoordinatorConfig, log *slog.Logger) *ForkCoordinator {
	if cfg.Owner == "" {
		cfg.Owner = "schedd"
	}
	if cfg.Lease <= 0 {
		cfg.Lease = defaultForkLease
	}
	if cfg.Poll <= 0 {
		cfg.Poll = defaultForkPoll
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = defaultForkMaxConcurrent
	}
	if log == nil {
		log = slog.Default()
	}
	return &ForkCoordinator{store: store, runtime: runtime, cfg: cfg, log: log, now: time.Now, held: map[string]string{}}
}

// Run ticks until ctx ends.
func (c *ForkCoordinator) Run(ctx context.Context) {
	ticker := time.NewTicker(c.cfg.Poll)
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

// Tick runs one pass. Exported so tests drive the coordinator without a
// clock.
func (c *ForkCoordinator) Tick(ctx context.Context) {
	now := c.now().UTC()
	if _, err := c.store.ExpireUnclaimedAppForks(ctx, now); err != nil {
		c.log.Warn("fork coordinator: expire unclaimed", "err", err)
	}
	c.teardownDue(ctx, now)
	c.takeOverAbandoned(ctx, now)
	c.renewHeld(ctx, now)
	c.claimQueued(ctx, now)
}

// Held reports how many forks this coordinator holds.
func (c *ForkCoordinator) Held() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.held)
}

func (c *ForkCoordinator) hold(fork state.AppFork) {
	if fork.LeaseToken == nil {
		return
	}
	c.mu.Lock()
	c.held[fork.ID] = *fork.LeaseToken
	c.mu.Unlock()
}

func (c *ForkCoordinator) drop(forkID string) {
	c.mu.Lock()
	delete(c.held, forkID)
	c.mu.Unlock()
}

func (c *ForkCoordinator) teardownDue(ctx context.Context, now time.Time) {
	due, err := c.store.AppForksDueForTeardown(ctx, c.cfg.Owner, now, forkTeardownBatch)
	if err != nil {
		c.log.Warn("fork coordinator: list due", "err", err)
		return
	}
	for _, fork := range due {
		status := state.AppForkExpired
		if fork.CancelRequested != nil {
			status = state.AppForkCancelled
		}
		c.end(ctx, fork, status, "", "")
	}
}

func (c *ForkCoordinator) takeOverAbandoned(ctx context.Context, now time.Time) {
	for range forkTeardownBatch {
		fork, err := c.store.TakeOverAbandonedAppFork(ctx, c.cfg.Owner, now, c.cfg.Lease)
		if errors.Is(err, state.ErrNotFound) {
			return
		}
		if err != nil {
			c.log.Warn("fork coordinator: take over", "err", err)
			return
		}
		if fork.Status == state.AppForkRunning && fork.InstanceID != nil {
			// The VM outlived its scheduler; adopt it until its TTL.
			c.hold(fork)
			c.log.Info("fork coordinator: adopted running fork", "fork", fork.ID)
			continue
		}
		c.end(ctx, fork, state.AppForkFailed, "scheduler_lost", "the scheduler restoring this fork stopped before it finished")
	}
}

func (c *ForkCoordinator) renewHeld(ctx context.Context, now time.Time) {
	c.mu.Lock()
	held := make(map[string]string, len(c.held))
	for id, token := range c.held {
		held[id] = token
	}
	c.mu.Unlock()
	for id, token := range held {
		if _, err := c.store.RenewAppForkLease(ctx, id, token, now, c.cfg.Lease); err != nil {
			if errors.Is(err, state.ErrAppForkLeaseLost) {
				c.drop(id)
				continue
			}
			c.log.Warn("fork coordinator: renew", "fork", id, "err", err)
		}
	}
}

func (c *ForkCoordinator) claimQueued(ctx context.Context, now time.Time) {
	for c.Held() < c.cfg.MaxConcurrent {
		fork, err := c.store.ClaimNextAppFork(ctx, c.cfg.Owner, now, c.cfg.Lease)
		if errors.Is(err, state.ErrNotFound) {
			return
		}
		if err != nil {
			c.log.Warn("fork coordinator: claim", "err", err)
			return
		}
		c.hold(fork)
		c.cfg.Metrics.ForkClaimed(forkSource(fork), now.Sub(fork.CreatedAt))
		c.restore(ctx, fork)
	}
}

// forkSource labels fork metrics by what the fork restores.
func forkSource(fork state.AppFork) string {
	if fork.CrashCaptureID != nil {
		return "crash_capture"
	}
	return "deployment"
}

func (c *ForkCoordinator) restore(ctx context.Context, fork state.AppFork) {
	started := c.now()
	restored, err := c.runtime.RestoreFork(ctx, fork)
	if err != nil {
		code, message := ForkFailureCode(err)
		c.cfg.Metrics.ForkRestoreFinished(forkSource(fork), code, c.now().Sub(started))
		c.log.Warn("fork coordinator: restore failed", "fork", fork.ID, "code", code, "err", err)
		c.finish(ctx, fork, state.AppForkFailed, code, message)
		return
	}
	if _, err := c.store.MarkAppForkRunning(ctx, fork.ID, *fork.LeaseToken, restored.SnapshotID, restored.InstanceID, c.now().UTC()); err == nil {
		c.cfg.Metrics.ForkRestoreFinished(forkSource(fork), "running", c.now().Sub(started))
	} else {
		// The lease moved on (cancelled and swept, or taken over) while
		// the VM booted. Nobody else knows this instance, so destroy it.
		c.log.Warn("fork coordinator: mark running", "fork", fork.ID, "err", err)
		if derr := c.runtime.DestroyForkInstance(ctx, fork.AppID, restored.InstanceID); derr != nil {
			c.log.Error("fork coordinator: destroy orphaned fork instance", "fork", fork.ID, "instance", restored.InstanceID, "err", derr)
		}
		c.drop(fork.ID)
	}
}

// end destroys a held fork's instance (if it has one) and finishes the row.
// When the destroy fails the row keeps its lease, so the next tick retries.
func (c *ForkCoordinator) end(ctx context.Context, fork state.AppFork, status state.AppForkStatus, code, message string) {
	if fork.InstanceID != nil {
		if err := c.runtime.DestroyForkInstance(ctx, fork.AppID, *fork.InstanceID); err != nil {
			c.log.Error("fork coordinator: destroy fork instance", "fork", fork.ID, "err", err)
			c.hold(fork)
			return
		}
	}
	c.finish(ctx, fork, status, code, message)
}

func (c *ForkCoordinator) finish(ctx context.Context, fork state.AppFork, status state.AppForkStatus, code, message string) {
	if fork.LeaseToken == nil {
		return
	}
	_, err := c.store.FinishAppFork(ctx, state.FinishAppForkParams{
		ForkID: fork.ID, LeaseToken: *fork.LeaseToken, Status: status,
		FailureCode: code, FailureMessage: message, FinishedAt: c.now().UTC(),
	})
	if err != nil && !errors.Is(err, state.ErrAppForkLeaseLost) {
		c.log.Warn("fork coordinator: finish", "fork", fork.ID, "status", status, "err", err)
		return
	}
	c.drop(fork.ID)
}
