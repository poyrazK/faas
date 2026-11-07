// Package domainverify bounds DNS ownership checks independently of the size
// of the pending domain and tenant-hostname inventories.
package domainverify

import (
	"context"
	"sync"
)

// RunBatch processes checks with bounded concurrency. Checks must honor ctx;
// cancellation stops queued work and joins the checks already in progress.
func RunBatch(ctx context.Context, workers int, checks []func(context.Context)) {
	jobs := make(chan func(context.Context))
	var running sync.WaitGroup
	for range min(max(workers, 1), len(checks)) {
		running.Go(func() {
			for check := range jobs {
				if ctx.Err() != nil {
					return
				}
				check(ctx)
			}
		})
	}
enqueue:
	for _, check := range checks {
		select {
		case jobs <- check:
		case <-ctx.Done():
			break enqueue
		}
	}
	close(jobs)
	running.Wait()
}
