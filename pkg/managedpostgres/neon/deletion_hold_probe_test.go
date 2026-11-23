// adr: 20261029174950 — the hold probe counts a release only once Neon
// accepts connections steadily.
package neon

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Neon keeps refusing a released endpoint for a while, so one accepted
// connection does not end the wait: a refusal after it restarts the run.
func TestAwaitSteadyNeedsAnUnbrokenRun(t *testing.T) {
	refused := errors.New("ERROR: The endpoint has been disabled")
	for name, tc := range map[string]struct {
		results []error // then success forever
		want    bool
		atLeast int
	}{
		"accepted at once":           {want: true, atLeast: 3},
		"refused, then accepted":     {results: []error{refused, refused}, want: true, atLeast: 5},
		"accepted, refused, settles": {results: []error{nil, refused, nil, refused}, want: true, atLeast: 7},
		"never steady":               {results: repeatErrors(refused, 1000), want: false},
	} {
		calls := 0
		query := func(context.Context) error {
			calls++
			if calls <= len(tc.results) {
				return tc.results[calls-1]
			}
			return nil
		}
		got := awaitSteady(context.Background(), query, 20*time.Millisecond, 200*time.Millisecond, 10*time.Millisecond)
		if got != tc.want || (tc.want && calls < tc.atLeast) {
			t.Errorf("%s: steady = %v after %d attempts; want %v after at least %d", name, got, calls, tc.want, tc.atLeast)
		}
	}
}

func TestAwaitSteadyStopsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if awaitSteady(ctx, func(context.Context) error { return nil }, time.Hour, time.Hour, time.Millisecond) {
		t.Fatal("a cancelled wait reported a steady endpoint")
	}
}

func TestDeletionHoldProbeSettlesForAMinute(t *testing.T) {
	if deletionHoldProbeSettle < time.Minute {
		t.Fatalf("deletionHoldProbeSettle = %v; refusals were measured tens of seconds after release", deletionHoldProbeSettle)
	}
}

func repeatErrors(err error, n int) []error {
	out := make([]error, n)
	for i := range out {
		out[i] = err
	}
	return out
}
