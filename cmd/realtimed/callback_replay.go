package main

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const (
	callbackReplayRetryInitial = time.Second
	callbackReplayRetryMax     = 30 * time.Second
)

// superviseCallbackReplay keeps durable callback replay available when an
// unexpected outbox or filesystem error stops a replay pass. Run normally
// returns only when the process context is canceled.
func superviseCallbackReplay(ctx context.Context, log *slog.Logger, initialDelay, maxDelay time.Duration, run func(context.Context) error) {
	if initialDelay <= 0 {
		initialDelay = callbackReplayRetryInitial
	}
	if maxDelay < initialDelay {
		maxDelay = initialDelay
	}

	retryDelay := initialDelay
	for {
		if ctx.Err() != nil {
			return
		}

		err := run(ctx)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			err = errors.New("callback outbox replay exited unexpectedly")
		}

		log.Warn("realtimed callback outbox stopped; retrying", "err", err, "retry_in", retryDelay.String())
		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
		retryDelay = nextCallbackReplayDelay(retryDelay, maxDelay)
	}
}

func nextCallbackReplayDelay(current, max time.Duration) time.Duration {
	if max <= 0 || current <= 0 || current >= max || current > max-current {
		return max
	}
	return current * 2
}
