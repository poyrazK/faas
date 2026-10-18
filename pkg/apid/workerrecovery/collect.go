// Package workerrecovery provides read-only recovery diagnostics. Authorization
// and environment resolution remain with the caller's existing app API boundary.
package workerrecovery

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type Reader interface {
	WorkerRecoveryByApp(context.Context, string) (state.WorkerRecovery, error)
}

// Collect filters pool observations by the selected scope. The lease and retry
// outcome always describe the whole app, even in an environment-scoped response.
// Missing records, read errors and stale observations never imply a healthy pool.
func Collect(ctx context.Context, store Reader, app state.App, account state.Account, scope string, now time.Time) *api.AppWorkerRecovery {
	row, err := store.WorkerRecoveryByApp(ctx, app.ID)
	worker := app.Manifest.ExecutionMode == api.ExecutionModeWorker || app.WorkloadClass == state.WorkloadClassWorker
	// Captured worker environments can survive a switch of the mutable app
	// head to request mode. Keep failed/unobserved attempts visible for them.
	if !worker && (errors.Is(err, state.ErrNotFound) || err == nil && len(row.Pools) == 0 &&
		row.Status == "ready" && row.Failures == 0 && row.ClaimToken == "") {
		return nil
	}
	out := &api.AppWorkerRecovery{Scope: "app", Eligible: app.Status == state.AppActive && account.Active(),
		Status: "unknown", Pools: []api.AppWorkerPoolObservation{}}
	if errors.Is(err, state.ErrNotFound) {
		out.Available, out.Status, out.Reason = true, "not_observed", "no_attempt"
		return out
	}
	if err != nil {
		out.Reason = "state_unavailable"
		return out
	}
	out.Available, out.Status, out.Failures = true, row.Status, row.Failures
	out.NextAttemptAt, out.UpdatedAt = &row.NextAttemptAt, &row.UpdatedAt
	if row.ClaimToken != "" {
		out.LeaseUntil = &row.LeaseUntil
	}
	freshAfter := now.Add(-time.Duration(api.WorkerRecoveryObservationFreshnessSeconds) * time.Second)
	for _, pool := range row.Pools {
		if pool.Revision != row.Revision || scope != "" && pool.Scope != scope {
			continue
		}
		blockers := make([]api.WorkerAdmissionBlocker, 0, len(pool.Blockers))
		for _, b := range pool.Blockers {
			b = b.Clone()
			b.Stale = b.ObservedAt.Before(freshAfter)
			blockers = append(blockers, b)
		}
		out.Pools = append(out.Pools, api.AppWorkerPoolObservation{Scope: pool.Scope, DeploymentID: pool.DeploymentID,
			ObservedAt: pool.ObservedAt, Stale: pool.ObservedAt.Before(freshAfter), Desired: pool.Desired,
			Running: pool.Running, Starting: pool.Starting, Draining: pool.Draining, Blockers: blockers})
	}
	return out
}
