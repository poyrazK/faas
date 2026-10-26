package sched_test

// EnsureWake's steps before Engine.Wake (authorization, admission reads,
// coordination, the lifecycle claim) end where Engine.Wake began.

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/sched"
)

func TestPreRPCWithPreWake(t *testing.T) {
	t0 := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	set := events.PreRPCTimings{Set: true}
	cases := []struct {
		name    string
		ctx     context.Context
		in      events.PreRPCTimings
		attempt time.Time
		want    events.PreWakeTimings
	}{
		{name: "ends at Engine.Wake's start",
			ctx:  sched.WithWakeStartedForTest(sched.WithPreWakeClockForTest(context.Background(), at(0), at(3), at(40), at(41)), at(55)),
			in:   set, attempt: at(70),
			want: events.PreWakeTimings{Set: true, AuthorizeMs: 3, EnvironmentMs: 37, CoordinateMs: 1, TransitionMs: 14}},
		{name: "falls back to the admission attempt",
			ctx:  sched.WithPreWakeClockForTest(context.Background(), at(0), at(3), at(40), at(41)),
			in:   set, attempt: at(60),
			want: events.PreWakeTimings{Set: true, AuthorizeMs: 3, EnvironmentMs: 37, CoordinateMs: 1, TransitionMs: 19}},
		{name: "no clock: not an EnsureWake", ctx: context.Background(), in: set, attempt: at(60), want: events.PreWakeTimings{}},
		{name: "unset pre-RPC stays unset",
			ctx:  sched.WithPreWakeClockForTest(context.Background(), at(0), at(3), at(40), at(41)),
			in:   events.PreRPCTimings{}, attempt: at(60), want: events.PreWakeTimings{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sched.PreRPCWithPreWakeForTest(tc.ctx, tc.in, tc.attempt).PreWake; got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestPreWakeMarksKeepTheFirstStamp(t *testing.T) {
	ctx := sched.WithEnsureWakeRPCStarted(context.Background(), time.Now())
	sched.MarkPreWakeForTest(ctx)
	first := sched.PreRPCWithPreWakeForTest(ctx, events.PreRPCTimings{Set: true}, time.Now().Add(time.Hour)).PreWake
	time.Sleep(5 * time.Millisecond)
	sched.MarkPreWakeForTest(ctx)
	again := sched.PreRPCWithPreWakeForTest(ctx, events.PreRPCTimings{Set: true}, time.Now().Add(time.Hour)).PreWake
	if !first.Set || first.AuthorizeMs != again.AuthorizeMs || first.EnvironmentMs != again.EnvironmentMs || first.CoordinateMs != again.CoordinateMs {
		t.Fatalf("a repeated mark moved a stamp: first %+v, again %+v", first, again)
	}
}
