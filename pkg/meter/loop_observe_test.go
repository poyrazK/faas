package meter_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// runLoopBrief runs a Loop with sub-second intervals until each wired
// timer has at least one tick, then cancels and waits for clean drain.
// Returns the Loop and the ops handle so the test can scrape the
// registry and (separately) inspect the per-tick last-fire map.
//
// fixtureStore can be nil — the sample / quota loops may return errors
// but tick bodies still run, so Observe is called.
func runLoopBrief(t *testing.T, store state.Store, dunning *meter.Dunning) (*meter.Loop, *wire.OpsMetrics) {
	t.Helper()
	ops := wire.NewOpsMetrics("meter_test_observe")
	cfg := &meter.Config{}
	cfg.Defaults()
	cfg.SampleInterval = 20 * time.Millisecond
	cfg.QuotaInterval = 20 * time.Millisecond
	cfg.StripeInterval = 20 * time.Millisecond
	cfg.DunningInterval = 20 * time.Millisecond

	loop := meter.NewLoop(
		store,
		&fakeParker{},
		nil, // StripePusher — nil; the pusher returns nil, error "pusher not configured"
		&fakeNotifier{},
		dunning,
		func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) },
		discardLog(),
		cfg,
		ops,
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("loop returned %v, want nil on cancel", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("loop did not return within 3s of cancel")
	}
	return loop, ops
}

// scrapeBody renders the registry through the same handler
// cmd/meterd mounts at /metrics, so the test asserts the actual
// on-the-wire Prometheus text format. Mirrors pkg/wire/metrics_test.go:
// 125-138 (HandlerStandalone) — httptest.NewRecorder + Handler().
func scrapeBody(t *testing.T, ops *wire.OpsMetrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	ops.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

// TestLoop_ObserveSampleAndStripe — the two ops that share the
// runTicks path. Asserts both ops surface in the registry with at
// least one *_total{op=...} line. Fails closed if Observe isn't wired.
func TestLoop_ObserveSampleAndStripe(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	// Seed at least one app so SampleAndRoll has work to do (else it
	// returns []RolledRow with no error; loop still ticks).
	acct, err := store.CreateAccount(context.Background(), "u@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if _, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "test-app", Type: state.AppTypeApp,
	}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	_, ops := runLoopBrief(t, store, nil)
	body := scrapeBody(t, ops)

	// Prometheus text format quotes label values: `op="sample",code="ok"`.
	if !strings.Contains(body, `op="sample"`) {
		t.Errorf("missing sample op in /metrics registry:\n%s", body)
	}
	if !strings.Contains(body, `op="stripe"`) {
		t.Errorf("missing stripe op in /metrics registry:\n%s", body)
	}
}

// TestLoop_ObserveQuotaHistogram — quota's runQuotaOnce always passes
// nil to Observe from runQuotaTicks (per pkg/meter/loop.go — quota has
// no aggregate err to surface). The histogram _count line is the
// stable assertion across both ok and empty-account setups.
func TestLoop_ObserveQuotaHistogram(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	if _, err := store.CreateAccount(context.Background(), "u@example.com", api.PlanHobby); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	_, ops := runLoopBrief(t, store, nil)
	body := scrapeBody(t, ops)
	if !strings.Contains(body, `op="quota"`) {
		t.Errorf("missing quota op in /metrics registry:\n%s", body)
	}
}
