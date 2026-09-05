package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
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
	"google.golang.org/protobuf/types/known/timestamppb"

	egresspb "github.com/onebox-faas/faas/api/proto/onebox/faas/egress/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	billingloader "github.com/onebox-faas/faas/pkg/billing/loader"
	"github.com/onebox-faas/faas/pkg/billing/paddle"
	"github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
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
func stubMeterdDeps(cfgPath, metricsAddr string, pool *pgxpool.Pool, listenFn func(string, http.Handler, time.Duration, time.Duration, time.Duration, int64) (*http.Server, error), env func(string) string) runDeps {
	return runDeps{
		configPath: cfgPath,
		openDB: func(context.Context, string) (*pgxpool.Pool, error) {
			return pool, nil
		},
		migrate:               func(context.Context, *pgxpool.Pool) error { return nil },
		loadMeter:             func(c *Config) (*meter.Config, error) { return c.Meter, nil },
		getenv:                env,
		dialSchedd:            func(context.Context, string, *tls.Config) (parkInstanceParker, error) { return &nopParker{}, nil },
		loadBillingProvider:   nil, // skipped when pusher is pre-populated
		parker:                &nopParker{},
		pusher:                &nopProvider{},
		mailer:                nil,
		now:                   time.Now,
		metricsListenAndServe: listenFn,
	}
}

// subSecondIntervalsEnv returns an env reader that pins every
// FAAS_*_INTERVAL knob to 20 ms. Used by tests that need the meterd
// timers (sample / quota / stripe / dunning / upstream_probe /
// upstream_part) to each fire at least once during a brief run;
// without this, the production defaults (60 s / 3 min / 60 min /
// 60 min / 30 s / 1 h) leave stripe + dunning + upstream_part
// dormant for the life of any unit test.
//
// The upstream_part knob is load-bearing: the C8 wiring always
// spawns the partition cron via WithPartitionCreate, so /healthz
// will report upstream_part="never" under the production default
// (1 h) and trip the FreshTick assertion downstream. Pin both
// upstream knobs the same way the historical four were pinned.
func subSecondIntervalsEnv() func(string) string {
	return func(k string) string {
		switch k {
		case "FAAS_SAMPLE_INTERVAL", "FAAS_QUOTA_INTERVAL",
			"FAAS_STRIPE_INTERVAL", "FAAS_DUNNING_INTERVAL",
			"FAAS_UPSTREAM_PROBE_INTERVAL", "FAAS_UPSTREAM_PROBE_PARTITION_INTERVAL":
			return "20ms"
		// PR #1191 C2: pkg/mail/factory refuses to boot on a non-dev
		// box with unset FAAS_MAIL_TRANSPORT. The unit tests are
		// dev/CI box for this purpose; the FAAS_DEV=1 escape hatch
		// keeps the contract visible in every boot path rather than
		// silently picking LogSender under the default branch.
		case "FAAS_DEV":
			return "1"
		}
		return ""
	}
}

