// adr:1573 — server-driven migration checks and durable restoration.
package migrationverify

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type Worker struct {
	Store   state.MigrationVerificationWorkerStore
	Backend func(context.Context, state.MigrationVerificationRecord) (Backend, error)
	OnError func(error)
}

func (w Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(api.MigrationVerificationPollInterval)
	defer ticker.Stop()
	for {
		if err := w.Drain(ctx); err != nil && ctx.Err() == nil && w.OnError != nil {
			w.OnError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w Worker) Drain(ctx context.Context) error {
	listCtx, cancel := context.WithTimeout(ctx, api.MigrationVerificationLease)
	rows, err := w.Store.ListDueMigrationVerifications(listCtx, time.Now(), api.MigrationVerificationWorkerBatch)
	cancel()
	if err != nil {
		return err
	}
	jobs := make(chan state.MigrationVerificationRecord)
	var wg sync.WaitGroup
	for range api.MigrationVerificationWorkers {
		wg.Go(func() {
			for r := range jobs {
				if ctx.Err() != nil {
					return
				}
				b, err := w.Backend(ctx, r)
				if err == nil {
					_, err = b.Advance(ctx, r.Report.ID)
				}
				if err != nil && ctx.Err() == nil && w.OnError != nil {
					w.OnError(err)
				}
			}
		})
	}
	for _, r := range rows {
		select {
		case jobs <- r:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	return ctx.Err()
}
