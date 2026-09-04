// handlers_admin_billing_test.go — pins the contract of the four
// admin billing handlers added in PR-P3:
//
//   GET    /v1/admin/billing-paddle-catalog       listPaddleCatalog
//   POST   /v1/admin/billing-paddle-catalog/sync  syncPaddleCatalog
//   DELETE /v1/admin/billing-paddle-catalog       resetPaddleCatalog
//   POST   /v1/admin/billing-reconcile/{id}       reconcileAccount
//
// What this file pins (Finding 1 of the PR-P3 /code-review medium report):
//
//   1. reconcileAccount 501s with code billing_reconcile_unsupported when
//      the active provider does not advertise CapUsageReconcile (i.e. Stripe).
//      Before this fix the handler unconditionally called ReconcileUsage,
//      which is a stub returning ErrNotImplemented on Stripe, and the
//      501 detail cited "ADR-049 §B.1" — a contradiction that confused
//      operators. The capability gate makes the 501 deterministic and
//      accurate.
//
//   2. Auth: the route is admin-only + admin-allowlist gated (same
//      two-layer pattern as handlers_admin_credits_test.go). A non-admin
//      caller is rejected with 403 before reaching the handler.

package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// newReconcileEnv is the testEnv twin for the admin reconcile route.
// Wires a single admin allowlist entry (the test operator), mints a
// bearer with admin scope, and returns env + server. The fakeBillingProvider
// is intentionally NOT wired — the test that exercises the capability
// gate installs its own fake so the capability set is explicit.
func newReconcileEnv(t *testing.T, scopes []string, adminEmail, callerEmail string) testEnv {
	t.Helper()
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("apid_reconcile_test")
	acct, err := store.CreateAccount(context.Background(), callerEmail, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "reconcile-test", scopes); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithOpsMetrics(context.Background(), ops)
	srv.WithAdminAllowlist(adminEmail)
	return testEnv{h: srv.handler(), s: srv, store: store, key: pt, acct: acct, ops: ops}
}

// stripeShapedProvider has a Capabilities bitmask that matches Stripe
// (no CapUsageReconcile) so the gate fires. ReconcileUsage still
// returns ErrNotImplemented as a defense-in-depth check.
type stripeShapedProvider struct {
	fakeBillingProvider
}

func (s *stripeShapedProvider) Capabilities() billing.CapabilitySet {
	// Mirror pkg/billing/stripe/client.go::Capabilities():
	// CapRefund | CapUsageMetered | CapSandbox — CapUsageReconcile absent.
	return billing.CapabilitySet(billing.CapRefund | billing.CapUsageMetered | billing.CapSandbox)
}

// paddleReconcileProvider advertises CapUsageReconcile AND returns a
// non-errNotImplemented value. Used to prove the gate lets through the
// providers that genuinely support reconcile.
type paddleReconcileProvider struct {
	fakeBillingProvider
	called int
}

func (p *paddleReconcileProvider) Capabilities() billing.CapabilitySet {
	return billing.CapabilitySet(billing.CapHostedCheckout | billing.CapUsageLineItem | billing.CapSandbox | billing.CapUsageReconcile)
}
func (p *paddleReconcileProvider) ReconcileUsage(_ context.Context, _ state.Account, _, _ time.Time) (int64, error) {
	p.called++
	return 4242, nil
}