// testMailDevEnv returns the minimal env reader that satisfies the
// mail fail-closed boot check — a no-op for everything except
// FAAS_DEV=1. Used by tests that don't need sub-second intervals
// (TestRun_MetricsAddrEmptySkipsListener, TestRun_Healthz_StaleReturns503)
// but still drive runWithDeps through the real wire-up path.
func testMailDevEnv() func(string) string {
	return func(k string) string {
		if k == "FAAS_DEV" {
			return "1"
		}
		return ""
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

// nopParker and nopProvider keep runWithDeps's optional collaborators happy
// without dialing anything. nopProvider satisfies the full billing.Provider
// surface (PR #3 / ADR-025) — the meterd loop only exercises PushUsageRecord
// but the conformance assertion guards against accidental partial
// implementations leaking into other test packages.
type nopParker struct{}

func (nopParker) ParkInstance(context.Context, string, string, string) error { return nil }
func (nopParker) ListInstanceStats(context.Context) ([]scheddgrpc.InstanceStatsRow, error) {
	// Issue #279 / PR-B: the test harness returns an empty
	// snapshot so the meterd sampler writes 0 CPU-µs per minute
	// without retrying the schedd gRPC.
	return nil, nil
}

type nopProvider struct{}

func (nopProvider) EnsurePlanProducts(context.Context) error { return nil }
func (nopProvider) CreateCustomer(context.Context, state.Account) (string, error) {
	return "", nil
}
func (nopProvider) PushUsageRecord(context.Context, state.Account, time.Time, int64) error {
	return nil
}
func (nopProvider) VerifyWebhook([]byte, map[string]string, time.Duration) (billing.Event, error) {
	return billing.Event{}, nil
}
func (nopProvider) CreateUpgradeTransaction(context.Context, state.Account, api.Plan) (string, string, error) {
	return "", "", nil
}

// Refund is the issue #279 billing.Provider seam. meterd never calls
// it; returning ErrNotImplemented matches the Paddle contract.
func (nopProvider) Refund(context.Context, string, int64) (*billing.RefundResult, error) {
	return nil, billing.ErrNotImplemented
}

// ReconcileUsage is the ADR-049 §B.1 drift-detector seam. meterd
// calls it via the reconciler (cmd/meterd/main.go). Returning
// ErrNotImplemented matches the Paddle contract — the reconciler
// treats that as "no provider drift signal yet".
func (nopProvider) ReconcileUsage(context.Context, state.Account, time.Time, time.Time) (int64, error) {
	return 0, billing.ErrNotImplemented
}
func (nopProvider) RetryLatestCharge(context.Context, state.Account) (string, string, error) {
	return "", "", nil
}
func (nopProvider) CancelAtPeriodEnd(context.Context, state.Account) (time.Time, error) {
	return time.Time{}, nil
}
func (nopProvider) PaymentMethodSummary(context.Context, state.Account) (billing.PaymentMethod, error) {
	return billing.PaymentMethod{}, nil
}
func (nopProvider) Capabilities() billing.CapabilitySet { return 0 }

// TestRun_MetricsAddrEmptySkipsListener — when cfg.MetricsAddr is empty,
// runWithDeps must not invoke the metricsListenAndServe factory at all. This
// pins the production default (deploy/etc/meterd.toml.example leaves
// metrics_addr commented — RETIRED in PR-1 Phase 2 after PR-X; the v2 path
// is deploy/ansible/roles/control_plane_service/files/meterd.toml.example)
// and ensures the wire-up guard doesn't accidentally bind a socket.
func TestRun_MetricsAddrEmptySkipsListener(t *testing.T) {
	dir := shortDir(t)
	cfgPath := writeMeterdConfig(t, dir, "")
	pool := testPool(t)

	var invocations int
	listenFn := func(string, http.Handler, time.Duration, time.Duration, time.Duration, int64) (*http.Server, error) {
		invocations++
		return nil, nil
	}
	deps := stubMeterdDeps(cfgPath, "", pool, listenFn, testMailDevEnv())

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
	listenFn := func(_ string, h http.Handler, _ time.Duration, _ time.Duration, _ time.Duration, _ int64) (*http.Server, error) {
		mu.Lock()
		defer mu.Unlock()
		captured = h
		return &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}, nil
	}
	// Shrink every timer to 20 ms so the four loops each fire at least
	// once during the handler wait — without this the only ticks that
	// land are stripe (60 min default), which never fires in a unit
	// test.
	deps := stubMeterdDeps(cfgPath, "127.0.0.1:0", pool, listenFn, subSecondIntervalsEnv())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithDeps(ctx, discardLog(), deps) }()

	// Wait for the goroutine to register the handler AND for every
	// tracked timer (sample, quota, stripe, upstream_probe,
	// upstream_part) to land at least one tick each. The healthz
	// response below will flip Healthy=false on any "never" Ticks entry,
	// so we need each timer to have fired before we read the body.
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		got := captured
		mu.Unlock()
		// Give the timer goroutines a generous tail window so the
		// freshly-spawned cron (upstream_part) at least one tick lands
		// before we read /healthz. 1.5s comfortably covers the
		// sub-second intervals the test factory sets.
		ready := got != nil && time.Now().After(deadline.Add(-1500*time.Millisecond))
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("metrics handler was not registered within 2s")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// /healthz — every wired loop has ticked at least once
	// (sub-second intervals), so the JSON body reports Healthy=true
	// and a status of 200. If a new cron is added without bumping
	// the wait window, this test will flake via the "never" check
	// below — the assertion is the canonical regression tripwire.
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
			t.Errorf("/healthz Ticks[%q] = \"never\" after the timers fired", name)
		}
	}

	// /metrics — returns the meterd_ prefix per ADR-015. After the
	// four Observe calls at boot the body must include at least one
	// meterd_ops_total line; the promhttp internals are the
	// load-bearing proof that the handler is mounted. The dedicated
	// Stripe-push histogram (meterd_stripe_push_duration_seconds) is
	// registered on the same wire.OpsMetrics instance and surfaces as
	// an INFO/help line even before the first push.
	rec = httptest.NewRecorder()
	captured.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/metrics status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	// The handler is mounted iff the Gatherers we wired
	// (ops + recRegistry + DefaultGatherer) all respond on the
	// scrape. promhttp_metric_handler_errors_total is created per
	// HandlerFor instance on its bound registry, so it's NOT
	// guaranteed to surface via the merged Gatherers — but the
	// DefaultGatherer line for Go runtime (`go_goroutines`) IS
	// always present once promhttp's runtime collector is loaded,
	// which it is on any prometheus.Handler() call anywhere in
	// the daemon's process. The line is the load-bearing proof the
	// handler is mounted and DefaultGatherer is reachable.
	if !strings.Contains(body, "go_goroutines") {
		t.Errorf("/metrics body missing Go runtime metrics (handler may be unconfigured or DefaultGatherer dropped):\n%s", body)
	}
	if !strings.Contains(body, "meterd_ops_total") {
		t.Errorf("/metrics body missing meterd_ops_total (Observe not wired?):\n%s", body)
	}
	if !strings.Contains(body, "meterd_stripe_push_duration_seconds") {
		t.Errorf("/metrics body missing meterd_stripe_push_duration_seconds histogram (wire seam not registered?):\n%s", body)
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

	listenFn := func(_ string, _ http.Handler, _ time.Duration, _ time.Duration, _ time.Duration, _ int64) (*http.Server, error) {
		return &http.Server{Handler: http.NewServeMux(), ReadHeaderTimeout: 10 * time.Second}, nil
	}
	deps := stubMeterdDeps(cfgPath, "127.0.0.1:0", pool, listenFn, testMailDevEnv())

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

// TestRun_DialScheddPropagatesCancel: when the dialSchedd seam errors,
// runWithDeps must propagate the error rather than blocking or silently
// falling through to a nil parker. Pins the issue #95 wire-up: a real
// dial failure (refused, timeout) is fatal at boot, not deferred.
//
// The cancellation half of the contract — "ctx passed to dialSchedd
// participates in daemon shutdown" — is exercised at the wire layer by
// pkg/wire.TestDialContextCancelled. Asserting it here too would either
// require cancelling the parent ctx (which would short-circuit openDB
// at the runWithDeps step *before* dialSchedd fires, leaving the seam
// never tested) or wrapping ctx internally at the seam (which would
// silently diverge from the production code path). Neither is worth the
// flakiness; the seam-level error propagation already pins the
// load-bearing behaviour for this slice.
//
// Requires a real Postgres (skipped on dev shells without one); the
// seam under test is mid-`runWithDeps`, after openDB+migrate.
func TestRun_DialScheddPropagatesCancel(t *testing.T) {
	dir := shortDir(t)
	cfgPath := writeMeterdConfig(t, dir, "")
	pool := testPool(t)

	wantErr := errors.New("dial cancelled (test)")
	listenFn := func(string, http.Handler, time.Duration, time.Duration, time.Duration, int64) (*http.Server, error) {
		return nil, nil
	}
	deps := stubMeterdDeps(cfgPath, "", pool, listenFn, testMailDevEnv())
	// parker is left nil so the dialSchedd seam is the only path that
	// runs. The stub returns the canonical error so we can assert the
	// wrap-and-return.
	deps.parker = nil
	deps.dialSchedd = func(context.Context, string, *tls.Config) (parkInstanceParker, error) {
		return nil, wantErr
	}

	if err := runWithDeps(context.Background(), discardLog(), deps); err == nil {
		t.Fatal("expected error from dialSchedd seam; got nil")
	} else if !errors.Is(err, wantErr) {
		t.Errorf("err = %v; want wraps %v", err, wantErr)
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
	listenFn := func(_ string, h http.Handler, _ time.Duration, _ time.Duration, _ time.Duration, _ int64) (*http.Server, error) {
		mu.Lock()
		defer mu.Unlock()
		captured = h
		return &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}, nil
	}
	// 20 ms intervals ⇒ 60 ms threshold. The test cancels after the
	// four timers each tick at least once, then sleeps 200 ms (>3 ×
	// threshold) before probing /healthz.
	deps := stubMeterdDeps(cfgPath, "127.0.0.1:0", pool, listenFn, subSecondIntervalsEnv())

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

// meterRec is the cmd/meterd-side test fake for the Stripe pusher.
// Renamed from `recordingStripe` because it deliberately differs from
// the pkg/meter-side recordingStripe in pusher_shadow_test.go:
//
//   - pkg/meter's fake records full (acct, hour, mb_seconds) tuples
//     because TestPushHour_Shadow24h asserts the integer-mb-seconds
//     math against a synthetic dataset.
//   - cmd/meterd's fake only counts calls because
//     TestRun_MetricsAddr_StripePushLabels asserts the /metrics scrape
//     shape, not the push math.
//
// Lifting either into a shared pkg/metertest would over-fit the
// other (or grow into a kitchen-sink fake). Keeping them as adjacent
// single-purpose helpers preserves locality: each test reads its fake
// next to its assertions, and changes to one fake don't drag the
// other along.
type meterRec struct {
	mu    sync.Mutex
	calls int
}

// EnsurePlanProducts / CreateCustomer / VerifyWebhook / CreateUpgradeTransaction
// are no-op stubs — the meterd pusher loop only calls PushUsageRecord.
// Returning the empty-string "no provider" semantics matches the production
// shapes so the test exercises the same dispatch surface as prod.
func (r *meterRec) EnsurePlanProducts(context.Context) error { return nil }
func (r *meterRec) CreateCustomer(context.Context, state.Account) (string, error) {
	return "", nil
}
func (r *meterRec) VerifyWebhook([]byte, map[string]string, time.Duration) (billing.Event, error) {
	return billing.Event{}, nil
}
func (r *meterRec) CreateUpgradeTransaction(context.Context, state.Account, api.Plan) (string, string, error) {
	return "", "", nil
}

// Refund is the issue #279 billing.Provider seam. meterd never calls
// it; returning ErrNotImplemented matches the Paddle contract.
func (r *meterRec) Refund(context.Context, string, int64) (*billing.RefundResult, error) {
	return nil, billing.ErrNotImplemented
}

// ReconcileUsage is the ADR-049 §B.1 drift-detector seam. Stub:
// the meterd main tests don't drive the reconciler, so we return
// (0, nil) — no drift signal.
func (r *meterRec) ReconcileUsage(context.Context, state.Account, time.Time, time.Time) (int64, error) {
	return 0, nil
}

// RetryLatestCharge / CancelAtPeriodEnd / PaymentMethodSummary
// (issue #242): meterd never drives these apid-only surfaces. Zero-
// value stubs satisfy the interface.
func (r *meterRec) RetryLatestCharge(context.Context, state.Account) (string, string, error) {
	return "", "", nil
}
func (r *meterRec) CancelAtPeriodEnd(context.Context, state.Account) (time.Time, error) {
	return time.Time{}, nil
}
func (r *meterRec) PaymentMethodSummary(context.Context, state.Account) (billing.PaymentMethod, error) {
	return billing.PaymentMethod{}, nil
}

func (r *meterRec) PushUsageRecord(context.Context, state.Account, time.Time, int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return nil
}

// Capabilities returns the Stripe-shaped set the meter pusher loop
// observes in production for the surfaces this stub actually
// implements. The metered usage + sandbox surfaces are exercised
// by the shadow test; Refund is intentionally omitted from the
// capability bitmask because the stub's Refund() returns
// ErrNotImplemented (the real *stripe.Client implements Refund —
// the stub is intentionally minimal).
func (r *meterRec) Capabilities() billing.CapabilitySet {
	return billing.CapabilitySet(billing.CapUsageMetered | billing.CapSandbox)
}

func (r *meterRec) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// TestGatewayEgressAdapter_EgressBytes — exercise the
// recordFrame → EgressBytes round-trip on the meterd-side
// push-consumer (ADR-046 PR-2). PR-1 left this as a stub
// returning ok=false; PR-2 replaces it with a real minute-bucketed
// accumulator. Contract pinned by this test:
//
//  1. EgressBytes looks up by current-minute bucket; the
//     adapter's `now` injection is the "what is current"
//     source of truth.
//  2. recordFrame without an instance id, with nil Minute, or
//     with empty InstanceId is a silent no-op (mirrors the
//     producer's upstream guards).
//  3. After the clock advances past the frame's minute, the
//     historical bucket is no longer current → EgressBytes
//     returns ok=false for that instance until a new frame in
//     the new minute arrives.
//  4. netTxBytes is always 0 on this path (the schedd adapter
//     owns that column; the aggregator combines).
//  5. Looking up an instance the adapter never saw returns
//     ok=false with zero bytes.
func TestGatewayEgressAdapter_EgressBytes(t *testing.T) {
	t.Parallel()

	// Anchor is mid-minute so the truncation boundary is
	// observable: anchor.Truncate(Minute) = 12:00, anchor+30s is
	// still in the 12:00 bucket.
	anchor := time.Date(2026, 7, 29, 12, 0, 30, 0, time.UTC)
	a := &gatewayEgressAdapter{
		now:  func() time.Time { return anchor },
		data: make(map[string]map[int64]gatewayUsageBucket),
	}

	// 1. recordFrame(nil) is a no-op.
	a.recordFrame(nil)
	if _, _, ok := a.EgressBytes("inst-1"); ok {
		t.Fatalf("EgressBytes(inst-1) ok=true after a nil recordFrame; want false")
	}

	// 2. recordFrame with empty InstanceId is a no-op.
	a.recordFrame(&egresspb.BytesFrame{InstanceId: "", Minute: timestamppb.New(anchor), Bytes: 4096})
	if _, _, ok := a.EgressBytes(""); ok {
		t.Fatalf("EgressBytes(\"\") ok=true after empty-id record; want false (receiver also gates on empty)")
	}

	// 3. recordFrame with nil Minute is a no-op (the bucket
	//    key is derived from frame.Minute.AsTime()).
	a.recordFrame(&egresspb.BytesFrame{InstanceId: "inst-1", Minute: nil, Bytes: 4096})
	if _, _, ok := a.EgressBytes("inst-1"); ok {
		t.Fatalf("EgressBytes(inst-1) ok=true after nil-Minute record; want false (no bucket was keyed)")
	}

	// 4. Happy path: a frame stamped at the anchor (truncated
	//    to 12:00) is readable while the clock stays in 12:00.
	a.recordFrame(&egresspb.BytesFrame{
		InstanceId: "inst-1",
		Minute:     timestamppb.New(anchor),
		Bytes:      4096,
	})

	got, netTx, ok := a.EgressBytes("inst-1")
	if !ok {
		t.Fatalf("EgressBytes(inst-1) ok=false after a positive recordFrame; want true")
	}
	if got != 4096 {
		t.Errorf("EgressBytes(inst-1) = %d, want 4096", got)
	}
	if netTx != 0 {
		t.Errorf("EgressBytes(inst-1) netTx = %d, want 0 (adapter never populates netTx; schedd adapter owns that column)", netTx)
	}

	// 5. Cross-minute: a frame stamped one minute past the anchor
	//    goes into its own bucket. While the clock is still in
	//    the anchor minute, the anchor bucket is the one read.
	nextMinute := anchor.Add(time.Minute)
	a.recordFrame(&egresspb.BytesFrame{
		InstanceId: "inst-1",
		Minute:     timestamppb.New(nextMinute),
		Bytes:      8192,
	})

	got, _, ok = a.EgressBytes("inst-1")
	if !ok {
		t.Fatalf("EgressBytes(inst-1) ok=false after cross-minute frame; want true")
	}
	if got != 4096 {
		t.Errorf("EgressBytes(inst-1) at anchor minute = %d, want 4096 (cross-minute frame must not overwrite anchor bucket)", got)
	}

	// 6. After the clock advances past the anchor minute, the
	//    anchor bucket is no longer "current" — EgressBytes
	//    returns ok=false even though the row is still in the
	//    map. This is the producer/consumer re-anchor contract:
	//    the meterd sampler reads the live minute only.
	a.now = func() time.Time { return nextMinute.Add(30 * time.Second) }
	if _, _, ok := a.EgressBytes("inst-1"); ok {
		t.Errorf("EgressBytes(inst-1) ok=true after clock advance into nextMinute; want false (anchor bucket is stale)")
	}

	// 7. A fresh frame in the new minute restores ok=true with
	//    that minute's bytes.
	a.recordFrame(&egresspb.BytesFrame{
		InstanceId: "inst-1",
		Minute:     timestamppb.New(a.now()),
		Bytes:      16384,
	})
	got, _, ok = a.EgressBytes("inst-1")
	if !ok {
		t.Fatalf("EgressBytes(inst-1) ok=false after fresh frame in current minute; want true")
	}
	if got != 16384 {
		t.Errorf("EgressBytes(inst-1) in nextMinute = %d, want 16384", got)
	}

	// 8. Ghost instance: never recorded → ok=false.
	if _, _, ok := a.EgressBytes("inst-ghost"); ok {
		t.Errorf("EgressBytes(inst-ghost) ok=true; want false (no frame ever recorded)")
	}

	// 9. nil receiver is safe (defends the test-injected seams).
	var nilA *gatewayEgressAdapter
	if _, _, ok := nilA.EgressBytes("inst-1"); ok {
		t.Errorf("EgressBytes on nil receiver ok=true; want false")
	}
	if got := nilA.Tracked(); got != 0 {
		t.Errorf("Tracked on nil receiver = %d, want 0", got)
	}
}

func TestGatewayEgressAdapter_ReadUsageDeltasAccumulatesAndDrains(t *testing.T) {
	anchor := time.Date(2026, 7, 29, 12, 0, 30, 0, time.UTC)
	a := &gatewayEgressAdapter{
		now:  func() time.Time { return anchor },
		data: make(map[string]map[int64]gatewayUsageBucket),
	}
	for _, frame := range []*egresspb.BytesFrame{
		{InstanceId: "inst-1", Minute: timestamppb.New(anchor.Add(-time.Minute)), Bytes: 100, Requests: 1},
		{InstanceId: "inst-1", Minute: timestamppb.New(anchor), Bytes: 200, Requests: 2, ColdBoots: 1},
		{InstanceId: "inst-1", Minute: timestamppb.New(anchor), Bytes: 300, Requests: 3},
		{InstanceId: "inst-1", Minute: timestamppb.New(anchor.Add(time.Minute)), Bytes: 999, Requests: 9},
	} {
		a.recordFrame(frame)
	}
	got, ok := a.ReadUsageDeltas("inst-1")
	if !ok || got.TXBytes != 600 || got.Requests != 6 || got.ColdBootCount != 1 {
		t.Fatalf("usage deltas = %+v, %v", got, ok)
	}
	if _, ok := a.ReadUsageDeltas("inst-1"); ok {
		t.Fatal("second read unexpectedly redelivered drained current buckets")
	}
	a.now = func() time.Time { return anchor.Add(time.Minute) }
	got, ok = a.ReadUsageDeltas("inst-1")
	if !ok || got.TXBytes != 999 || got.Requests != 9 {
		t.Fatalf("future usage deltas = %+v, %v", got, ok)
	}
}

func TestScheddEgressAdapterComputesCumulativeCounterDeltas(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 30, 0, time.UTC)
	cpu := &scheddCPUAdapter{
		now:     func() time.Time { return now },
		fetched: now,
		rows: map[string]scheddgrpc.InstanceStatsRow{
			"inst-1": {InstanceID: "inst-1", NetTxBytes: 1000, NetRxBytes: 400},
		},
	}
	a := &scheddEgressAdapter{cpu: cpu}
	if first, ok := a.ReadUsageDeltas("inst-1"); !ok || first.NetTXBytes != 0 || first.NetRXBytes != 0 {
		t.Fatalf("first baseline = %+v, %v", first, ok)
	}
	cpu.rows["inst-1"] = scheddgrpc.InstanceStatsRow{InstanceID: "inst-1", NetTxBytes: 1600, NetRxBytes: 700}
	got, ok := a.ReadUsageDeltas("inst-1")
	if !ok || got.NetTXBytes != 600 || got.NetRXBytes != 300 {
		t.Fatalf("network delta = %+v, %v", got, ok)
	}
	cpu.rows["inst-1"] = scheddgrpc.InstanceStatsRow{InstanceID: "inst-1", NetTxBytes: 10, NetRxBytes: 5}
	got, ok = a.ReadUsageDeltas("inst-1")
	if !ok || got.NetTXBytes != 0 || got.NetRXBytes != 0 {
		t.Fatalf("regression delta = %+v, %v", got, ok)
	}
}

// TestGatewayEgressAdapter_Concurrent — race-safe concurrent
// recordFrame + EgressBytes. 8 producers × 200 frames × 1 byte
// = 1600 expected. Gateway frames are drain deltas, so the
// adapter must sum every frame in a per-(instance, minute) bucket.
// The adapter must hold the
// (instance, minute) invariant under -race: every concurrent
// recordFrame lands in exactly one bucket (no torn writes), and
// EgressBytes reads the bucket monotonically.
//
// All frames are stamped against the same minute (anchor) so
// the last frame wins the (inst-1, 12:00) bucket.
func TestGatewayEgressAdapter_Concurrent(t *testing.T) {
	t.Parallel()

	anchor := time.Date(2026, 7, 29, 12, 0, 30, 0, time.UTC)
	a := &gatewayEgressAdapter{
		now:  func() time.Time { return anchor },
		data: make(map[string]map[int64]gatewayUsageBucket),
	}

	const (
		producers   = 8
		framesEach  = 200
		bytesPerFrm = uint64(1)
	)

	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := 0; f < framesEach; f++ {
				a.recordFrame(&egresspb.BytesFrame{
					InstanceId: "inst-1",
					Minute:     timestamppb.New(anchor),
					Bytes:      bytesPerFrm,
				})
			}
		}()
	}
	wg.Wait()

	// Per-bucket additive semantics: every drained frame contributes.
	got, _, ok := a.EgressBytes("inst-1")
	if !ok {
		t.Fatalf("EgressBytes(inst-1) ok=false after concurrent writes; want true")
	}
	want := uint64(producers * framesEach * bytesPerFrm)
	if got != want {
		t.Errorf("EgressBytes(inst-1) = %d, want %d (sum of drained frames)", got, want)
	}
	if tracked := a.Tracked(); tracked != 1 {
		t.Errorf("Tracked() = %d, want 1 (single instance, single minute)", tracked)
	}
}

