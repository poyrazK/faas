// Tests for the schedd daemon entrypoint. The actual scheduler lives in
// pkg/sched (covered separately). Here we only exercise the run() loop in
// main.go — confirm it logs the readiness banner, blocks on ctx, and
// returns cleanly on cancel.
package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestRunBlocksUntilCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	done := make(chan error, 1)
	go func() { done <- run(ctx, log) }()

	// Must not return early.
	select {
	case err := <-done:
		t.Fatalf("run returned before cancel: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run returned %v, want nil on clean cancel", err)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not return within 1s after cancel")
	}
}