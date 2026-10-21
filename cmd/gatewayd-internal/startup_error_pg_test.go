//go:build !no_pg

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestRun_StartupErrorExitsInsteadOfHanging reproduces the nightly e2e hang:
// gatewayd-internal could not create its egress replay ledger, runWithDeps
// returned the error, and the daemon then blocked in pool.Close forever. The
// invalidation watcher held the notify hub's LISTEN connection under the
// daemon context, which nothing cancelled, so the error was never logged.
//
// The body mirrors run: the LISTEN is established before runWithDeps starts.
func TestRun_StartupErrorExitsInsteadOfHanging(t *testing.T) {
	pool := pgtest.Open(t)
	// A ledger directory under a regular file fails even for root.
	notDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_EGRESS_SOCKET", filepath.Join(t.TempDir(), "egress.sock"))
	deps := defaultDeps()
	deps.capCheck = func() error { return nil }
	deps.pool = pool
	deps.egressPendingPath = filepath.Join(notDir, "egress-meter", "pending.json")
	var logs safeBuffer
	log := slog.New(slog.NewTextHandler(&logs, nil))

	watcherDone := make(chan struct{})
	body := func(ctx context.Context) error {
		subscribed := make(chan struct{})
		go func() {
			defer close(watcherDone)
			watchInvalidations(ctx, pool, &fakeInvalidator{}, log, subscribed)
		}()
		select {
		case <-subscribed:
		case <-time.After(10 * time.Second):
			t.Error("invalidation LISTEN was not established")
		}
		return runWithDeps(ctx, log, deps)
	}
	done := make(chan error, 1)
	go func() { done <- runThenClosePool(t.Context(), log, pool.Close, body) }()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "open durable egress replay ledger") {
			t.Fatalf("run returned %v, want the egress replay ledger error", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return within 10s of a startup error; pool.Close is waiting on a connection a background goroutine still holds")
	}
	select {
	case <-watcherDone:
	case <-time.After(5 * time.Second):
		t.Error("the invalidation watcher kept running after run returned")
	}
	if err := pool.Ping(context.Background()); err == nil {
		t.Error("the pool is still open after run returned")
	}
	if !strings.Contains(logs.String(), "open durable egress replay ledger") {
		t.Errorf("the startup error was not logged; logs:\n%s", logs.String())
	}
}
