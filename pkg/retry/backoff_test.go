package retry

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func TestDelaySaturatesWithoutOverflow(t *testing.T) {
	for _, attempt := range []int{1, 2, 3, 30, math.MaxInt} {
		ceiling := time.Second
		if attempt < 4 {
			ceiling = min(time.Duration(1<<(attempt-1))*100*time.Millisecond, time.Second)
		}
		for range 100 {
			delay := Delay(100*time.Millisecond, time.Second, attempt)
			if delay < ceiling/2 || delay > ceiling {
				t.Fatalf("retry %d: delay %s outside [%s, %s]", attempt, delay, ceiling/2, ceiling)
			}
		}
	}
	if got := Delay(time.Duration(math.MaxInt64), time.Duration(math.MaxInt64), math.MaxInt); got <= 0 {
		t.Fatalf("overflowed delay: %s", got)
	}
}

func TestCancellationInterruptsBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- Wait(ctx, time.Hour, time.Hour, 1) }()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("wait: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt backoff")
	}
	if err := Wait(ctx, time.Hour, time.Hour, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled initial attempt: %v", err)
	}
}
