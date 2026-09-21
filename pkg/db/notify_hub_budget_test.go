package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestDaemonMaxConnectionsFollowsNotifyHubMode pins the coupling this file
// exists to break.
//
// The hub-on table is the one production runs and the one a future capacity
// cut will lower. The hub-off table must keep the pre-ADR-190 sizing, because
// on that path every subscriber parks its own connection. If the two tables
// were ever allowed to converge again, lowering the hub-on numbers would
// silently make FAAS_DB_NOTIFY_HUB=0 unbootable — the kill switch would stop
// being a rollback and become a second outage.
func TestDaemonMaxConnectionsFollowsNotifyHubMode(t *testing.T) {
	for _, daemon := range []string{"schedd", "gatewayd-internal", "apid"} {
		hubOff, ok := DaemonMaxConnectionsNotifyHubDisabled[daemon]
		if !ok {
			t.Fatalf("%s has no hub-disabled budget; the kill switch would fall back to the hub-on cap", daemon)
		}
		hubOn, ok := DaemonMaxConnections[daemon]
		if !ok {
			t.Fatalf("%s has no hub-on budget", daemon)
		}
		// The hub can only ever reduce the connections a daemon needs, so a
		// hub-off budget below the hub-on budget is always a mistake.
		if hubOff < hubOn {
			t.Errorf("%s: hub-disabled budget %d < hub-on budget %d — the legacy path needs MORE connections, never fewer",
				daemon, hubOff, hubOn)
		}
	}
}

// TestDaemonMaxConnectionsResolvesPerMode pins the resolver, including the
// "faas-" prefix strip that every production caller relies on
// (OpenWithAppName tags pools as "faas-schedd", not "schedd").
func TestDaemonMaxConnectionsResolvesPerMode(t *testing.T) {
	tests := []struct {
		name    string
		hubEnv  string
		appName string
		want    int32
	}{
		{"hub on resolves the hub-on table", "", "faas-schedd", DaemonMaxConnections["schedd"]},
		{"hub explicitly on resolves the hub-on table", "1", "faas-schedd", DaemonMaxConnections["schedd"]},
		{"hub off resolves the legacy table", "0", "faas-schedd", DaemonMaxConnectionsNotifyHubDisabled["schedd"]},
		{"hub off, unprefixed name", "0", "gatewayd-internal", DaemonMaxConnectionsNotifyHubDisabled["gatewayd-internal"]},
		// A daemon absent from both tables has no LISTEN subscribers worth
		// reserving for; both modes give it the same default.
		{"unknown daemon, hub on", "", "faas-unknown", defaultMaxConnections},
		{"unknown daemon, hub off", "0", "faas-unknown", defaultMaxConnections},
		{"empty app name", "", "", defaultMaxConnections},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(NotifyHubEnv, tc.hubEnv)
			if got := daemonMaxConnections(tc.appName); got != tc.want {
				t.Errorf("daemonMaxConnections(%q) with %s=%q = %d, want %d",
					tc.appName, NotifyHubEnv, tc.hubEnv, got, tc.want)
			}
		})
	}
}

// TestLegacySubscribeFailsFastOnExhaustedPool is the behaviour change.
//
// Before this, a daemon running FAAS_DB_NOTIFY_HUB=0 against a pool too small
// to seat every subscriber blocked inside pool.Acquire forever: the
// connections it was waiting on were held by its own earlier subscribers, so
// no release was ever coming. The unit sat in `activating` with no error
// until systemd's TimeoutStartSec killed it — the failure mode the comments
// in db.go describe as "deadlocks before sd_notify(READY=1)".
//
// The test holds the pool's only connection and asserts that a legacy-path
// subscribe RETURNS, with an error naming the kill switch, rather than
// hanging until the caller's context expires. The outer context deliberately
// outlives the internal timeout so that a regression to the old behaviour
// fails this test by timing out rather than by returning early.
func TestLegacySubscribeFailsFastOnExhaustedPool(t *testing.T) {
	t.Setenv(NotifyHubEnv, "0")
	pool := openTestPoolWithMaxConns(t, 1)

	// Shorten the production timeout: the test has to actually reach it, and
	// the behaviour under test is "bounded, with a named error", not the
	// specific duration.
	restore := legacySubscribeAcquireTimeout
	legacySubscribeAcquireTimeout = 250 * time.Millisecond
	t.Cleanup(func() { legacySubscribeAcquireTimeout = restore })

	// Hold the only connection, exactly as an earlier subscriber would.
	held, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer held.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 20*legacySubscribeAcquireTimeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := SubscribeWithReconnect(ctx, pool, []string{"budget_test_channel"}, nil)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("SubscribeWithReconnect succeeded against a fully-held pool, want a bounded failure")
		}
		if ctx.Err() != nil {
			t.Fatalf("subscribe only returned once the CALLER's context expired (%v) — the initial acquire is still unbounded", ctx.Err())
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("error = %v, want it to wrap context.DeadlineExceeded", err)
		}
		// The message has to name the cause: an operator reading this in a
		// journal has no other clue that the kill switch is what broke boot.
		if !strings.Contains(err.Error(), NotifyHubEnv) {
			t.Errorf("error %q does not mention %s; an operator cannot act on it", err, NotifyHubEnv)
		}
	case <-time.After(2 * legacySubscribeAcquireTimeout):
		t.Fatal("SubscribeWithReconnect did not return within twice its own acquire timeout — it is still hanging")
	}
}
