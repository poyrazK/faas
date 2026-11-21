package sched_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// A push lane keeps delivering while polls return records, instead of one
// record per tick (ADR-972 amendment 2026-11-19).
func TestDrainQueuePushLane(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name    string
		backlog int   // records the polls return, one per dispatch
		failAt  int   // dispatch number that fails; 0 = none
		err     error // the failure
		want    int   // dispatches the lane makes
		wantErr error
	}{
		{"empty queue polls once", 0, 0, nil, 1, nil},
		{"drains the backlog then stops on the empty poll", 5, 0, nil, 6, nil},
		{"stops at the record budget", 1000, 0, nil, api.QueuePushLaneDrainMaxRecords, nil},
		{"stops on a dispatch error", 10, 3, boom, 3, boom},
		{"stops on a gateway failure", 10, 2, sched.ErrTriggerGatewayDispatch, 2, sched.ErrTriggerGatewayDispatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls, left := 0, tc.backlog
			err := sched.DrainQueuePushLane(t.Context(), func(ctx context.Context) error {
				calls++
				if calls == tc.failAt {
					return tc.err
				}
				if left > 0 {
					left--
					sched.ReportTriggerPoll(ctx, 1)
				} else {
					sched.ReportTriggerPoll(ctx, 0)
				}
				return nil
			})
			if !errors.Is(err, tc.wantErr) || calls != tc.want {
				t.Fatalf("dispatches=%d err=%v, want %d %v", calls, err, tc.want, tc.wantErr)
			}
		})
	}
}

// A cancelled scheduler stops a lane after the dispatch in progress.
func TestDrainQueuePushLaneStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	_ = sched.DrainQueuePushLane(ctx, func(ctx context.Context) error {
		calls++
		sched.ReportTriggerPoll(ctx, 1)
		cancel()
		return nil
	})
	if calls != 1 {
		t.Fatalf("dispatches after cancel=%d, want 1", calls)
	}
}

// A dispatch without a report (a path that returned before polling) counts
// as empty, so the lane never spins.
func TestDrainQueuePushLaneUnreportedDispatchEndsLane(t *testing.T) {
	calls := 0
	_ = sched.DrainQueuePushLane(t.Context(), func(context.Context) error { calls++; return nil })
	if calls != 1 {
		t.Fatalf("dispatches=%d, want 1", calls)
	}
}