// TestGatewayEgressAdapter_Tracked — exercise the
// per-instance cardinality seam. Distinct instance ids each
// open their own row in the data map; cross-recordFrames for the
// same instance don't bump Tracked.
func TestGatewayEgressAdapter_Tracked(t *testing.T) {
	t.Parallel()

	anchor := time.Date(2026, 7, 29, 12, 0, 30, 0, time.UTC)
	a := &gatewayEgressAdapter{
		now:  func() time.Time { return anchor },
		data: make(map[string]map[int64]gatewayUsageBucket),
	}

	if got := a.Tracked(); got != 0 {
		t.Fatalf("fresh adapter Tracked() = %d, want 0", got)
	}

	// Two distinct instances, multiple frames each.
	for i := 0; i < 5; i++ {
		a.recordFrame(&egresspb.BytesFrame{
			InstanceId: "inst-A",
			Minute:     timestamppb.New(anchor),
			Bytes:      uint64(100 + i),
		})
	}
	for i := 0; i < 3; i++ {
		a.recordFrame(&egresspb.BytesFrame{
			InstanceId: "inst-B",
			Minute:     timestamppb.New(anchor),
			Bytes:      uint64(200 + i),
		})
	}

	if got := a.Tracked(); got != 2 {
		t.Errorf("Tracked() = %d, want 2 (inst-A + inst-B)", got)
	}
}

