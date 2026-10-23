package main

import (
	"errors"
	"testing"
	"time"
)

// The exit frame is retried until acknowledged for jobExitShipBudget, which
// spans a vmmd restart, with a backoff that stays short.
func TestRetryJobExitRetriesForTheBudget(t *testing.T) {
	clock := time.Unix(0, 0)
	now := func() time.Time { return clock }
	sleep := func(d time.Duration) { clock = clock.Add(d) }
	refused := errors.New("connect: connection reset")

	calls := 0
	n, err := retryJobExit(jobExitShipBudget, now, sleep, func(int) error {
		calls++
		if calls < 6 {
			return refused
		}
		return nil
	})
	if err != nil || n != 6 {
		t.Fatalf("acknowledged on the sixth attempt: n=%d err=%v", n, err)
	}

	clock = time.Unix(0, 0)
	n, err = retryJobExit(jobExitShipBudget, now, sleep, func(int) error { return refused })
	if !errors.Is(err, refused) {
		t.Fatalf("never acknowledged: err=%v", err)
	}
	if elapsed := clock.Sub(time.Unix(0, 0)); elapsed > jobExitShipBudget || elapsed < jobExitShipBudget-2*time.Second {
		t.Fatalf("retried for %s, want about %s", elapsed, jobExitShipBudget)
	}
	if n < 30 {
		t.Fatalf("%d attempts in the budget; the backoff grew too fast", n)
	}

	// A clock that jumps past the budget still gets the old three attempts.
	clock = time.Unix(0, 0)
	jump := func(time.Duration) { clock = clock.Add(time.Hour) }
	if n, _ := retryJobExit(jobExitShipBudget, now, jump, func(int) error { return refused }); n != jobExitMinAttempts {
		t.Fatalf("attempts on a jumping clock = %d, want %d", n, jobExitMinAttempts)
	}
}

func TestJobExitBackoff(t *testing.T) {
	for i, want := range []time.Duration{100, 200, 400, 800, 1600, 2000, 2000} {
		if got := jobExitBackoff(i); got != want*time.Millisecond {
			t.Fatalf("backoff(%d) = %s, want %s", i, got, want*time.Millisecond)
		}
	}
}
