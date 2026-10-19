package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// TestRunThenClosePool_StopsBackgroundWorkBeforeClosing pins the order run
// depends on without Postgres: the closer stands in for pgxpool.Close, which
// waits until a LISTEN loop running under the body's context releases its
// connection. The error must be logged before that wait starts.
func TestRunThenClosePool_StopsBackgroundWorkBeforeClosing(t *testing.T) {
	startErr := errors.New("gatewayd: open durable egress replay ledger: genslot: create /var/lib/faas/egress-meter: permission denied")
	var logs safeBuffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	released := make(chan struct{})
	body := func(ctx context.Context) error {
		go func() {
			<-ctx.Done()
			close(released)
		}()
		return startErr
	}
	var loggedBeforeClose bool
	closePool := func() {
		loggedBeforeClose = strings.Contains(logs.String(), startErr.Error())
		<-released
	}

	done := make(chan error, 1)
	go func() { done <- runThenClosePool(t.Context(), log, closePool, body) }()
	select {
	case err := <-done:
		if !errors.Is(err, startErr) {
			t.Fatalf("returned %v, want %v", err, startErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runThenClosePool hung: the pool closed before the body's background work was stopped")
	}
	if !loggedBeforeClose {
		t.Errorf("the error was not logged before the pool closed; logs:\n%s", logs.String())
	}
}

// TestRunThenClosePool_CleanExitLogsNothing keeps a clean shutdown quiet.
func TestRunThenClosePool_CleanExitLogsNothing(t *testing.T) {
	var logs safeBuffer
	closed := false
	err := runThenClosePool(t.Context(), slog.New(slog.NewTextHandler(&logs, nil)), func() { closed = true },
		func(context.Context) error { return nil })
	if err != nil || !closed {
		t.Fatalf("err=%v closed=%v, want nil and true", err, closed)
	}
	if logs.String() != "" {
		t.Errorf("clean exit logged:\n%s", logs.String())
	}
}