// TestRun_MetricsAddr_StripePushLabels — the §14 M7 dashboard
// acceptance for the new wire.OpsMetrics seam. Drives the meterd
// stripe-tick at sub-second cadence against an injected meterRec,
// then asserts the /metrics body carries the per-push counter +
// histogram with the canonical code label `result="ok"`. With
// nopStripe (the default) the histogram's observation never lands;
// this test wires the recording stub via runDeps.stripe to exercise
// the production code path.
func TestRun_MetricsAddr_StripePushLabels(t *testing.T) {
	dir := shortDir(t)
	cfgPath := writeMeterdConfig(t, dir, "127.0.0.1:0")
	pool := testPool(t)

	var (
		mu       sync.Mutex
		captured http.Handler
	)
	listenFn := func(_ string, h http.Handler, _ time.Duration, _ time.Duration, _ time.Duration, _ int64) (*http.Server, error) {
		mu.Lock()
		defer mu.Unlock()
		captured = h
		return &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}, nil
	}
	rec := &meterRec{}
	deps := stubMeterdDeps(cfgPath, "127.0.0.1:0", pool, listenFn, subSecondIntervalsEnv())
	deps.pusher = rec // override nopProvider; pre-populated field on runDeps

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithDeps(ctx, discardLog(), deps) }()

	// Wait for the handler AND for the stripe tick to land at least
	// once. The stripe pusher walks ListAllAccounts and skips
	// every account in the empty test store, so `rec.Calls()` may
	// stay at 0 — but the per-push Observe still fires (with
	// code="ok") since the loop body itself runs even when no
	// account is pushed. The dashboard's `meterd_ops_total{op=
	// "stripe",code="ok"}` is the proxy; we assert that line shows
	// up. The dedicated histogram series only registers when an
	// SDK call actually happens, so we don't assert it here.
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

	recBody := httptest.NewRecorder()
	captured.ServeHTTP(recBody, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recBody.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", recBody.Code)
	}
	body := recBody.Body.String()
	if !strings.Contains(body, "meterd_ops_total") {
		t.Errorf("/metrics body missing meterd_ops_total (Observe not wired?):\n%s", body)
	}
	// meterd_ops_total{op="stripe"} must show up after at least one
	// stripe-tick body run. The stripe-tick body calls Observe("stripe",
	// dur, nil) regardless of whether any account was pushed.
	if !strings.Contains(body, `op="stripe"`) {
		t.Errorf("/metrics body missing op=\"stripe\" label (stripe-tick body never ran?):\n%s", body)
	}
	// The dedicated histogram's HELP/TYPE lines are emitted by the
	// registry even before the first observation — that's the
	// invariant the dashboard's panel depends on.
	if !strings.Contains(body, "meterd_stripe_push_duration_seconds") {
		t.Errorf("/metrics body missing meterd_stripe_push_duration_seconds histogram registration:\n%s", body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run returned %v, want nil on clean drain", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("loop did not return within 3s of cancel")
	}
}

