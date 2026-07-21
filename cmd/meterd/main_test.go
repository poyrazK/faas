package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// discardLog mirrors the meterd-side test fixture style. Pulled here because
// this is the only test file in package main.
func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// shortDir returns a short temp dir name. Schedd's equivalent has the same
// purpose — Linux sun_path is 108 bytes and macOS test paths can blow past
// that if the user has a deep $TMPDIR.
func shortDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// writeMeterdConfig plants a minimal meterd.toml in dir and returns its path.
// Tests that exercise runWithDeps's config-driven behavior should use this so
// they don't accidentally depend on /etc/faas/meterd.toml.
func writeMeterdConfig(t *testing.T, dir, metricsAddr string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("schedd_socket = \"" + filepath.Join(dir, "schedd.sock") + "\"\n")
	b.WriteString("db_url = \"\"\n")
	if metricsAddr != "" {
		b.WriteString("metrics_addr = \"" + metricsAddr + "\"\n")
	}
	p := filepath.Join(dir, "meterd.toml")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return p
}

// stubMeterdDeps returns a runDeps that doesn't open a real database or dial
// schedd — the test supplies pre-populated parker/stripe and stub
// collaborators so runWithDeps passes its early exits without touching the
// host. This is the meterd-side equivalent of schedd's "drains on cancel"
// test seam.
func stubMeterdDeps(cfgPath, metricsAddr string, pool *pgxpool.Pool, listenFn func(string, http.Handler) (*http.Server, error)) runDeps {
	return runDeps{
		configPath: cfgPath,
		openDB: func(context.Context, string) (*pgxpool.Pool, error) {
			return pool, nil
		},
		migrate:               func(context.Context, *pgxpool.Pool) error { return nil },
		loadMeter:             func(c *Config) (*meter.Config, error) { return c.Meter, nil },
		getenv:                func(string) string { return "" },
		dialSchedd:            func(string) (parkInstanceParker, error) { return &nopParker{}, nil },
		newStripeClient:       nil, // skipped when stripe is pre-populated
		parker:                &nopParker{},
		stripe:                &nopStripe{},
		mailer:                nil,
		now:                   time.Now,
		metricsListenAndServe: listenFn,
	}
}

// testPool returns a pgtest pool with the schema migrated, or t.Skip()s when
// no Postgres is reachable. Mirrors cmd/schedd/main_test.go::migratedPool so
// the runWithDeps tests can pass a non-nil pool to openDB without reaching
// for a real cluster from inside the harness.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

// nopParker and nopStripe keep runWithDeps's optional collaborators happy
// without dialing anything.
type nopParker struct{}

func (nopParker) ParkInstance(context.Context, string, string) error { return nil }

type nopStripe struct{}

func (nopStripe) PushUsageRecord(context.Context, state.Account, time.Time, float64) error {
	return nil
}

// TestRun_MetricsAddrEmptySkipsListener — when cfg.MetricsAddr is empty,
// runWithDeps must not invoke the metricsListenAndServe factory at all. This
// pins the production default (deploy/etc/meterd.toml.example leaves
// metrics_addr commented) and ensures the wire-up guard doesn't accidentally
// bind a socket.
func TestRun_MetricsAddrEmptySkipsListener(t *testing.T) {
	dir := shortDir(t)
	cfgPath := writeMeterdConfig(t, dir, "")
	pool := testPool(t)

	var invocations int
	listenFn := func(string, http.Handler) (*http.Server, error) {
		invocations++
		return nil, nil
	}
	deps := stubMeterdDeps(cfgPath, "", pool, listenFn)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithDeps(ctx, discardLog(), deps) }()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run returned %v, want nil on clean drain", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run did not return within 3s of cancel")
	}
	if invocations != 0 {
		t.Errorf("metricsListenAndServe invoked %d times, want 0 (empty MetricsAddr)", invocations)
	}
}

// TestRun_MetricsAddrServesEndpoints — when MetricsAddr is set, the wire-up
// builds an http.Handler exposing /metrics and /healthz. The test factory
// captures the handler without binding a socket; we drive `h` directly via
// httptest.NewRecorder.
//
// The factory returns a real *http.Server whose Handler is the captured mux
// but whose Serve is never called — Shutdown on a never-Serve'd server is a
// no-op. The follow-up PR wiring ops.Observe into the four timer ticks will
// surface `meterd_*` series under /metrics. Today an empty CounterVec emits
// nothing (expected Prometheus behavior); the `promhttp_metric_handler_errors_total`
// line is the load-bearing proof that the registry is mounted.
func TestRun_MetricsAddrServesEndpoints(t *testing.T) {
	dir := shortDir(t)
	cfgPath := writeMeterdConfig(t, dir, "127.0.0.1:0")
	pool := testPool(t)

	var (
		mu       sync.Mutex
		captured http.Handler
	)
	listenFn := func(_ string, h http.Handler) (*http.Server, error) {
		mu.Lock()
		defer mu.Unlock()
		captured = h
		return &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}, nil
	}
	deps := stubMeterdDeps(cfgPath, "127.0.0.1:0", pool, listenFn)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithDeps(ctx, discardLog(), deps) }()

	// Wait for the goroutine to register the handler.
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		got := captured
		mu.Unlock()
		if got != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("metrics handler was not registered within 2s")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// /healthz — unconditional 200.
	rec := httptest.NewRecorder()
	captured.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/healthz status = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); body != "ok" {
		t.Errorf("/healthz body = %q, want \"ok\"", body)
	}

	// /metrics — returns the meterd_ prefix per ADR-015. The ops counter
	// starts at 0 with no series exposed; Counter vecs with no observed
	// labels are not emitted (expected Prometheus behavior). The
	// promhttp_metric_handler_errors_total line is the load-bearing proof
	// that the handler is mounted and is on the per-daemon registry.
	rec = httptest.NewRecorder()
	captured.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/metrics status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "promhttp_metric_handler_errors_total") {
		t.Errorf("/metrics body missing promhttp internals (handler may be unconfigured):\n%s", body)
	}

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

// TestRun_MetricsAddrDrainsOnCancel — with the metrics listener wired, cancel
// must result in a clean nil return within the 5s shutdown deadline. Mirrors
// the schedd drains-on-cancel pattern but adds the metrics shutdown path.
func TestRun_MetricsAddrDrainsOnCancel(t *testing.T) {
	dir := shortDir(t)
	cfgPath := writeMeterdConfig(t, dir, "127.0.0.1:0")
	pool := testPool(t)

	listenFn := func(_ string, _ http.Handler) (*http.Server, error) {
		return &http.Server{Handler: http.NewServeMux(), ReadHeaderTimeout: 10 * time.Second}, nil
	}
	deps := stubMeterdDeps(cfgPath, "127.0.0.1:0", pool, listenFn)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithDeps(ctx, discardLog(), deps) }()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run returned %v, want nil on clean drain", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("run did not return within 6s (5s shutdown + slack) of cancel")
	}
}
