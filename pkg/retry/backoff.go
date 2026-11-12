// Package retry supplies cancellable, capped backoff. Callers own the attempt
// budget and must establish that replaying an operation is safe.
package retry

import (
	"context"
	"math/rand/v2"
	"time"
)

// Delay returns equal-jitter exponential backoff for a 1-based retry number.
// Zero is the initial attempt. Growth saturates before multiplying, including
// for untrusted or very large attempt numbers. Jitter never exceeds the cap.
func Delay(base, cap time.Duration, retry int) time.Duration {
	if retry <= 0 || base <= 0 || cap <= 0 {
		return 0
	}
	delay := min(base, cap)
	for n := 1; n < retry && delay < cap; n++ {
		if delay > cap/2 {
			delay = cap
			break
		}
		delay *= 2
	}
	half := delay / 2
	return half + time.Duration(rand.Int64N(int64(delay-half)+1)) //nolint:gosec // Scheduling jitter carries no secrets.
}

// Wait waits before a retry, stopping immediately on cancellation. It also
// checks cancellation before the initial attempt, for which there is no delay.
func Wait(ctx context.Context, base, cap time.Duration, retry int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delay := Delay(base, cap, retry)
	if delay == 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}
