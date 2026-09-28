package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestCallbackReplaySupervisorRetriesAfterUnexpectedExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var attempts atomic.Int32
	var restarts atomic.Int32
	restarted := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		superviseCallbackReplay(ctx, discardLogger(), time.Millisecond, 4*time.Millisecond, func(runCtx context.Context) error {
			if attempts.Add(1) == 1 {
				return errors.New("temporary outbox failure")
			}
			close(restarted)
			<-runCtx.Done()
			return runCtx.Err()
		}, func() { restarts.Add(1) })
	}()

	select {
	case <-restarted:
	case <-time.After(time.Second):
		t.Fatal("replay supervisor did not restart")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("replay supervisor did not stop after cancellation")
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("replay attempts = %d, want 2", got)
	}
	if got := restarts.Load(); got != 1 {
		t.Fatalf("replay restarts = %d, want 1", got)
	}
}

func TestCallbackReplaySupervisorStopsDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var attempts atomic.Int32
	var restarts atomic.Int32
	failed := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		superviseCallbackReplay(ctx, discardLogger(), time.Hour, time.Hour, func(context.Context) error {
			attempts.Add(1)
			close(failed)
			return errors.New("temporary outbox failure")
		}, func() { restarts.Add(1) })
	}()

	select {
	case <-failed:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("replay supervisor did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("replay supervisor remained in backoff after cancellation")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("replay attempts = %d, want 1", got)
	}
	if got := restarts.Load(); got != 0 {
		t.Fatalf("replay restarts during canceled backoff = %d, want 0", got)
	}
}

func TestNextCallbackReplayDelayCapsWithoutOverflow(t *testing.T) {
	tests := []struct {
		name    string
		current time.Duration
		max     time.Duration
		want    time.Duration
	}{
		{name: "doubles", current: time.Second, max: 30 * time.Second, want: 2 * time.Second},
		{name: "caps", current: 16 * time.Second, max: 30 * time.Second, want: 30 * time.Second},
		{name: "stays capped", current: 30 * time.Second, max: 30 * time.Second, want: 30 * time.Second},
		{name: "avoids overflow", current: time.Duration(1<<62 + 1), max: time.Duration(1<<62 + 2), want: time.Duration(1<<62 + 2)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextCallbackReplayDelay(tt.current, tt.max); got != tt.want {
				t.Fatalf("nextCallbackReplayDelay(%s, %s) = %s, want %s", tt.current, tt.max, got, tt.want)
			}
		})
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