// captureWarnLogger returns a slog.Logger writing JSON records into the
// returned buffer at WARN level (so Info records don't pollute the
// capture) plus a pointer to the buffer. Tests parse the buffer line-by-
// line to assert on which messages fired.
func captureWarnLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	return slog.New(h), buf
}

// TestWarnIfEmptyAPIKey_TOMLOnlyNoFalsePositive pins Finding 2 of the
// PR-P3 /code-review medium report. Before the fix, meterd's empty-key
// warn read deps.getenv directly, so a TOML-only Stripe or Paddle deploy
// emitted a misleading "API key is empty" warning on every boot even
// though the SDK was correctly initialized via the TOML fallback. After
// the fix, warnIfEmptyAPIKey reads the merged cfg, so the TOML value
// suppresses the warn.
func TestWarnIfEmptyAPIKey_TOMLOnlyNoFalsePositive(t *testing.T) {
	t.Run("stripe TOML key suppresses warn", func(t *testing.T) {
		log, buf := captureWarnLogger()
		cfg := &billingloader.RootBillingConfig{
			Stripe: &stripe.Config{APIKey: "sk_test_toml_value"},
			Paddle: &paddle.Config{},
		}
		warnIfEmptyAPIKey(log, cfg, "stripe")
		if buf.Len() != 0 {
			t.Errorf("expected no warn for TOML-only Stripe; got: %s", buf.String())
		}
	})
	t.Run("paddle TOML key suppresses warn", func(t *testing.T) {
		log, buf := captureWarnLogger()
		cfg := &billingloader.RootBillingConfig{
			Stripe: &stripe.Config{},
			Paddle: &paddle.Config{APIKey: "pdl_test_toml_value"},
		}
		warnIfEmptyAPIKey(log, cfg, "paddle")
		if buf.Len() != 0 {
			t.Errorf("expected no warn for TOML-only Paddle; got: %s", buf.String())
		}
	})
	t.Run("stripe no key fires warn", func(t *testing.T) {
		log, buf := captureWarnLogger()
		cfg := &billingloader.RootBillingConfig{
			Stripe: &stripe.Config{APIKey: ""},
			Paddle: &paddle.Config{},
		}
		warnIfEmptyAPIKey(log, cfg, "stripe")
		if !strings.Contains(buf.String(), "Stripe API key is empty") {
			t.Errorf("expected Stripe empty-key warn; got: %s", buf.String())
		}
	})
	t.Run("paddle no key fires warn", func(t *testing.T) {
		log, buf := captureWarnLogger()
		cfg := &billingloader.RootBillingConfig{
			Stripe: &stripe.Config{},
			Paddle: &paddle.Config{APIKey: ""},
		}
		warnIfEmptyAPIKey(log, cfg, "paddle")
		if !strings.Contains(buf.String(), "Paddle API key is empty") {
			t.Errorf("expected Paddle empty-key warn; got: %s", buf.String())
		}
	})
	t.Run("nil cfg is silent", func(t *testing.T) {
		log, buf := captureWarnLogger()
		warnIfEmptyAPIKey(log, nil, "stripe")
		if buf.Len() != 0 {
			t.Errorf("expected no warn for nil cfg; got: %s", buf.String())
		}
	})
	t.Run("unknown provider is silent", func(t *testing.T) {
		log, buf := captureWarnLogger()
		cfg := &billingloader.RootBillingConfig{
			Stripe: &stripe.Config{APIKey: ""},
			Paddle: &paddle.Config{APIKey: ""},
		}
		warnIfEmptyAPIKey(log, cfg, "lemonsqueezy")
		if buf.Len() != 0 {
			t.Errorf("expected no warn for unknown provider; got: %s", buf.String())
		}
	})
}