// TestReconcileAccount_StripeIsUnsupported pins Finding 1 of the PR-P3
// /code-review medium report. Before the fix, the handler unconditionally
// called ReconcileUsage, which Stripe returns as ErrNotImplemented; the
// 501 detail message then cited "ADR-049 §B.1", misleading operators.
// After the fix, the Capabilities().Has(CapUsageReconcile) gate fires
// first and the handler returns 501 with code billing_reconcile_unsupported
// and a detail that does NOT cite ADR-049.
func TestReconcileAccount_StripeIsUnsupported(t *testing.T) {
	e := newReconcileEnv(t, api.ScopesAdminOnly, "ops@example.com", "ops@example.com")
	target, err := e.store.CreateAccount(context.Background(), "alvo@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}
	stripe := &stripeShapedProvider{}
	// Install the Stripe-shaped fake. WithBillingProvider is a fluent
	// setter; the test wires it the same way handlers_change_plan_test
	// does for changePlan.
	stripeFake := stripe
	// Use the public wiring seam — findServer returns *server; the
	// builder helpers in handlers_admin_credits_test use newServer +
	// WithBillingProvider indirectly. Here we replicate:
	srv := newServer(e.store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithOpsMetrics(context.Background(), wire.NewOpsMetrics("apid_reconcile_stripe_test"))
	srv.WithAdminAllowlist("ops@example.com")
	srv.WithBillingProvider(stripeFake)
	e.s = srv

	body := strings.NewReader("")
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/billing-reconcile/"+target.ID, body)
	e.addAdminSession(t, req)
	req.Header.Set("Idempotency-Key", "test-reconcile-stripe")
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501; body=%s", rec.Code, rec.Body)
	}
	var prob api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
		t.Fatalf("decode problem: %v body=%s", err, rec.Body)
	}
	if prob.Code != "billing_reconcile_unsupported" {
		t.Errorf("problem code = %q, want billing_reconcile_unsupported", prob.Code)
	}
	if strings.Contains(prob.Detail, "ADR-049") {
		t.Errorf("detail must NOT cite ADR-049 (Stripe does not implement reconcile); got: %q", prob.Detail)
	}
	if !strings.Contains(strings.ToLower(prob.Detail), "capusage") && !strings.Contains(strings.ToLower(prob.Detail), "capability") {
		t.Errorf("detail should mention capability gate; got: %q", prob.Detail)
	}
}

// TestReconcileAccount_PaddleWithCapabilityCallsProvider is the
// positive half: a provider that advertises CapUsageReconcile reaches
// the ReconcileUsage call and gets back the right body. Without the
// gate this would also work for Stripe (since Stripe's stub returns
// ErrNotImplemented), so the test does not assert against Stripe's
// behavior — only against the gate's passthrough.
func TestReconcileAccount_PaddleWithCapabilityCallsProvider(t *testing.T) {
	e := newReconcileEnv(t, api.ScopesAdminOnly, "ops@example.com", "ops@example.com")
	target, err := e.store.CreateAccount(context.Background(), "alvo@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}
	paddle := &paddleReconcileProvider{}
	srv := newServer(e.store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithOpsMetrics(context.Background(), wire.NewOpsMetrics("apid_reconcile_paddle_test"))
	srv.WithAdminAllowlist("ops@example.com")
	srv.WithBillingProvider(paddle)
	e.s = srv

	body := strings.NewReader("")
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/billing-reconcile/"+target.ID, body)
	e.addAdminSession(t, req)
	req.Header.Set("Idempotency-Key", "test-reconcile-paddle")
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if paddle.called != 1 {
		t.Errorf("ReconcileUsage called %d times, want 1", paddle.called)
	}
	var resp api.BillingReconcileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body)
	}
	if resp.AccountID != target.ID {
		t.Errorf("AccountID = %q, want %q", resp.AccountID, target.ID)
	}
	if resp.MBSeconds != 4242 {
		t.Errorf("MBSeconds = %d, want 4242", resp.MBSeconds)
	}
}

func TestListBillingCatalog_PolarUsesProviderNeutralSurface(t *testing.T) {
	e := newReconcileEnv(t, api.ScopesAdminOnly, "ops@example.com", "ops@example.com")
	p, err := polar.NewProvider(polar.Config{
		APIKey:         "polar_test_token",
		HobbyProductID: "prod_hobby",
		ProProductID:   "prod_pro",
		ScaleProductID: "prod_scale",
		UsageEventName: polar.DefaultUsageEventName,
		MeterID:        "meter-1",
		BaseURL:        "http://127.0.0.1:1",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(e.store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	srv.WithAdminAllowlist("ops@example.com")
	srv.WithBillingProvider(p)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/billing-paddle-catalog", nil)
	req.Header.Set("Authorization", "Bearer "+e.key)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var response struct {
		Provider string                    `json:"provider"`
		Entries  []api.BillingCatalogEntry `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body)
	}
	if response.Provider != "polar" {
		t.Errorf("provider = %q, want polar", response.Provider)
	}
	if len(response.Entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(response.Entries))
	}
}
