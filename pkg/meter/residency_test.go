package meter_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// newResidencyTestHarness builds a Residency pointed at a fresh
// in-memory store and returns the OpsMetrics registry the test scrapes
// after RunOnce. now is fixed at 2026-07-21 so the "current month"
// math is stable; test cases that exercise different months pass a
// different time to AppendUsage + an aligned now.
func newResidencyTestHarness(t *testing.T, now time.Time) (*meter.Residency, *state.MemStore, *wire.OpsMetrics) {
	t.Helper()
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("meter_test_residency")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := meter.NewResidency(store, func() time.Time { return now }, log, ops)
	return r, store, ops
}

// appendUsage seeds a single (account, app, month) usage row.
// MBSeconds = mb × seconds; GB-hours = mbSec / 1024 / 3600. The
// test fixtures use 1024*3600 = 3,686,400 MB-seconds per GB-hour
// to keep the math readable.
func appendUsage(t *testing.T, store *state.MemStore, accountID string, mbSec int64, when time.Time) {
	t.Helper()
	if err := store.AppendUsage(context.Background(), accountID, "app-"+accountID, "inst-"+accountID, when, mbSec, 1); err != nil {
		t.Fatalf("append usage: %v", err)
	}
}

// gaugeForPlan scrapes the registry and returns the gauge sample for
// the given plan label, or 0 if absent.
func gaugeForPlan(t *testing.T, ops *wire.OpsMetrics, plan string) float64 {
	t.Helper()
	body := scrapeResidencyBody(t, ops)
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "meter_test_residency_resident_gb_per_customer{") {
			continue
		}
		if !strings.Contains(line, `plan="`+plan+`"`) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			continue
		}
		return v
	}
	return 0
}

// scrapeResidencyBody renders the registry through the Prometheus
// text handler the daemon would mount at /metrics. Mirrors the
// scrapeBody helper in loop_observe_test.go (same package) — kept
// separate so the residency tests are self-contained.
func scrapeResidencyBody(t *testing.T, ops *wire.OpsMetrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	ops.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

func TestResidency_PayingPredicate(t *testing.T) {
	cases := []struct {
		name string
		st   state.AccountStatus
		want bool
	}{
		{"active counts", state.AccountActive, true},
		{"past_due counts", state.AccountPastDue, true},
		{"suspended counts", state.AccountSuspended, true},
		{"deleted_pending excluded", state.AccountDeletedPending, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := meter.Paying(state.Account{Status: tc.st})
			if got != tc.want {
				t.Errorf("Paying(%s) = %v, want %v", tc.st, got, tc.want)
			}
		})
	}
}

// TestResidency_RunOnce_HappyPath: 2 Hobby paying customers consume
// 1 GB-month and 2 GB-month → avg = 1.5 GB/customer → gauge emits 1.5
// for plan=hobby. The other plans stay at 0 (no paying customers).
// Pre-instantiated label set means every plan label surfaces in
// /metrics from boot even with zero customers.
func TestResidency_RunOnce_HappyPath(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	r, store, ops := newResidencyTestHarness(t, now)

	a1, err := store.CreateAccount(context.Background(), "h1@x", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := store.CreateAccount(context.Background(), "h2@x", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	appendUsage(t, store, a1.ID, 1024*3600, now) // 1 GB-hour
	appendUsage(t, store, a2.ID, 2*1024*3600, now) // 2 GB-hour

	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if got := gaugeForPlan(t, ops, "hobby"); got != 1.5 {
		t.Errorf("hobby gauge = %v, want 1.5 (avg of 1 + 2 over 2 customers)", got)
	}
	for _, p := range []string{"free", "pro", "scale"} {
		if got := gaugeForPlan(t, ops, p); got != 0 {
			t.Errorf("%s gauge = %v, want 0 (no paying customers)", p, got)
		}
	}
}

// TestResidency_RunOnce_ExcludesDeletedPending: a deleted_pending
// account's monthly GB-hours do NOT contribute to the per-plan
// average. Without this guard the metric would include stale data
// from accounts that have signed up for deletion (G6) but whose
// retention sweep hasn't pruned usage rows yet.
func TestResidency_RunOnce_ExcludesDeletedPending(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	r, store, ops := newResidencyTestHarness(t, now)

	a, err := store.CreateAccount(context.Background(), "to-delete@x", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStatus(context.Background(), a.ID, state.AccountDeletedPending); err != nil {
		t.Fatal(err)
	}
	appendUsage(t, store, a.ID, 4*1024*3600, now) // 4 GB-hour

	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got := gaugeForPlan(t, ops, "pro"); got != 0 {
		t.Errorf("pro gauge = %v, want 0 (deleted_pending excluded)", got)
	}
}

// TestResidency_RunOnce_MissingUsageRows: an active account with no
// usage rows contributes to the paying-customer count but not the
// total GB. NaN-free result. Common case for new accounts that
// haven't woken an instance yet.
func TestResidency_RunOnce_MissingUsageRows(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	r, store, ops := newResidencyTestHarness(t, now)

	a1, err := store.CreateAccount(context.Background(), "active-no-usage@x", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	_ = a1 // counted but no usage rows; the assertion is on counts[PlanScale]==2
	a2, err := store.CreateAccount(context.Background(), "active-with-usage@x", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	appendUsage(t, store, a2.ID, 4*1024*3600, now) // 4 GB-hour

	counts, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if counts[api.PlanScale] != 2 {
		t.Errorf("scale paying count = %d, want 2 (both active)", counts[api.PlanScale])
	}
	// Average is Σ=4 over n=2 = 2 GB/customer.
	if got := gaugeForPlan(t, ops, "scale"); got != 2 {
		t.Errorf("scale gauge = %v, want 2", got)
	}
}

// TestResidency_LoopRunTicks: end-to-end smoke that the Residency
// timer in pkg/meter.Loop fires on ResidencyInterval and stamps
// lastTick["residency"]. Mirrors the loop_health_test pattern but
// with ResidencyInterval=20ms.
func TestResidency_LoopRunTicks(t *testing.T) {
	ops := wire.NewOpsMetrics("meter_test_loop_residency")
	cfg := &meter.Config{}
	cfg.Defaults()
	cfg.SampleInterval = time.Hour
	cfg.QuotaInterval = time.Hour
	cfg.StripeInterval = time.Hour
	cfg.DunningInterval = time.Hour
	cfg.ResidencyInterval = 20 * time.Millisecond

	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	store := state.NewMemStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	residency := meter.NewResidency(store, func() time.Time { return now }, log, ops)
	loop := meter.NewLoop(store, &fakeParker{}, nil, &fakeNotifier{}, nil, residency,
		func() time.Time { return now }, log, cfg, ops)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()
	time.Sleep(120 * time.Millisecond) // > 5 ticks
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("loop returned %v, want nil on cancel", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("loop did not return within 3s of cancel")
	}
	last, ok := loop.LastTick("residency")
	if !ok {
		t.Fatal("residency tick never fired")
	}
	if time.Since(last) > time.Second {
		t.Errorf("residency last tick = %v, want within 1s", last)
	}
}
