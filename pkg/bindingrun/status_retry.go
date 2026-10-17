package bindingrun

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func statusReadRetryable(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	problem := api.AsProblem(err)
	if problem == nil {
		var wire *api.APIError
		if errors.As(err, &wire) {
			problem = &wire.Problem
		}
	}
	if problem != nil {
		return problem.Status == 502 || problem.Status == 503 || problem.Status == 504
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ETIMEDOUT) {
		return true
	}
	var network net.Error
	return errors.As(err, &network) && network.Timeout()
}
func statusReadRetryCount(o Options) int {
	if o.StatusReadRetries == nil {
		return api.BindingVerificationRunnerDefaultStatusReadRetries
	}
	return *o.StatusReadRetries
}
func statusReadRetryDelay(attempt int) time.Duration {
	base := min(time.Duration(api.BindingVerificationRunnerStatusReadRetryInitialMilliseconds)*time.Millisecond<<(attempt-1), time.Duration(api.BindingVerificationRunnerStatusReadRetryMaxMilliseconds)*time.Millisecond)
	return base/2 + time.Duration(rand.Int64N(int64(base/2)+1))
}
func statusReadContextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if until, ok := ctx.Deadline(); ok && !until.After(time.Now()) {
		return context.DeadlineExceeded
	}
	return nil
}
func readStatusWithRetry(ctx context.Context, id string, o Options, read func(context.Context, bool) (snapshot, error), notify func(Progress) error) (snapshot, error) {
	for attempt := 0; ; attempt++ {
		if err := statusReadContextError(ctx); err != nil {
			return snapshot{}, err
		}
		result, err := read(ctx, false)
		if ctxErr := statusReadContextError(ctx); ctxErr != nil {
			return snapshot{}, ctxErr
		}
		if err == nil || attempt >= statusReadRetryCount(o) || !statusReadRetryable(err) {
			return result, err
		}
		delay := statusReadRetryDelay(attempt + 1)
		if until, ok := ctx.Deadline(); ok {
			delay = min(delay, max(time.Until(until), 0))
		}
		if e := notify(Progress{RunID: id, Step: -1, Status: "retrying", RetryAttempt: attempt + 1, RetryDelayMilliseconds: delay.Milliseconds()}); e != nil {
			return snapshot{}, e
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return snapshot{}, ctx.Err()
		case <-timer.C:
		}
	}
}
