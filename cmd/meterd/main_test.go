package main

import (
	"context"
	"encoding/json"
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
//
// env is the env-var reader (FAAS_*_INTERVAL knobs); defaults to a function
// that returns "". Tests that want sub-second intervals pass a closure.
func stubMeterdDeps(cfgPath, metricsAddr string, pool *pgxpool.Pool, listenFn func(string, http.Handler) (*http.Server, error), env func(string) string) runDeps {
	return runDeps{
		configPath: cfgPath,
		openDB: func(context.Context, string) (*pgxpool.Pool, error) {
			return pool, nil
		},
		migrate:               func(context.Context, *pgxpool.Pool) error { return nil },
		loadMeter:             func(c *Config) (*meter.Config, error) { return c.Meter, nil },
		getenv:                env,
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
	deps := stubMeterdDeps(cfgPath, "", pool, listenFn, func(string) string { return "" })

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
// no-op. After this PR the four timer ticks each Observe once, so the
// /metrics body carries meterd_ops_total + meterd_op_duration_seconds series
// in addition to the promhttp internals.
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
	// Shrink every timer to 20 ms so the four loops each fire at least
	// once during the handler wait — without this the only ticks that
	// land are stripe (60 min default), which never fires in a unit
	// test.
	env := func(k string) string {
		switch k {
		case "FAAS_SAMPLE_INTERVAL", "FAAS_QUOTA_INTERVAL",
			"FAAS_STRIPE_INTERVAL", "FAAS_DUNNING_INTERVAL":
			return "20ms"
		}
		return ""
	}
	deps := stubMeterdDeps(cfgPath, "127.0.0.1:0", pool, listenFn, env)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithDeps(ctx, discardLog(), deps) }()

	// Wait for the goroutine to register the handler AND for the four
	// timers to land at least one tick each.
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		got := captured
		mu.Unlock()
		if got != nil && time.Now().After(deadline.Add(-1500*time.Millisecond)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("metrics handler was not registered within 2s")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// /healthz — with the sub-second intervals the four loops have
	// already ticked, so the JSON body reports Healthy=true and a
	// status of 200.
	rec := httptest.NewRecorder()
	captured.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/healthz status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("/healthz Content-Type = %q, want application/json", ct)
	}
	var status struct {
		Healthy bool              `json:"healthy"`
		Stale   []string          `json:"stale"`
		Ticks   map[string]string `json:"ticks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("/healthz body is not valid JSON: %v (body=%q)", err, rec.Body.String())
	}
	if !status.Healthy {
		t.Errorf("/healthz Healthy = false on freshly-ticked meterd (Stale=%v, Ticks=%v)",
			status.Stale, status.Ticks)
	}
	for name, ts := range status.Ticks {
		if ts == "never" {
			t.Errorf("/healthz Ticks[%q] = \"never\" after the four timers fired", name)
		}
	}

	// /metrics — returns the meterd_ prefix per ADR-015. After the
	// four Observe calls at boot the body must include at least one
	// meterd_ops_total line; the promhttp internals are the
	// load-bearing proof that the handler is mounted.
	rec = httptest.NewRecorder()
	captured.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/metrics status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "promhttp_metric_handler_errors_total") {
		t.Errorf("/metrics body missing promhttp internals (handler may be unconfigured):\n%s", body)
	}
	if !strings.Contains(body, "meterd_ops_total") {
		t.Errorf("/metrics body missing meterd_ops_total (Observe not wired?):\n%s", body)
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
	deps := stubMeterdDeps(cfgPath, "127.0.0.1:0", pool, listenFn, func(string) string { return "" })

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

// TestRun_Healthz_StaleReturns503 — drives the loop with sub-second
// intervals so all four timers fire, cancels, waits past the
// 3 × interval threshold, then asserts /healthz returns 503 with a
// JSON body listing every timer in Stale. Pins the §14 M7 wording:
// "meterd healthy iff sampled within 3 minutes" ⇒ a loop that's gone
// silent past 3× its interval must report stale.
func TestRun_Healthz_StaleReturns503(t *testing.T) {
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
	// 20 ms intervals ⇒ 60 ms threshold. The test cancels after the
	// four timers each tick at least once, then sleeps 200 ms (>3 ×
	// threshold) before probing /healthz.
	env := func(k string) string {
		switch k {
		case "FAAS_SAMPLE_INTERVAL", "FAAS_QUOTA_INTERVAL",
			"FAAS_STRIPE_INTERVAL", "FAAS_DUNNING_INTERVAL":
			return "20ms"
		}
		return ""
	}
	deps := stubMeterdDeps(cfgPath, "127.0.0.1:0", pool, listenFn, env)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithDeps(ctx, discardLog(), deps) }()

	// Wait for the handler to register AND the four timers to tick.
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		got := captured
		mu.Unlock()
		if got != nil && time.Now().After(deadline.Add(-1500*time.Millisecond)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("metrics handler was not registered within 2s")
		}
		time.Sleep(10 * time.Millisecond)
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

	// Sleep past the 60 ms (3 × 20 ms) threshold so the handlers
	// report every timer as stale.
	time.Sleep(200 * time.Millisecond)

	rec := httptest.NewRecorder()
	captured.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/healthz status = %d, want 503 (body=%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("/healthz Content-Type = %q, want application/json", ct)
	}
	var status struct {
		Healthy bool              `json:"healthy"`
		Stale   []string          `json:"stale"`
		Ticks   map[string]string `json:"ticks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("/healthz body is not valid JSON: %v (body=%q)", err, rec.Body.String())
	}
	if status.Healthy {
		t.Errorf("/healthz Healthy = true after cancel + 200ms; want false (Ticks=%v)",
			status.Ticks)
	}
	// Every wired timer must be reported as stale; the env override
	// wired all four (sample / quota / stripe / dunning).
	for _, name := range []string{"sample", "quota", "stripe", "dunning"} {
		found := false
		for _, n := range status.Stale {
			if n == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("/healthz Stale missing %q (have %v)", name, status.Stale)
		}
	}
}
