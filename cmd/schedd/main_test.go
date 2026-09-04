// Tests for the schedd daemon entrypoint. The real VM path needs KVM; here we
// exercise run()'s orchestration through the dependency-injection seam. Paths
// that need a live pool (LISTEN, migrate, seed) use pkg/db/pgtest, which skips
// when Postgres is unreachable; the pure config/open-failure paths run anywhere.

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/sched/instancestats"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// shortDir keeps unix socket paths under macOS's ~104-char sun_path limit.
func shortDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	short := "/tmp/fsst-schedd-" + t.Name()
	if err := os.Symlink(root, short); err != nil {
		return root
	}
	t.Cleanup(func() { _ = os.Remove(short) })
	return short
}

// migratedPool returns a pgtest pool with the schema migrated, or skips.
func migratedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

func TestRun_BadConfigPath(t *testing.T) {
	deps := runDeps{
		configPath: t.TempDir(), // a directory fails the non-ENOENT read
		capCheck:   func() error { return nil },
	}
	if err := runWithDeps(context.Background(), discardLog(), deps); err == nil {
		t.Fatal("expected error from directory-as-config-path")
	}
}

func TestRun_OpenDBFailurePropagates(t *testing.T) {
	wantErr := errors.New("db down")
	// Gate-B (PR-1): the role gate runs before DB open, so the
	// observed role must be one of schedd's allowed values
	// (RoleSingleBox / RoleControlPlane). Set the env so this
	// test exercises the DB-open path, not the role gate.
	t.Setenv("FAAS_SCHEDD_ROLE", string(role.RoleSingleBox))
	deps := runDeps{
		configPath: filepath.Join(t.TempDir(), "absent.toml"), // ENOENT => defaults
		capCheck:   func() error { return nil },
		openDB:     func(context.Context, string) (*pgxpool.Pool, error) { return nil, wantErr },
	}
	if err := runWithDeps(context.Background(), discardLog(), deps); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want wraps %v", err, wantErr)
	}
}

