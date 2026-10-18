package main

// Coverage sweep for cmd/schedd/main.go FAAS_* env-override parsing
// + the envOr helper that PR #753 did not reach. Pattern: every
// test injects a real pgtest pool + a working listen seam and
// waits for validation failure or readiness before ctx cancel.
//
// Driven by the coverage profile on origin/main — covers ~10
// remaining 0%-coverage branches in cmd/schedd/main.go. Stays
// deterministic; no timing-dependent assertions.

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/wire"
)

// envListenSeam is a unix-socket listener that captures the address
// the production listen() would resolve to. The env blocks sit
// AFTER listen acquisition (cmd/schedd/main.go:747+), so a working
// listener is required to reach them.
func envListenSeam(_ context.Context, target string, _ *tls.Config, _ string) (net.Listener, error) {
	t2, err := wire.ParseTarget(target)
	if err != nil {
		return nil, err
	}
	return net.Listen("unix", t2.Address)
}

// runScheddWithEnv drives runWithDeps to the env-override block
// and back. Invalid overrides return their parse error. Valid
// overrides must reach the daemon's readiness notification before
// cancellation, which proves all startup env blocks executed.
func runScheddWithEnv(t *testing.T, env map[string]string) error {
	t.Helper()
	pool := migratedPool(t)
	dir := shortDir(t)
	cfgPath := filepath.Join(dir, "schedd.toml")
	cfg := "socket_path = \"" + filepath.Join(dir, "schedd.sock") + "\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		return err
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	notifyPath := filepath.Join(dir, "notify.sock")
	notify, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: notifyPath, Net: "unixgram"})
	if err != nil {
		return err
	}
	defer func() { _ = notify.Close() }()
	t.Setenv("NOTIFY_SOCKET", notifyPath)
	ready := make(chan struct{})
	go func() {
		buf := make([]byte, 256)
		for {
			n, _, err := notify.ReadFromUnix(buf)
			if err != nil {
				return
			}
			if string(buf[:n]) == "READY=1" {
				close(ready)
				return
			}
		}
	}()
	deps := runDeps{
		configPath:  cfgPath,
		capCheck:    func() error { return nil },
		openDB:      func(context.Context, string) (*pgxpool.Pool, error) { return pool, nil },
		migrate:     func(context.Context, *pgxpool.Pool) error { return nil },
		detectFC:    func(context.Context) (string, error) { return "1.10.0", nil },
		dialVMM:     stubDialVMM,
		signPubPath: writeTestSignPub(t),
		listen:      envListenSeam,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runWithDeps(ctx, discardLog(), deps) }()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	select {
	case err := <-done:
		if err == nil {
			t.Error("runWithDeps exited before reporting readiness")
		}
		return err
	case <-ready:
		cancel()
	case <-deadline.C:
		t.Fatal("runWithDeps neither validated configuration nor became ready within 3s")
	}
	select {
	case err := <-done:
		return err
	case <-deadline.C:
		t.Fatal("runWithDeps did not finish startup and cancellation within 3s")
		return nil
	}
}

func TestRun_EnvRebalanceCooldown_BadValue(t *testing.T) {
	err := runScheddWithEnv(t, map[string]string{
		"FAAS_REBALANCE_COOLDOWN_SECONDS": "not-a-number",
	})
	if err == nil {
		t.Fatal("expected error for bad FAAS_REBALANCE_COOLDOWN_SECONDS")
	}
	if !strings.Contains(err.Error(), "FAAS_REBALANCE_COOLDOWN_SECONDS") {
		t.Errorf("err = %v, want FAAS_REBALANCE_COOLDOWN_SECONDS prefix", err)
	}
}

func TestRun_EnvRebalanceCooldown_Zero(t *testing.T) {
	err := runScheddWithEnv(t, map[string]string{
		"FAAS_REBALANCE_COOLDOWN_SECONDS": "0",
	})
	if err == nil {
		t.Fatal("expected error for FAAS_REBALANCE_COOLDOWN_SECONDS=0 (must be positive)")
	}
}

