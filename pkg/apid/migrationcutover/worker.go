package migrationcutover

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type Worker struct {
	Store   state.MigrationCutoverStore
	Backend func(state.MigrationCutoverRecord) Backend
	OnError func(error)
}

func (w Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(api.MigrationCutoverPollInterval)
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
	listCtx, cancel := context.WithTimeout(ctx, api.MigrationCutoverLease)
	rows, err := w.Store.ListDueMigrationCutovers(listCtx, time.Now(), api.MigrationCutoverWorkerBatch)
	cancel()
	if err != nil {
		return err
	}
	jobs := make(chan state.MigrationCutoverRecord)
	var wg sync.WaitGroup
	for range api.MigrationCutoverWorkers {
		wg.Go(func() {
			for r := range jobs {
				if ctx.Err() != nil {
					return
				}
				if _, err := w.Backend(r).Advance(ctx, r.Report.ID); err != nil && ctx.Err() == nil && w.OnError != nil {
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
	return nil
}
