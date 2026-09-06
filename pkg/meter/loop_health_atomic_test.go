package meter

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

func TestLoopHealthNeverClearsRepeatedTickFailure(t *testing.T) {
	now := time.Now()
	failure := errors.New("billing unavailable")
	loop := &Loop{
		cfg:         &Config{SampleInterval: time.Hour, QuotaInterval: time.Hour, StripeInterval: time.Hour},
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		ops:         wire.NewOpsMetrics("meter_atomic_health"),
		lastTick:    map[string]time.Time{"sample": now, "quota": now, "stripe": now},
		lastTickErr: map[string]string{"stripe": failure.Error()},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	done := make(chan error, 1)
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("runTicks: %v", err)
		}
	}()
	started := make(chan struct{})
	var once sync.Once
	go func() {
		done <- loop.runTicks(ctx, time.Microsecond, func(context.Context) error {
			once.Do(func() { close(started) })
			return failure
		}, "stripe")
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("billing tick did not start")
	}
	for range 10000 {
		status := loop.Health(now)
		if status.Healthy || status.Failed["stripe"] != failure.Error() {
			t.Fatalf("failed billing tick briefly reported healthy: %+v", status)
		}
	}
}

func TestLoopHealthTickResultTransitions(t *testing.T) {
	now := time.Now()
	loop := &Loop{
		cfg:         &Config{SampleInterval: time.Hour, QuotaInterval: time.Hour, StripeInterval: time.Hour},
		lastTick:    map[string]time.Time{"sample": now, "quota": now},
		lastTickErr: make(map[string]string),
	}
	for i, tc := range []struct {
		name string
		err  error
	}{
		{"first failure", errors.New("billing unavailable")},
		{"retry failure", errors.New("billing still unavailable")},
		{"successful recovery", nil},
		{"failure after recovery", errors.New("billing failed again")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at := now.Add(time.Duration(i) * time.Second)
			loop.recordTick("stripe", at, tc.err)
			if got, ok := loop.LastTick("stripe"); !ok || !got.Equal(at) {
				t.Fatalf("LastTick = %v, %v; want %v, true", got, ok, at)
			}
			status := loop.Health(at)
			if status.Healthy != (tc.err == nil) {
				t.Fatalf("health does not reflect tick result %v: %+v", tc.err, status)
			}
			if tc.err != nil && status.Failed["stripe"] != tc.err.Error() {
				t.Fatalf("failure = %q, want %q", status.Failed["stripe"], tc.err.Error())
			}
			if tc.err == nil && len(status.Failed) != 0 {
				t.Fatalf("successful tick retained failures: %v", status.Failed)
			}
		})
	}
}
