package sched

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"syscall"

	"github.com/onebox-faas/faas/pkg/state"
)

// workerStopTracker records reconciler-owned worker stops that are running
// in the background. A stop spends up to the workload's grace period waiting
// for the process to exit; running it inline would hold the scaling tick, and
// with it every other app's scaling decision, for that long.
//
// The set is per schedd and in memory. A stop interrupted by a restart leaves
// a RUNNING row; the next reconcile either keeps it or stops it again.
type workerStopTracker struct {
	mu       sync.Mutex
	stopping map[string]struct{}
	wg       sync.WaitGroup
}

func newWorkerStopTracker() *workerStopTracker {
	return &workerStopTracker{stopping: map[string]struct{}{}}
}

func (t *workerStopTracker) claim(instanceID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, busy := t.stopping[instanceID]; busy {
		return false
	}
	t.stopping[instanceID] = struct{}{}
	t.wg.Add(1)
	return true
}

func (t *workerStopTracker) release(instanceID string) {
	t.mu.Lock()
	delete(t.stopping, instanceID)
	t.mu.Unlock()
	t.wg.Done()
}

func (t *workerStopTracker) active(instanceID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, busy := t.stopping[instanceID]
	return busy
}

// WaitWorkerStops blocks until every background worker stop started so far
// has finished. Shutdown paths and tests use it to observe settled state.
func (e *Engine) WaitWorkerStops() {
	e.workerStops.wg.Wait()
}

// workerStopping reports whether a background stop owns this instance. The
// reconciler neither keeps nor re-stops such a worker; it still holds its RAM
// and account slot until teardown commits.
func (e *Engine) workerStopping(instanceID string) bool {
	return e.workerStops.active(instanceID)
}

// beginWorkerStop retires one reconciler-owned worker without waiting for
// it. Rows that are still booting take the existing bounded destroy path
// inline; only a RUNNING worker's signal and grace period run in the
// background.
func (e *Engine) beginWorkerStop(ctx context.Context, ins state.Instance, opts StopOptions) error {
	if state.State(ins.State) != state.StateRunning {
		return e.stopManagedWorker(ctx, ins.ID, opts)
	}
	if !e.workerStops.claim(ins.ID) {
		return nil
	}
	stopCtx := detachedServiceContext(ctx)
	go func() {
		defer e.workerStops.release(ins.ID)
		if err := e.stopRunningWorker(stopCtx, ins.ID, opts); err != nil {
			e.log.Warn("sched: background worker stop failed; next reconcile retries",
				"app", ins.AppID, "instance", ins.ID, "err", err)
		}
	}()
	return nil
}

// stopRunningWorker delivers the stop signal and waits out the grace period
// without appMu, so admission and wakes for the same app are not serialized
// behind a draining workload. Teardown re-reads the row under appMu: if
// another owner moved it out of RUNNING meanwhile (runtime-config refresh,
// migration, crash handling), that owner keeps it.
func (e *Engine) stopRunningWorker(ctx context.Context, instanceID string, opts StopOptions) error {
	ins, err := e.store.InstanceByID(ctx, instanceID)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if state.State(ins.State) != state.StateRunning {
		return nil
	}
	signal := syscall.Signal(opts.Signal)
	if signal == 0 {
		signal = syscall.SIGTERM
	}
	grace := opts.GraceSeconds
	if grace <= 0 {
		grace = 30
	}
	if _, err := e.vmm.StopInstanceOnNode(ctx, ins.NodeID, instanceID, int32(signal), grace); err != nil {
		e.log.Warn("sched: worker stop signal-grace failed; falling through to destroy",
			"app", ins.AppID, "instance", instanceID, "err", err)
	}

	release := e.lockApp(ins.AppID)
	defer release()
	fresh, err := e.store.InstanceByID(ctx, instanceID)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if state.State(fresh.State) != state.StateRunning {
		return nil
	}
	if err := e.timedDestroy(ctx, fresh.NodeID, fresh.ID, DestroyTimeout); err != nil {
		return fmt.Errorf("sched: worker stop destroy %s: %w", fresh.ID, err)
	}
	e.ledger.Release(fresh.ID)
	e.transition(ctx, fresh.ID, fresh.AppID, state.StateStopped)
	return nil
}
