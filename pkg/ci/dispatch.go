package ci

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// DispatchStore exposes only the Jobs reconciliation queue. Qualification and
// blocked receipts can never become dispatcher candidates.
type DispatchStore interface {
	ExecutionStore
	ListJobsExecutions(context.Context, string) ([]ExecutionSummary, error)
	claimDispatch(context.Context, Config) (executionLease, error)
	finishDispatch(context.Context, executionLease, bool) error
}

type ExecutionSummary struct {
	Rerun      *ExecutionRerun `json:"rerun,omitempty"`
	ID         string          `json:"id"`
	RevisionID string          `json:"revision_id"`
	Pipeline   string          `json:"pipeline"`
	CommitSHA  string          `json:"commit_sha"`
	Status     string          `json:"status"`
	CreatedAt  time.Time       `json:"created_at"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
}

// Dispatcher reconciles persisted intent through Jobs; schedd still owns task
// admission and native instances. The database bounds leases across processes.
type Dispatcher struct {
	Config  Config
	Store   DispatchStore
	Backend Backend
	Log     *slog.Logger
}

func (d Dispatcher) Run(ctx context.Context) {
	if !d.Config.WorkerEnabled || d.Store == nil || d.Backend == nil {
		return
	}
	var workers sync.WaitGroup
	for range api.CIDispatchWorkers {
		workers.Go(func() { d.runWorker(ctx) })
	}
	workers.Wait()
}

func (d Dispatcher) runWorker(ctx context.Context) {
	for ctx.Err() == nil {
		claimed, err := d.RunOnce(ctx)
		recordWorkerCycle(ctx, err)
		if err != nil && ctx.Err() == nil && d.Log != nil {
			// Provider errors can contain signed URLs; retain only stable classification.
			d.Log.Warn("internal CI reconciliation deferred", "code", "ci_reconcile_unavailable")
		}
		if claimed && err == nil {
			continue
		}
		timer := time.NewTimer(time.Duration(api.CIDispatchPollSeconds) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// RunOnce is bounded by the execution lease. Restart after any ambiguous backend
// acknowledgement retains the attempt identity and uses the database retry time.
func (d Dispatcher) RunOnce(ctx context.Context) (bool, error) {
	if !d.Config.WorkerEnabled || d.Store == nil || d.Backend == nil {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.CIExecutionLeaseSeconds)*time.Second)
	defer cancel()
	lease, err := d.Store.claimDispatch(ctx, d.Config)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	current, err := (Engine{Store: d.Store, Backend: d.Backend}).reconcile(ctx, lease)
	if current.ID == "" {
		// Preserve the claim schedule on a lost lease or ambiguous database
		// acknowledgement; a concurrent cancellation must remain due.
		return true, err
	}
	scheduleErr := d.Store.finishDispatch(ctx, lease, err != nil)
	return true, errors.Join(err, scheduleErr)
}
