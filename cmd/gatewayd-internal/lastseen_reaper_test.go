package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Exercise the production flush cadence against the real idle selector.
// A legal ten-second idle timeout must not park a continuously active guest
// between reports, regardless of the reaper's phase relative to the flush.
func TestActivityFlushKeepsBusyGuestOutOfIdleReaper(t *testing.T) {
	for phase := 0; phase < 10; phase++ {
		t.Run(fmt.Sprintf("reaper_phase_%d", phase), func(t *testing.T) {
			rep := &fakeReporter{}
			sink := newSchedFlushSink(newSingleClientResolver(rep), nil, testLogger())
			start := time.Unix(1_700_000_000, 0)
			instance := sched.InstanceInfo{
				Instance: "active", AppID: "app", Plan: api.PlanScale,
				State: state.StateRunning, Started: start.Add(-time.Minute),
				LastRequest: start, IdleTimeoutS: 10,
			}
			for second := 1; second <= 90; second++ {
				elapsed := time.Duration(second) * time.Second
				now := start.Add(elapsed)
				sink.Touch(instance.Instance, now)
				if elapsed%lastSeenFlushInterval == 0 {
					if err := sink.Flush(context.Background()); err != nil {
						t.Fatal(err)
					}
					instance.LastRequest = rep.last[0].LastRequest
				}
				if second%10 == phase {
					if reaped := sched.ReapIdle(now, []sched.InstanceInfo{instance}, nil, nil); len(reaped) != 0 {
						t.Fatalf("busy guest selected at %s; durable activity is %s old", elapsed, now.Sub(instance.LastRequest))
					}
				}
			}
			// Reporting activity must not turn into a permanent keep-alive.
			if reaped := sched.ReapIdle(start.Add(2*time.Minute), []sched.InstanceInfo{instance}, nil, nil); len(reaped) != 1 {
				t.Fatal("idle guest was not eligible after traffic stopped")
			}
		})
	}
}
