package datapipeline

import (
	"context"
	"errors"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sync/errgroup"
)

type Worker struct {
	SetupTick  func(context.Context) (bool, error)
	Store      Store
	Engine     *Engine
	Identities []*age.X25519Identity
	Allowed    func(context.Context, string) bool
}

// Tick reconciles one bounded source. Only metadata crosses the control API;
// native PostgreSQL handles the initial copy and ongoing row movement. Approved
// new-column backfills use bounded worker-side batches between the two endpoints.
func (w *Worker) Tick(ctx context.Context) error {
	_, err := w.tick(ctx)
	return err
}

func (w *Worker) tick(ctx context.Context) (bool, error) {
	p, err := w.Store.Claim(ctx, uuid.NewString())
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, w.reconcilePipeline(ctx, p)
}

func (w *Worker) reconcilePipeline(ctx context.Context, p Pipeline) error {
	op, cancel := context.WithTimeout(ctx, api.DataPipelineOperationTimeout)
	defer cancel()
	intent := p
	admitted := w.Allowed != nil && w.Allowed(op, p.AccountID)
	if !admitted && intent.DesiredState == "running" {
		intent.DesiredState = "paused"
	}
	current := func(ctx context.Context) error {
		current, err := w.Store.Get(ctx, p.AccountID, p.ID)
		if err != nil {
			return err
		}
		if current.Revision != p.Revision || current.LeaseToken != p.LeaseToken || !current.LeaseUntil.After(time.Now()) {
			return ErrConflict
		}
		return nil
	}
	c, err := w.Store.Connection(op, p.AccountID, p.SourceConnectionID)
	var raw string
	if err == nil {
		intent.SourceRevision = c.Revision
		raw, err = openConnection(w.Identities, c)
	}
	var ob Observation
	if err != nil {
		ob = w.Engine.reconcileUnavailable(op, intent, current)
	} else {
		ob = w.Engine.Reconcile(op, intent, raw, current)
	}
	if !admitted && p.DesiredState == "running" && ob.State == "paused" {
		ob.State, ob.Reason = "blocked", "admission_closed"
	}
	return w.Store.Finish(ctx, p, ob)
}
func (w *Worker) Run(ctx context.Context) error {
	if w.Store == nil || w.Engine == nil || len(w.Identities) == 0 {
		return ErrInvalid
	}
	queues := []workerQueue{
		{api.DataPipelineReconcileConcurrency, w.tick},
		{api.DataPipelineCheckConcurrency, w.checkTick},
	}
	if w.SetupTick != nil {
		queues = append(queues, workerQueue{api.DataPipelineSetupConcurrency, w.SetupTick})
	}
	return runWorkerQueues(ctx, queues...)
}

func (w *Worker) CheckTick(ctx context.Context) error {
	_, err := w.checkTick(ctx)
	return err
}

func (w *Worker) checkTick(ctx context.Context) (bool, error) {
	job, err := w.Store.ClaimCheck(ctx, uuid.NewString())
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, w.checkConnection(ctx, job)
}

func (w *Worker) checkConnection(ctx context.Context, job ConnectionCheck) error {
	op, cancel := context.WithTimeout(ctx, api.DataPipelineOperationTimeout)
	defer cancel()
	result := blockedCheck("admission_closed", "Connection checks are unavailable", "Ask the operator to enable this staging account.")
	if w.Allowed != nil && w.Allowed(op, job.AccountID) {
		connection, err := w.Store.Connection(op, job.AccountID, job.ConnectionID)
		if err != nil || connection.Revision != job.ConnectionRevision {
			return ErrConflict
		}
		raw, err := openConnection(w.Identities, connection)
		if err != nil {
			result = blockedCheck("credential_unavailable", "Source credential is unavailable", "Rotate the source credential and run the check again.")
		} else {
			result = w.Engine.Check(op, job, raw)
		}
	}
	return w.Store.FinishCheck(ctx, job, result)
}

type workerQueue struct {
	concurrency int
	next        func(context.Context) (bool, error)
}

// Claims and their existing lease/revision fences serialize each resource.
// Independent bounded queues drain due work immediately; only an empty queue
// waits. A slow endpoint or provisioning call cannot hold up another queue.
func runWorkerQueues(ctx context.Context, queues ...workerQueue) error {
	group, running := errgroup.WithContext(ctx)
	for _, queue := range queues {
		for range queue.concurrency {
			group.Go(func() error {
				timer := time.NewTimer(api.DataPipelineWorkerIdleInterval)
				defer timer.Stop()
				retry := api.DataPipelineWorkerIdleInterval
				for {
					if err := running.Err(); err != nil {
						return err
					}
					claimed, err := queue.next(running)
					if err != nil && !errors.Is(err, ErrConflict) {
						if !errors.Is(err, ErrUnavailable) {
							return err
						}
						timer.Reset(retry)
						retry = min(retry*2, api.DataPipelineCatalogRetryMax)
						select {
						case <-running.Done():
							return running.Err()
						case <-timer.C:
						}
						continue
					}
					retry = api.DataPipelineWorkerIdleInterval
					if claimed {
						continue
					}
					timer.Reset(api.DataPipelineWorkerIdleInterval)
					select {
					case <-running.Done():
						return running.Err()
					case <-timer.C:
					}
				}
			})
		}
	}
	err := group.Wait()
	// Catalog errors are sanitized, including queries canceled during shutdown.
	// Preserve external cancellation without masking a fatal queue error that
	// canceled only the group's internal context.
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
