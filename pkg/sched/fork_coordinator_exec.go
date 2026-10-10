package sched

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

const (
	// forkExecMaxInFlight bounds commands this coordinator runs at once;
	// the store allows one per fork.
	forkExecMaxInFlight = 4
	// forkExecGrace is how long past its timeout a running command may go
	// unfinished before it is failed (a scheduler that stopped mid-run).
	forkExecGrace = time.Minute
)

// forkExecs is the coordinator's optional ADR-732 fork exec runner.
type forkExecs struct {
	store    state.AppForkExecStore
	runtime  ForkExecRuntime
	mu       sync.Mutex
	inFlight int
	wg       sync.WaitGroup
}

// WithForkExecs lets the coordinator run commands for the forks it holds.
func (c *ForkCoordinator) WithForkExecs(store state.AppForkExecStore, runtime ForkExecRuntime) *ForkCoordinator {
	c.execs = &forkExecs{store: store, runtime: runtime}
	return c
}

// WaitForkExecs blocks until the commands started so far have finished.
func (c *ForkCoordinator) WaitForkExecs() {
	if c.execs != nil {
		c.execs.wg.Wait()
	}
}

// runForkExecs fails commands of ended forks, then starts queued commands
// of held forks. Each runs in its own goroutine so a long command never
// delays lease renewal.
func (c *ForkCoordinator) runForkExecs(ctx context.Context, now time.Time) {
	x := c.execs
	if x == nil {
		return
	}
	if _, err := x.store.FailOrphanedAppForkExecs(ctx, forkExecGrace, now); err != nil {
		c.log.Warn("fork coordinator: fail orphaned execs", "err", err)
	}
	for {
		x.mu.Lock()
		full := x.inFlight >= forkExecMaxInFlight
		if !full {
			x.inFlight++
		}
		x.mu.Unlock()
		if full {
			return
		}
		exec, err := x.store.ClaimNextAppForkExec(ctx, c.cfg.Owner, now)
		if err != nil {
			x.release()
			if !errors.Is(err, state.ErrNotFound) {
				c.log.Warn("fork coordinator: claim exec", "err", err)
			}
			return
		}
		x.wg.Add(1)
		go c.runForkExec(context.WithoutCancel(ctx), exec)
	}
}

func (c *ForkCoordinator) runForkExec(ctx context.Context, exec state.AppForkExec) {
	defer c.execs.wg.Done()
	defer c.execs.release()
	result, err := c.execs.runtime.ExecForkCommand(ctx, exec)
	if err != nil {
		c.log.Warn("fork coordinator: exec", "exec", exec.ID, "fork", exec.ForkID, "err", err)
	}
	p := forkExecFinish(exec, result, err)
	p.FinishedAt = c.now().UTC()
	if _, ferr := c.execs.store.FinishAppForkExec(ctx, p); ferr != nil {
		c.log.Warn("fork coordinator: finish exec", "exec", exec.ID, "err", ferr)
	}
}

func (x *forkExecs) release() {
	x.mu.Lock()
	x.inFlight--
	x.mu.Unlock()
}
