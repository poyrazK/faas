package main_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestMCPVerificationQueuedJobsReceiveFreshBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		budgets := make([]time.Duration, 8)
		jobErrors := make([]error, 8)
		main.RunMCPVerificationJobs(t.Context(), len(budgets), func(ctx context.Context, index int) {
			deadline, ok := ctx.Deadline()
			if !ok {
				return
			}
			budgets[index] = time.Until(deadline)
			if index < 4 {
				<-ctx.Done()
			}
			jobErrors[index] = ctx.Err()
		})
		for index, budget := range budgets {
			if budget <= 0 || budget != budgets[0] {
				t.Fatalf("queued job %d lost its probe budget: %s, first job %s", index, budget, budgets[0])
			}
			if index < 4 && !errors.Is(jobErrors[index], context.DeadlineExceeded) {
				t.Fatalf("active job %d exceeded its deadline: %v", index, jobErrors[index])
			}
			if index >= 4 && jobErrors[index] != nil {
				t.Fatalf("waiting consumed job %d's deadline: %v", index, jobErrors[index])
			}
		}
	})
}

func TestMCPVerificationJobsHonorCallerDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		started := time.Now()
		jobErrors := make([]error, 8)
		main.RunMCPVerificationJobs(ctx, len(jobErrors), func(ctx context.Context, index int) {
			<-ctx.Done()
			jobErrors[index] = ctx.Err()
		})
		if elapsed := time.Since(started); elapsed != 2*time.Second {
			t.Fatalf("caller deadline was extended: %s", elapsed)
		}
		for index, err := range jobErrors {
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("job %d ignored caller cancellation: %v", index, err)
			}
		}
	})
}