func TestRun_EnvRebalanceCooldown_BadMaxPerTick(t *testing.T) {
	// Cooldown is valid; the inner MAX_PER_TICK is bad.
	err := runScheddWithEnv(t, map[string]string{
		"FAAS_REBALANCE_COOLDOWN_SECONDS": "30",
		"FAAS_REBALANCE_MAX_PER_TICK":     "not-a-number",
	})
	if err == nil {
		t.Fatal("expected error for bad FAAS_REBALANCE_MAX_PER_TICK")
	}
	if !strings.Contains(err.Error(), "FAAS_REBALANCE_MAX_PER_TICK") {
		t.Errorf("err = %v, want FAAS_REBALANCE_MAX_PER_TICK prefix", err)
	}
}

func TestRun_EnvRebalanceMaxPerTickOnly_BadValue(t *testing.T) {
	// Cooldown is unset; only MAX_PER_TICK is set, and it's bad.
	err := runScheddWithEnv(t, map[string]string{
		"FAAS_REBALANCE_MAX_PER_TICK": "not-a-number",
	})
	if err == nil {
		t.Fatal("expected error for bad FAAS_REBALANCE_MAX_PER_TICK")
	}
}

func TestRun_EnvMigrateLiveMaxPerTick_BadValue(t *testing.T) {
	err := runScheddWithEnv(t, map[string]string{
		"FAAS_MIGRATE_LIVE_MAX_PER_TICK": "garbage",
	})
	if err == nil {
		t.Fatal("expected error for bad FAAS_MIGRATE_LIVE_MAX_PER_TICK")
	}
	if !strings.Contains(err.Error(), "FAAS_MIGRATE_LIVE_MAX_PER_TICK") {
		t.Errorf("err = %v, want FAAS_MIGRATE_LIVE_MAX_PER_TICK prefix", err)
	}
}

func TestRun_EnvMigrateLiveMaxPerTick_Zero(t *testing.T) {
	err := runScheddWithEnv(t, map[string]string{
		"FAAS_MIGRATE_LIVE_MAX_PER_TICK": "0",
	})
	if err == nil {
		t.Fatal("expected error for FAAS_MIGRATE_LIVE_MAX_PER_TICK=0 (must be positive)")
	}
}

func TestRun_EnvMigrateLiveLeaseSeconds_BadValue(t *testing.T) {
	err := runScheddWithEnv(t, map[string]string{
		"FAAS_MIGRATE_LIVE_LEASE_SECONDS": "lemon",
	})
	if err == nil {
		t.Fatal("expected error for bad FAAS_MIGRATE_LIVE_LEASE_SECONDS")
	}
	if !strings.Contains(err.Error(), "FAAS_MIGRATE_LIVE_LEASE_SECONDS") {
		t.Errorf("err = %v, want FAAS_MIGRATE_LIVE_LEASE_SECONDS prefix", err)
	}
}

func TestRun_EnvMigratingWatchdogTickLimit_BadValue(t *testing.T) {
	err := runScheddWithEnv(t, map[string]string{
		"FAAS_MIGRATING_WATCHDOG_TICK_LIMIT": "broken",
	})
	if err == nil {
		t.Fatal("expected error for bad FAAS_MIGRATING_WATCHDOG_TICK_LIMIT")
	}
	if !strings.Contains(err.Error(), "FAAS_MIGRATING_WATCHDOG_TICK_LIMIT") {
		t.Errorf("err = %v, want FAAS_MIGRATING_WATCHDOG_TICK_LIMIT prefix", err)
	}
}

func TestRun_EnvMigratingWatchdogIntervalSeconds_BadValue(t *testing.T) {
	err := runScheddWithEnv(t, map[string]string{
		"FAAS_MIGRATING_WATCHDOG_INTERVAL_SECONDS": "wat",
	})
	if err == nil {
		t.Fatal("expected error for bad FAAS_MIGRATING_WATCHDOG_INTERVAL_SECONDS")
	}
	if !strings.Contains(err.Error(), "FAAS_MIGRATING_WATCHDOG_INTERVAL_SECONDS") {
		t.Errorf("err = %v, want FAAS_MIGRATING_WATCHDOG_INTERVAL_SECONDS prefix", err)
	}
}

