package routejobs

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// MonitorDrain evaluates the production route monitors that are due. Each
// read returns at most api.RouteMonitorBatchSize monitors; the drain keeps
// reading until no monitor it has not already taken this drain is due or
// api.RouteMonitorDrainBudget has passed, and evaluates
// api.RouteMonitorWorkerConcurrency monitors at a time, so throughput scales
// past one batch per poll (ADR-20261024154324). Replicas share the queue:
// an evaluation another replica holds is skipped.
type MonitorDrain struct {
	Queue state.RouteMonitorWorkerStore
	// AfterEvaluate runs after each completed evaluation, outside its
	// transaction; it applies the monitor's automatic rollback (ADR-845).
	AfterEvaluate func(context.Context, state.RouteMonitorTarget)
	// Released decides the monitoring capability for the target's account
	// (ADR-20261028110044). An unreleased monitor is deferred without
	// evaluating, so it opens no incident, sends no webhook and rolls nothing
	// back. Nil releases every monitor.
	Released func(context.Context, state.RouteMonitorTarget) bool
	Metrics  *Metrics
	Log      *slog.Logger
	Now      func() time.Time
}

// Run drains due monitors and returns how many evaluations completed.
func (d MonitorDrain) Run(ctx context.Context) (int, error) {
	started := d.now()
	seen := make(map[string]struct{})
	completed := 0
	first := true
	for d.now().Sub(started) < api.RouteMonitorDrainBudget {
		listCtx, cancel := context.WithTimeout(ctx, api.RouteCheckTimeout)
		targets, err := d.Queue.ListDueRouteMonitors(listCtx)
		cancel()
		if err != nil {
			return completed, err
		}
		if first {
			first = false
			lag := time.Duration(0)
			if len(targets) > 0 {
				lag = started.Sub(targets[0].DueAt)
			}
			d.Metrics.setMonitorLag(lag)
		}
		fresh := targets[:0]
		for _, t := range targets {
			if _, ok := seen[t.AppID]; !ok {
				seen[t.AppID] = struct{}{}
				fresh = append(fresh, t)
			}
		}
		if len(fresh) == 0 {
			break
		}
		completed += d.evaluate(ctx, fresh)
		if ctx.Err() != nil {
			return completed, ctx.Err()
		}
	}
	d.Metrics.monitorDrained(d.now())
	return completed, nil
}

// evaluate runs one batch with bounded concurrency.
func (d MonitorDrain) evaluate(ctx context.Context, targets []state.RouteMonitorTarget) int {
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		completed int
	)
	slots := make(chan struct{}, api.RouteMonitorWorkerConcurrency)
	for _, target := range targets {
		if ctx.Err() != nil {
			break
		}
		slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-slots; wg.Done() }()
			if d.evaluateOne(ctx, target) {
				mu.Lock()
				completed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return completed
}

func (d MonitorDrain) evaluateOne(ctx context.Context, target state.RouteMonitorTarget) bool {
	if d.Released != nil && !d.Released(ctx, target) {
		d.Metrics.observeMonitor(MonitorUnreleased)
		deferCtx, deferCancel := context.WithTimeout(ctx, api.RouteCheckTimeout)
		_ = d.Queue.DeferRouteMonitor(deferCtx, target)
		deferCancel()
		return false
	}
	checkCtx, cancel := context.WithTimeout(ctx, api.RouteCheckTimeout)
	done, err := d.Queue.EvaluateRouteMonitor(checkCtx, target.AccountID, target.AppID)
	cancel()
	if err != nil && ctx.Err() == nil {
		d.Metrics.observeMonitor(MonitorFailed)
		d.log().Warn("production route monitor evaluation will retry", "app_id", target.AppID)
		retryCtx, retryCancel := context.WithTimeout(ctx, api.RouteCheckTimeout)
		_ = d.Queue.DeferRouteMonitor(retryCtx, target)
		retryCancel()
		return false
	}
	if !done {
		if err == nil {
			d.Metrics.observeMonitor(MonitorSkipped)
		}
		return false
	}
	d.Metrics.observeMonitor(MonitorEvaluated)
	if d.AfterEvaluate != nil {
		// Evaluation has committed any new incident; rollback runs outside
		// its transaction because monitors never write deployments (ADR-845).
		rollbackCtx, rollbackCancel := context.WithTimeout(ctx, api.RouteCheckTimeout)
		d.AfterEvaluate(rollbackCtx, target)
		rollbackCancel()
	}
	return true
}

func (d MonitorDrain) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d MonitorDrain) log() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.Default()
}
