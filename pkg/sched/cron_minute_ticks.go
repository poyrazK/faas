package sched

import (
	"context"
	"time"
)

// cronTickOffset is how far past each wall-clock minute the cron sweep runs,
// so a boundary written at :00 is already in the past when it is evaluated.
const cronTickOffset = time.Second

// minuteTicks delivers one tick shortly after every wall-clock minute until
// ctx ends. The sweep used to run on a plain 60 s ticker whose phase was the
// schedd's start time: on production-us one node fired every cron 46 s after
// its scheduled minute, every minute (production-us hunt #5, H5-40). Each
// wait is recomputed from the clock, so the phase never drifts.
func minuteTicks(ctx context.Context, now func() time.Time) <-chan time.Time {
	ch := make(chan time.Time, 1)
	go func() {
		for {
			timer := time.NewTimer(untilNextMinuteTick(now()))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case at := <-timer.C:
				select {
				case ch <- at:
				default: // the loop is still busy with the previous sweep
				}
			}
		}
	}()
	return ch
}

// untilNextMinuteTick is the wait from now to the next minute boundary plus
// cronTickOffset.
func untilNextMinuteTick(now time.Time) time.Duration {
	next := now.Truncate(time.Minute).Add(cronTickOffset)
	if !next.After(now) {
		next = next.Add(time.Minute)
	}
	return next.Sub(now)
}