func TestRun_EnvDeadNodeReconcilerStaleness_BadValue(t *testing.T) {
	err := runScheddWithEnv(t, map[string]string{
		"FAAS_DEAD_NODE_RECONCILER_STALENESS_SECONDS": "broken",
	})
	if err == nil {
		t.Fatal("expected error for bad FAAS_DEAD_NODE_RECONCILER_STALENESS_SECONDS")
	}
	if !strings.Contains(err.Error(), "FAAS_DEAD_NODE_RECONCILER_STALENESS_SECONDS") {
		t.Errorf("err = %v, want FAAS_DEAD_NODE_RECONCILER_STALENESS_SECONDS prefix", err)
	}
}

func TestRun_EnvDeadNodeReconcilerInterval_BadValue(t *testing.T) {
	err := runScheddWithEnv(t, map[string]string{
		"FAAS_DEAD_NODE_RECONCILER_INTERVAL_SECONDS": "broken",
	})
	if err == nil {
		t.Fatal("expected error for bad FAAS_DEAD_NODE_RECONCILER_INTERVAL_SECONDS")
	}
	if !strings.Contains(err.Error(), "FAAS_DEAD_NODE_RECONCILER_INTERVAL_SECONDS") {
		t.Errorf("err = %v, want FAAS_DEAD_NODE_RECONCILER_INTERVAL_SECONDS prefix", err)
	}
}

func TestRun_EnvFloorIntervalSeconds_BadValue(t *testing.T) {
	err := runScheddWithEnv(t, map[string]string{
		"FAAS_FLOOR_INTERVAL_SECONDS": "broken",
	})
	if err == nil {
		t.Fatal("expected error for bad FAAS_FLOOR_INTERVAL_SECONDS")
	}
	if !strings.Contains(err.Error(), "FAAS_FLOOR_INTERVAL_SECONDS") {
		t.Errorf("err = %v, want FAAS_FLOOR_INTERVAL_SECONDS prefix", err)
	}
}

// TestRun_EnvHappyPath exercises every env knob at a valid value so
// we cover the With* happy branches in one test. Confirms the
// production wiring tolerates the operator overrides.
func TestRun_EnvHappyPath(t *testing.T) {
	err := runScheddWithEnv(t, map[string]string{
		"FAAS_REBALANCE_COOLDOWN_SECONDS":             "30",
		"FAAS_REBALANCE_MAX_PER_TICK":                 "5",
		"FAAS_MIGRATE_LIVE_MAX_PER_TICK":              "2",
		"FAAS_MIGRATE_LIVE_LEASE_SECONDS":             "60",
		"FAAS_MIGRATING_WATCHDOG_TICK_LIMIT":          "10",
		"FAAS_MIGRATING_WATCHDOG_INTERVAL_SECONDS":    "5",
		"FAAS_DEAD_NODE_RECONCILER_STALENESS_SECONDS": strconv.Itoa(120),
		"FAAS_DEAD_NODE_RECONCILER_INTERVAL_SECONDS":  strconv.Itoa(30),
		"FAAS_FLOOR_INTERVAL_SECONDS":                 strconv.Itoa(5),
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("happy-path envs returned err = %v, want nil/cancel", err)
	}
}

// TestRun_EnvOr confirms the envOr helper resolves to the default
// when unset and to the override when set.
func TestRun_EnvOr(t *testing.T) {
	if got := envOr("FAAS_TEST_DEFINITELY_NOT_SET_xyz", "fallback"); got != "fallback" {
		t.Errorf("envOr unset = %q, want fallback", got)
	}
	t.Setenv("FAAS_TEST_DEFINITELY_SET_xyz", "explicit")
	if got := envOr("FAAS_TEST_DEFINITELY_SET_xyz", "fallback"); got != "explicit" {
		t.Errorf("envOr set = %q, want explicit", got)
	}
}
