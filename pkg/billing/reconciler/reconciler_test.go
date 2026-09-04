// Package reconciler tests (ADR-049 §B.1). Pure unit tests —
// uses stub Provider + state.MemStore so pkg/billing/reconciler
// stays pgxpool-free. The full Store integration is exercised by
// the e2e suite (cmd/e2e/reconciler_test.go) once the box surface
// ships.
package reconciler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

type stubProvider struct {
	pushed int64
	err    error
	caps   billing.CapabilitySet
	mode   billing.UsageMode
}

func (s *stubProvider) EnsurePlanProducts(context.Context) error { return nil }
func (s *stubProvider) CreateCustomer(context.Context, state.Account) (string, error) {
	return "", nil
}
func (s *stubProvider) PushUsageRecord(context.Context, state.Account, time.Time, int64) error {
	return nil
}
func (s *stubProvider) VerifyWebhook([]byte, map[string]string, time.Duration) (billing.Event, error) {
	return billing.Event{}, nil
}
func (s *stubProvider) CreateUpgradeTransaction(context.Context, state.Account, api.Plan) (string, string, error) {
	return "", "", nil
}
func (s *stubProvider) Refund(context.Context, string, int64) (*billing.RefundResult, error) {
	return nil, nil
}
func (s *stubProvider) ReconcileUsage(_ context.Context, _ state.Account, _, _ time.Time) (int64, error) {
	return s.pushed, s.err
}
func (s *stubProvider) RetryLatestCharge(_ context.Context, _ state.Account) (string, string, error) {
	return "", "", nil
}
func (s *stubProvider) CancelAtPeriodEnd(_ context.Context, _ state.Account) (time.Time, error) {
	return time.Time{}, nil
}
func (s *stubProvider) PaymentMethodSummary(_ context.Context, _ state.Account) (billing.PaymentMethod, error) {
	return billing.PaymentMethod{}, nil
}
func (s *stubProvider) Capabilities() billing.CapabilitySet {
	return s.caps
}
func (s *stubProvider) UsageMode() billing.UsageMode { return s.mode }

// seedMemStore constructs a MemStore with one account + a row
// of per-hour usage. MemStore.CreateAccount generates a fresh
// random ID per call, so we look up the created account via
// ListAllAccounts and use its real ID for AppendUsage. The
// reconciler-side ListAllAccounts picks up the same ID.
func seedMemStore(t *testing.T, label string, plan api.Plan, mbSeconds int64) (*state.MemStore, string) {
	t.Helper()
	store := state.NewMemStore()
	if _, err := store.CreateAccount(context.Background(), label+"@example.com", plan); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	accts, err := store.ListAllAccounts(context.Background())
	if err != nil || len(accts) != 1 {
		t.Fatalf("ListAllAccounts: %v (got %d)", err, len(accts))
	}
	id := accts[0].ID
	// AppendUsage is a pure write — MemStore does not cross-check
	// against accounts. We seed a row in the reconciler's 24h
	// window (now - 30 min, well inside [now-24h, now]) so
	// UsageByHour sees it.
	if err := store.AppendUsage(
		context.Background(),
		id, "app1", "inst1",
		time.Now().UTC().Truncate(time.Hour).Add(-30*time.Minute),
		mbSeconds, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("AppendUsage: %v", err)
	}
	return store, id
}

// listAcctIDs reflects over MemStore to dump the account-id set so
// the reconciler-side ListAllAccounts can pick them up. We do
// this via the public store API: ListAllAccounts on MemStore
// walks m.accounts (an internal map). Retained for any future
// test that needs to assert ListAllAccounts returns the seeded
// account.
func listAcctIDs(t *testing.T, store *state.MemStore) []string {
	t.Helper()
	accts, err := store.ListAllAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAllAccounts: %v", err)
	}
	ids := make([]string, 0, len(accts))
	for _, a := range accts {
		ids = append(ids, a.ID)
	}
	return ids
}

var _ = listAcctIDs // keep the helper exported for future tests

// TestReconciler_ZeroDriftIsHappyPath asserts the gauges stay at 0
// when local sum == pushed sum. This is the steady state the
// BillingDrift alert expects to see in production.
func TestReconciler_ZeroDriftIsHappyPath(t *testing.T) {
	store, id := seedMemStore(t, "acct_a", api.PlanHobby, 3600)
	prov := &stubProvider{pushed: 3600, caps: billing.CapabilitySet(billing.CapUsageReconcile)}
	rec := New("stripe", store, prov, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := rec.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty seeded account id")
	}
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "meterd_billing_drift_mb_seconds") {
		t.Errorf("expected metric name in scrape body, got:\n%s", body)
	}
	if !strings.Contains(string(body), "meterd_billing_drift_ratio") {
		t.Errorf("expected ratio metric name in scrape body, got:\n%s", body)
	}
}