// TestDefaultDeps_MetricsListenAndServe_AppliesCanonicalShape pins the
// ADR-122 canonical metrics-listener shape at the factory level. The
// factory binds a real net.Listener (no test stub), inspects the
// returned *http.Server, then Shutdowns cleanly. The listener timeout
// values must match cfg.MetricsListener — i.e. the constant fallback
// path — so a stray edit to either the helper or the api.* family
// surfaces here.
//
// Loopback bind means no port collision with anything else on the
// test box: the factory picks 127.0.0.1:0 and we never see the port.
func TestDefaultDeps_MetricsListenAndServe_AppliesCanonicalShape(t *testing.T) {
	deps := defaultDeps()
	if deps.metricsListenAndServe == nil {
		t.Fatal("defaultDeps.metricsListenAndServe is nil")
	}
	mux := http.NewServeMux()
	cfg := &Config{} // all zeros → MetricsListener falls back to constants
	readTimeout, writeTimeout, idleTimeout, maxHeaderBytes := cfg.MetricsListener()
	srv, err := deps.metricsListenAndServe("127.0.0.1:0", mux, readTimeout, writeTimeout, idleTimeout, maxHeaderBytes)
	if err != nil {
		t.Fatalf("metricsListenAndServe: %v", err)
	}
	if srv.ReadTimeout != readTimeout {
		t.Errorf("ReadTimeout = %v want %v", srv.ReadTimeout, readTimeout)
	}
	if srv.WriteTimeout != writeTimeout {
		t.Errorf("WriteTimeout = %v want %v", srv.WriteTimeout, writeTimeout)
	}
	if srv.IdleTimeout != idleTimeout {
		t.Errorf("IdleTimeout = %v want %v", srv.IdleTimeout, idleTimeout)
	}
	if int64(srv.MaxHeaderBytes) != maxHeaderBytes {
		t.Errorf("MaxHeaderBytes = %d want %d", srv.MaxHeaderBytes, maxHeaderBytes)
	}
	// drain so the listener goroutine exits cleanly.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	_ = srv.Shutdown(stopCtx)
}

func TestValidateBillingPushInterval(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		interval time.Duration
		wantErr  bool
	}{
		{name: "polar hourly", provider: provPolar, interval: time.Hour},
		{name: "polar faster than hourly", provider: provPolar, interval: 30 * time.Minute},
		{name: "polar daily rejected", provider: provPolar, interval: 24 * time.Hour, wantErr: true},
		{name: "stripe legacy daily", provider: provStripe, interval: 24 * time.Hour},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateBillingPushInterval(tc.provider, tc.interval)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateBillingPushInterval(%q, %s) = %v, wantErr=%v", tc.provider, tc.interval, err, tc.wantErr)
			}
		})
	}
}
