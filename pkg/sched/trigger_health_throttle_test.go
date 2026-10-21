// adr: 134 — trigger dispatch records consumer health for the binding status surfaces.

package sched_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TestRecordTriggerConsumerHealthWritesOnChangeOrEveryInterval pins the
// control-plane write throttle: an unchanged poll outcome is written at most
// every 10 s (readers call a consumer stale after 30 s), while every change of
// outcome is written at once.
func TestRecordTriggerConsumerHealthWritesOnChangeOrEveryInterval(t *testing.T) {
	ctx := context.Background()
	store := &sched.FakeDeadLetterStore{}
	trigger := sqlc.Trigger{
		ID:   pgtype.UUID{Bytes: uuid.MustParse("00000000-0000-0000-0000-000000000098"), Valid: true},
		Kind: string(api.TriggerKindKafka),
	}
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	loop := sched.MakeLoopForDLQ().WithClock(func() time.Time { return now })
	brokerDown, authFailed := errors.New("broker down"), errors.New("auth failed")

	steps := []struct {
		advance time.Duration
		err     error
		written bool
	}{
		{0, nil, true},                      // first poll
		{time.Second, nil, false},           // unchanged, 1 s later
		{time.Second, brokerDown, true},     // success → failure
		{time.Second, brokerDown, false},    // same failure
		{time.Second, authFailed, true},     // different failure
		{time.Second, nil, true},            // recovered
		{9 * time.Second, nil, false},       // unchanged, 9 s after the last write
		{time.Second, nil, true},            // unchanged, 10 s after the last write
		{triggerHealthBackwards, nil, true}, // the clock stepped back
	}
	want := 0
	for i, step := range steps {
		now = now.Add(step.advance)
		loop.RecordTriggerConsumerHealth(ctx, store, trigger, sched.PollResult{Error: step.err})
		if step.written {
			want++
		}
		got := store.Health()
		if len(got) != want {
			t.Fatalf("step %d: %d health writes, want %d", i, len(got), want)
		}
		if step.written {
			last := got[len(got)-1]
			if !last.LastPollAt.Equal(now) || last.Success != (step.err == nil) {
				t.Fatalf("step %d: wrote %+v, want a poll at %s with success %t", i, last, now, step.err == nil)
			}
		}
	}
}

// triggerHealthBackwards steps the test clock back past the last write.
const triggerHealthBackwards = -time.Minute