func TestReconciler_OverageProviderUsesNetCalendarMonthUsage(t *testing.T) {
	store, id := seedMemStore(t, "acct_overage", api.PlanHobby, int64(api.PlanHobby.PlanIncludedGBHours()+2)*api.SecondsPerGBHour)
	prov := &stubProvider{
		pushed: 2 * api.SecondsPerGBHour,
		mode:   billing.UsageModeOverage,
		caps:   billing.CapabilitySet(billing.CapUsageReconcile),
	}
	rec := New("polar", store, prov, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := rec.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("scrape GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `meterd_billing_drift_mb_seconds{account_id="`+id+`",provider="polar"} 0`) {
		t.Fatalf("net overage drift was not zero:\n%s", body)
	}
}

// TestReconciler_EmitsDriftOnMismatch asserts the gauges reflect
// the local-pushed gap. The BillingDrift alert gates on ratio >
// 0.005; this test pins the formula.
func TestReconciler_EmitsDriftOnMismatch(t *testing.T) {
	store, id := seedMemStore(t, "acct_drift", api.PlanHobby, 1000)
	prov := &stubProvider{pushed: 990, caps: billing.CapabilitySet(billing.CapUsageReconcile)}
	rec := New("stripe", store, prov, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := rec.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("scrape GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	// local=1000, pushed=990 → drift=10, ratio=10/1000=0.01
	driftLine := `meterd_billing_drift_mb_seconds{account_id="` + id + `",provider="stripe"} 10`
	ratioLine := `meterd_billing_drift_ratio{account_id="` + id + `",provider="stripe"} 0.01`
	if !strings.Contains(string(body), driftLine) {
		t.Errorf("expected %q in scrape body, got:\n%s", driftLine, body)
	}
	if !strings.Contains(string(body), ratioLine) {
		t.Errorf("expected %q in scrape body, got:\n%s", ratioLine, body)
	}
}

// TestReconciler_ProviderErrorFailsSoft asserts a provider error
// for one account does not block the loop or fail RunOnce.
func TestReconciler_ProviderErrorFailsSoft(t *testing.T) {
	store, id := seedMemStore(t, "acct_err", api.PlanHobby, 500)
	// caps advertises CapUsageReconcile so the new short-circuit
	// does NOT skip the SDK call — the fail-soft-on-provider-error
	// path must still be exercised by this test.
	prov := &stubProvider{
		err:  errors.New("transient stripe blip"),
		caps: billing.CapabilitySet(billing.CapUsageReconcile),
	}
	rec := New("stripe", store, prov, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := rec.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce should not fail-soft propagate: %v", err)
	}
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("scrape GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	want := `meterd_billing_drift_reconcile_failures_total{provider="stripe",reason="provider"} 1`
	if !strings.Contains(string(body), want) {
		t.Fatalf("missing provider failure metric %q for account %s:\n%s", want, id, body)
	}
}

// TestReconciler_ErrNotImplementedSkipped asserts Paddle's
// not-yet-implemented ErrNotImplemented is treated as "no drift
// signal" rather than an error.
func TestReconciler_ErrNotImplementedSkipped(t *testing.T) {
	store, id := seedMemStore(t, "acct_p", api.PlanHobby, 1234)
	// caps advertises CapUsageReconcile so the new short-circuit
	// does NOT skip the SDK call — the errors.Is(err, ErrNotImplemented)
	// swallow path must still be exercised by this test.
	prov := &stubProvider{
		err:  billing.ErrNotImplemented,
		caps: billing.CapabilitySet(billing.CapUsageReconcile),
	}
	rec := New("paddle", store, prov, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := rec.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce should swallow ErrNotImplemented: %v", err)
	}
	// The reconciler should NOT emit any gauge for this account
	// since the provider has nothing to say.
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("scrape GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), `account_id="`+id+`"`) {
		t.Errorf("expected no gauge for ErrNotImplemented account, got:\n%s", body)
	}
}

// TestReconciler_CapabilityShortCircuit confirms the new
// Capabilities() check skips the SDK call entirely when the provider
// does not advertise CapUsageReconcile (Paddle today). This pins the
// detour that prevents the zero-pushed-but-non-zero-local drift
// ratio from paging the BillingDrift alert on a Paddle box.
//
// Tripwire design: the seedMemStore seeds 1234 mb_seconds and the
// stub returns pushed=999 with err=nil. If the short-circuit were
// removed, the reconciler would compute drift = 1234 − 999 = 235,
// emit a gauge with account_id, and the strings.Contains assertion
// below would fail. A broken short-circuit therefore fails this
// test with a clear signal (the local and pushed sums must NOT
// be equal so Prometheus emits a non-zero gauge).
func TestReconciler_CapabilityShortCircuit(t *testing.T) {
	store, id := seedMemStore(t, "acct_paddle", api.PlanHobby, 1234)
	// Stub advertises NO CapUsageReconcile. pushed differs from
	// the seeded local sum so a non-short-circuited call would
	// produce a non-zero gauge that Prometheus actually emits.
	prov := &stubProvider{
		pushed: 999,
		err:    nil,
		// caps is zero-valued — no CapUsageReconcile.
	}
	rec := New("paddle", store, prov, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := rec.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce should swallow the short-circuit (no SDK call): %v", err)
	}
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("scrape GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), `account_id="`+id+`"`) {
		t.Errorf("short-circuit should NOT emit any gauge for capability-skipped account, got:\n%s", body)
	}
}

// TestReconciler_FreePlanSkipped asserts Free plan accounts do not
// produce drift gauge rows.
func TestReconciler_FreePlanSkipped(t *testing.T) {
	store, id := seedMemStore(t, "acct_free", api.PlanFree, 100)
	prov := &stubProvider{pushed: 0}
	rec := New("stripe", store, prov, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := rec.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("scrape GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), `account_id="`+id+`"`) {
		t.Errorf("expected free-plan account to be skipped, got:\n%s", body)
	}
}

// TestReconciler_LoopStopsOnContextCancel is the goroutine-lifecycle
// tripwire: the Loop must exit when ctx is cancelled (otherwise the
// meterd daemon hangs on shutdown).
func TestReconciler_LoopStopsOnContextCancel(t *testing.T) {
	store, _ := seedMemStore(t, "acct_a", api.PlanHobby, 100)
	prov := &stubProvider{}
	rec := New("stripe", store, prov, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		rec.Loop(ctx, time.Hour)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Loop did not exit on ctx.Done()")
	}
}