// TestRun_PartialVMMDFailsAtBoot validates that a config with a partial
// vmmd_tls_* cluster (only one of the three paths set) is rejected
// during startup rather than surfacing the error on the first dial.
// PR #113 made the dial lazy (router dials on first Wake), so the
// eager-boot-time "dial failure" path this test used to exercise
// no longer exists; the partial-TLS-cluster check is the equivalent
// loud-at-boot guarantee under the lazy model.
func TestRun_PartialVMMDFailsAtBoot(t *testing.T) {
	pool := migratedPool(t)
	dir := shortDir(t)
	cfgPath := filepath.Join(dir, "schedd.toml")
	// Exactly one of the three vmmd_tls_* keys is set — wire.LoadClientTLSConfigWithPrefix
	// rejects this with an error naming the missing fields.
	cfg := `socket_path = "` + filepath.Join(dir, "schedd.sock") + `"
vmmd_tls_cert_path = "/some/cert"
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	deps := runDeps{
		configPath: cfgPath,
		capCheck:   func() error { return nil },
		openDB:     func(context.Context, string) (*pgxpool.Pool, error) { return pool, nil },
		migrate:    func(context.Context, *pgxpool.Pool) error { return nil },
		detectFC:   func(context.Context) (string, error) { return "1.10.0", nil },
		dialVMM:    stubDialVMM,
	}
	if err := runWithDeps(context.Background(), discardLog(), deps); err == nil {
		t.Fatal("expected partial vmmd_tls_* cluster to fail at boot")
	}
}

func TestRun_ListenFailurePropagates(t *testing.T) {
	pool := migratedPool(t)
	wantErr := errors.New("listen broken")
	deps := runDeps{
		configPath:  filepath.Join(t.TempDir(), "absent.toml"),
		capCheck:    func() error { return nil },
		openDB:      func(context.Context, string) (*pgxpool.Pool, error) { return pool, nil },
		migrate:     func(context.Context, *pgxpool.Pool) error { return nil },
		detectFC:    func(context.Context) (string, error) { return "1.10.0", nil },
		dialVMM:     stubDialVMM,
		listen:      func(context.Context, string, *tls.Config, string) (net.Listener, error) { return nil, wantErr },
		signPubPath: writeTestSignPub(t),
	}
	if err := runWithDeps(context.Background(), discardLog(), deps); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want wraps %v", err, wantErr)
	}
}

// TestMain_AttachesInstanceStats pins the production wiring
// contract for the per-instance metrics poller (issue #170 / PR-A).
//
// runWithDeps is a one-shot function — it constructs the Loop,
// runs it, and tears down on cancel — so we can't observe the
// constructed Loop directly. Instead, this test mirrors the
// production wiring inline (a) by exercising the same code paths
// the production main.go uses (NewPoller, the dial adapter, and
// Loop.WithInstanceStats) and (b) by asserting the constructed
// poller has the right shape (non-nil Reader, non-nil Dialer,
// non-nil Metrics, Interval == DefaultStatsInterval). The reader
// is the canonical seam #171 / #169 will read from — its presence
// in the test verifies the production wiring would carry it
// through.
//
// The Loop's runInstanceStats helper is unexported, so we can't
// drive it from cmd/schedd (package main, not pkg/sched_test).
// The deeper integration is covered by
// pkg/sched/loop_instancestats_test.go. Here we pin the
// production-shape wiring only.
func TestMain_AttachesInstanceStats(t *testing.T) {
	log := discardLog()
	ops := wire.NewOpsMetrics("schedd")
	reader := instancestats.NewReader()
	dialer := instancestats.DialerFunc(stubDialVMM)
	poller := instancestats.NewPoller(state.NewMemStore(), dialer, nil, reader, ops, log)

	// Reader is non-nil + empty (no Replace yet).
	if reader == nil {
		t.Fatal("reader is nil after NewReader()")
	}
	if got := reader.SnapshotAll(); got != nil {
		t.Errorf("SnapshotAll on fresh Reader = %v, want nil", got)
	}

	// Poller wired correctly: non-nil, default cadence.
	if poller == nil {
		t.Fatal("poller is nil after NewPoller()")
	}
	if got := poller.TickInterval(); got != instancestats.DefaultStatsInterval {
		t.Errorf("TickInterval = %v, want %v", got, instancestats.DefaultStatsInterval)
	}

	// WithInstanceStats accepts the poller (compile-time guarantee
	// that *instancestats.Poller satisfies sched.InstanceStatsPoller;
	// this is the load-bearing part of the wiring contract).
	chain := sched.NewLoop(nil, nil, log).
		WithWatchdog(nil).
		WithRetention(nil).
		WithHeartbeat(nil).
		WithInstanceStats(poller)
	if chain == nil {
		t.Fatal("WithInstanceStats returned nil chain")
	}
}

// TestRun_DrainsOnCancel exercises the happy early-lifecycle path end to end:
// config, migrated pool, injected FC + vmm, a real unix listener + LISTEN loop,
// then cancel → clean nil return.
func TestRun_DrainsOnCancel(t *testing.T) {
	pool := migratedPool(t)
	dir := shortDir(t)
	cfgPath := filepath.Join(dir, "schedd.toml")
	cfg := "socket_path = \"" + filepath.Join(dir, "schedd.sock") + "\"\nowner_user = \"root\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	deps := runDeps{
		configPath:  cfgPath,
		capCheck:    func() error { return nil },
		openDB:      func(context.Context, string) (*pgxpool.Pool, error) { return pool, nil },
		migrate:     func(context.Context, *pgxpool.Pool) error { return nil },
		detectFC:    func(context.Context) (string, error) { return "1.10.0", nil },
		dialVMM:     stubDialVMM,
		signPubPath: writeTestSignPub(t),
		listen: func(_ context.Context, target string, _ *tls.Config, _ string) (net.Listener, error) {
			t2, err := wire.ParseTarget(target)
			if err != nil {
				return nil, err
			}
			return net.Listen("unix", t2.Address)
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithDeps(ctx, discardLog(), deps) }()
	// Give run() enough time to reach the DB subscribe loop before
	// cancel. 50 ms was too tight on busy CI runners: the listener
	// acquisition raced the cancel and surfaced as
	// "SubscribeWithReconnect ... context canceled" instead of a clean
	// nil drain. 200 ms is still far under the 3 s watchdog below.
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run returned %v, want nil on clean drain", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run did not return within 3s of cancel")
	}
}

// writeTestSignPub generates a fresh ECDSA P-256 cosign keypair and
// writes the public half to a temp file in mode 0444. ADR-038 / Tier 3
// phase 3: schedd's run() load step (cmd/schedd/main.go:232-242) reads
// FAAS_SIGN_PUB / cosign.DefaultSignPubPath and fails loud on missing.
// Tests that drive run() through runWithDeps inject the path via the
// runDeps.signPubPath seam; the underlying file must exist and pass
// the PEM + perm checks cosign.NewLocalVerifier runs.
//
// Mirrors the e2etest harness helper (pkg/e2etest/harness.go::
// writeScheddSignPub) — same shape because both surfaces face the same
// fail-loud contract.
func writeTestSignPub(t *testing.T) string {
	t.Helper()
	_, pubPEM, err := cosign.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate cosign keypair: %v", err)
	}
	path := filepath.Join(t.TempDir(), "sign-pub.pem")
	if err := os.WriteFile(path, pubPEM, 0o444); err != nil {
		t.Fatalf("write sign-pub.pem: %v", err)
	}
	return path
}

// stubDialVMM is the canonical no-op dialer for the wiring tests
// (no VM is booted, no net dial happens). Every runDeps in this
// file uses it so the runDeps literal stays one line per knob;
// production main.go passes deps.dialVMM, which is the real
// overlay.Dial.
func stubDialVMM(context.Context, string, *tls.Config) (sched.VMM, error) {
	return stubVMM{}, nil
}

// stubVMM is a no-op sched.VMM for the wiring tests (no VM is booted).
type stubVMM struct{}

func (stubVMM) CreateColdBoot(context.Context, string, sched.AppSpec) (*sched.WakeOutcome, error) {
	return &sched.WakeOutcome{}, nil
}
func (stubVMM) CreateFromSnapshot(context.Context, string, sched.AppSpec, sched.SnapshotRef) (*sched.WakeOutcome, error) {
	return &sched.WakeOutcome{}, nil
}
func (stubVMM) PauseAndSnapshot(context.Context, string, string, string, string) (sched.SnapshotBytes, error) {
	return sched.SnapshotBytes{}, nil
}

// WarmSnapshot (issue #470 / PR #470-FU-A) is the cmd/schedd
// main_test stubVMM's no-op seam — the schedd-bootstrap integration
// test doesn't exercise the warm-capture path (it lives in
// pkg/sched/engine_test.go).
func (stubVMM) WarmSnapshot(context.Context, string, string, string) (sched.SnapshotBytes, error) {
	return sched.SnapshotBytes{}, nil
}
func (stubVMM) Destroy(context.Context, string) error { return nil }
func (stubVMM) Ping(context.Context) (*sched.PingOutcome, error) {
	return &sched.PingOutcome{FcVersion: "1.10.0"}, nil
}

// Stats implements sched.VMM (issue #170 / PR-A). Wiring tests
// don't observe instance metrics; return empty snapshot.
func (stubVMM) Stats(context.Context) (*sched.StatsSnapshot, error) {
	return &sched.StatsSnapshot{}, nil
}
func (stubVMM) Close() error { return nil }

// Tier A5 (ADR-066) — wiring tests don't drive migration; the
// four RPCs are no-op stubs.
func (stubVMM) PrepareLiveMigration(context.Context, string, string, string) (sched.LiveMigrationPrepare, error) {
	return sched.LiveMigrationPrepare{}, nil
}
func (stubVMM) AdoptMigratedInstance(context.Context, string, string, sched.AppSpec, string, string, string) (sched.LiveMigrationAdopt, error) {
	return sched.LiveMigrationAdopt{}, nil
}
func (stubVMM) AcknowledgeMigration(context.Context, string, string, string) error {
	return nil
}
func (stubVMM) CancelLiveMigration(context.Context, string, string, string) error {
	return nil
}

// FrameworkReady (issue #470) — wiring tests don't drive the
// guest-init framework-ready DGRAM path; the vmmdgrpc handler tests
// do. Returns nil so the VMM contract is satisfied.
func (stubVMM) FrameworkReady(context.Context, string, int64) error {
	return nil
}

// UpdateEgressAllowlist (tier-2 PR-B) — wiring tests don't drive
// the egress drift path. Returns nil so the VMM contract is
// satisfied when schedd's deps.subscribeEgressDrift is left
// nil (the production subscriber is not started in these tests).
func (stubVMM) UpdateEgressAllowlist(context.Context, string, []netip.Prefix) error {
	return nil
}

// UpdateStaticEgressIP (ADR-119) — wiring tests don't drive
// the static-IP drift path. Returns nil so the VMM contract is
// satisfied when schedd's deps.subscribeEgressDrift is left
// nil (the production subscriber is not started in these tests).
// Mirrors UpdateEgressAllowlist above.
func (stubVMM) UpdateStaticEgressIP(context.Context, string, string, string) error {
	return nil
}

// StopInstance (M-2 / ADR-138 §Decision 1) satisfies the
// sched.VMM contract. Wiring tests don't drive the per-mode
// dispatch path (Engine.StopInstance is exercised in pkg/sched/
// test_table); the stub returns a zero-value outcome so the
// interface stays compile-clean while the real wire goes
// through vmmdgrpc.
func (stubVMM) StopInstance(context.Context, string, int32, int32) (*sched.StopInstanceOutcome, error) {
	return &sched.StopInstanceOutcome{}, nil
}

// Logs (issue #254 / Move 4, issue #517 / PR-B) — wiring tests
// don't drive the log stream path; the scheddgrpc handler tests
// do. Returns nil + io.EOF so any accidental caller exits cleanly.
// PR-B adds the sinceWrittenAt time lower-bound; the fake ignores
// it.
func (stubVMM) Logs(context.Context, string, int64, time.Time) (sched.LogStream, error) {
	return nil, io.EOF
}

// TestBrokerEgressConfigFromEnv covers PR #993 / issue #757
// review MED-3 — the env-var seam that wires the broker egress
// shaper into the dispatch loop. The helper itself is a pure
// function over os.Getenv, so no runWithDeps fixture is needed
// (the regression gate for the wiring side is the unit test in
// pkg/sched + the e2e suite; this table covers the parser).
//
// Cases:
//
//   - unset             → (zero, false, nil): noop accountor stays.
//   - positive, default IFNAME → (cfg, true, nil): iface = "faas-brokerq".
//   - positive, custom  IFNAME → (cfg, true, nil): iface from env.
//   - zero              → error: 0 must be positive (the field is mbit).
//   - negative          → error: -1 must be positive.
//   - non-numeric       → error: "lemon" parses to err.
//   - error mentions the env var name so operators can grep.
func TestBrokerEgressConfigFromEnv(t *testing.T) {
	cases := []struct {
		name      string
		mbit      string // empty → env unset
		ifname    string // empty → env unset
		wantOK    bool
		wantErr   bool
		wantMbit  int
		wantIface string
	}{
		{
			name:   "unset_returns_noop",
			wantOK: false,
		},
		{
			name:      "positive_uses_default_ifname",
			mbit:      "500",
			wantOK:    true,
			wantMbit:  500,
			wantIface: "faas-brokerq",
		},
		{
			name:      "positive_custom_ifname",
			mbit:      "1000",
			ifname:    "eth-broker",
			wantOK:    true,
			wantMbit:  1000,
			wantIface: "eth-broker",
		},
		{
			name:    "zero_rejected",
			mbit:    "0",
			wantErr: true,
		},
		{
			name:    "negative_rejected",
			mbit:    "-1",
			wantErr: true,
		},
		{
			name:    "malformed_rejected",
			mbit:    "lemon",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Both env vars default to unset; tests set them
			// only when the case exercises a value.
			if tc.mbit != "" {
				t.Setenv("FAAS_BROKER_EGRESS_MBIT", tc.mbit)
			} else {
				// Ensure no leakage from a sibling test.
				t.Setenv("FAAS_BROKER_EGRESS_MBIT", "")
			}
			if tc.ifname != "" {
				t.Setenv("FAAS_BROKER_EGRESS_IFNAME", tc.ifname)
			} else {
				t.Setenv("FAAS_BROKER_EGRESS_IFNAME", "")
			}

			cfg, ok, err := brokerEgressConfigFromEnv()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("err = nil, want non-nil parse error")
				}
				if !strings.Contains(err.Error(), "FAAS_BROKER_EGRESS_MBIT") {
					t.Errorf("err = %v, want FAAS_BROKER_EGRESS_MBIT prefix", err)
				}
				if ok {
					t.Errorf("ok = true on error path, want false")
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return // noop path; cfg must be zero.
			}
			if cfg.EgressMbit != tc.wantMbit {
				t.Errorf("EgressMbit = %d, want %d", cfg.EgressMbit, tc.wantMbit)
			}
			if cfg.InterfaceName != tc.wantIface {
				t.Errorf("InterfaceName = %q, want %q", cfg.InterfaceName, tc.wantIface)
			}
		})
	}
}
