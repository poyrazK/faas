// Package healthdispatch runs a bounded set of independently claimed health jobs.
package healthdispatch

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Options struct {
	Workers                                int
	PollInterval, ClaimTimeout, JobTimeout time.Duration
}

// Run claims only when a worker is available. Slot zero prefers recovery; other
// slots preserve ordinary due-time fairness. Reconcile owns claim cleanup, even
// when shutdown races a successful claim. Cancellation waits for active calls.
func Run[T any](ctx context.Context, options Options, claim func(context.Context, string, bool) (*T, error), reconcile func(context.Context, T, string) error, report func(*T, error)) {
	var wg sync.WaitGroup
	for slot := range options.Workers {
		wg.Go(func() {
			for ctx.Err() == nil {
				token := uuid.NewString()
				claimCtx, cancel := context.WithTimeout(ctx, options.ClaimTimeout)
				job, err := claim(claimCtx, token, slot == 0)
				cancel()
				if err == nil && job != nil {
					callCtx, callCancel := context.WithTimeout(ctx, options.JobTimeout)
					err = reconcile(callCtx, *job, token)
					callCancel()
					if err != nil && ctx.Err() == nil && report != nil {
						report(job, err)
					}
					continue
				}
				if err != nil && ctx.Err() == nil && report != nil {
					report(nil, err)
				}
				timer := time.NewTimer(options.PollInterval)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		})
	}
	wg.Wait()
}
