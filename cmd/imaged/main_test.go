// imaged daemon entrypoint — cover the run() loop.
package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestRunBlocksUntilCancel(t *testing.T) {
	// Per contextcheck: the goroutine owns its cancellable ctx; the test
	// signals cancel via a dedicated channel rather than capturing its own
	// ctx into the goroutine.
	stop := make(chan struct{})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-stop
			cancel()
		}()
		done <- run(ctx, log)
	}()

	select {
	case err := <-done:
		t.Fatalf("run returned before cancel: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(stop)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run returned %v, want nil on clean cancel", err)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not return within 1s after cancel")
	}
}
