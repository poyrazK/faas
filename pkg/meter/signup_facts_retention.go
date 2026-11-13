package meter

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// SignupFactsRetentionOnce clears the sign-up address and user agent of
// account_signup_facts rows older than api.SignupFactsPersonalRetention
// (ADR-20261113160000), in RetentionBatchSize batches up to
// MaxRetentionBatches. The rest of each row stays with the account.
func SignupFactsRetentionOnce(ctx context.Context, store state.AccountSignupFactsStore, now time.Time) (int64, error) {
	cutoff := now.Add(-api.SignupFactsPersonalRetention)
	var total int64
	for i := 0; i < MaxRetentionBatches; i++ {
		n, err := store.ReduceAccountSignupFacts(ctx, cutoff, now, RetentionBatchSize)
		if err != nil {
			return total, fmt.Errorf("signup facts retention (batch %d, reduced so far %d): %w", i, total, err)
		}
		total += n
		if n < RetentionBatchSize {
			return total, nil
		}
	}
	return total, ErrRetentionBatchCap
}

// SignupFactsRetentionLoop runs SignupFactsRetentionOnce every interval until
// ctx is done. A batch-cap hit resumes on the next tick.
func SignupFactsRetentionLoop(ctx context.Context, store state.AccountSignupFactsStore, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = DefaultRetentionInterval
	}
	if log == nil {
		log = slog.Default()
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if n, err := SignupFactsRetentionOnce(ctx, store, now); err != nil {
				log.Warn("signup facts retention tick", "rows_reduced", n, "err", err)
			} else if n > 0 {
				log.Info("signup facts retention tick ok", "rows_reduced", n)
			}
		}
	}
}
