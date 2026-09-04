// pure_helpers_test.go — fill pkg/billing/paddle coverage of the
// tiny pure helpers reachable without the Paddle sandbox.
//
// Targets:
//   - Config.Defaults (no-op, but the contract still must hold:
//     a second call leaves a populated field untouched)
//   - Provider.monthlyPriceForPlan (the catalog RLock + map read)
//   - Provider.ListCatalog (the catalog snapshot)
//   - Provider.ResetCatalog (the no-op for Paddle)
//   - shouldInjectIdempotencyKey (POST + transactionsPathSegment match)
//
// Whitebox `package paddle`.
package paddle

import (
	"context"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// --- Config.Defaults (no-op contract) --------------------------

func TestConfigDefaults_NoOp(t *testing.T) {
	c := &Config{}
	c.Defaults()
	// Defaults is a no-op today; we don't want to assert zero on
	// any specific field (Paddle may add a default later) but the
	// call must not panic or mutate unrelated fields.
	if c.APIKey != "" || c.WebhookSecret != "" || c.Sandbox {
		t.Errorf("Defaults populated unexpected fields: %+v", c)
	}
}

// --- monthlyPriceForPlan ---------------------------------------

// monthlyPriceForPlan reads from the catalog RLock — without a
// hydration, every plan resolves to the empty string. This pins
// the "unhydrated catalog returns empty handles" contract so a
// future regression that returns the zero-value with a different
// sentinel trips here.
func TestMonthlyPriceForPlan_EmptyForUnhydrated(t *testing.T) {
	p := &Provider{catalog: &priceCatalog{}}
	for _, plan := range api.Plans {
		if got := p.monthlyPriceForPlan(plan); got != "" {
			t.Errorf("plan=%v: got %q, want empty", plan, got)
		}
	}
}

// --- ListCatalog -----------------------------------------------

// ListCatalog on an unhydrated Provider returns an empty slice
// (never nil) so the JSON marshaler renders `[]` and the CLI
// can distinguish "never synced" from "synced but no entries".
func TestListCatalog_EmptySliceWhenUnhydrated(t *testing.T) {
	p := &Provider{catalog: &priceCatalog{}}
	got := p.ListCatalog(context.Background())
	if got == nil {
		t.Fatal("ListCatalog returned nil; want empty slice")
	}
	if len(got) != 0 {
		t.Errorf("unhydrated: got %d entries, want 0", len(got))
	}
}

// --- ResetCatalog ----------------------------------------------

func TestResetCatalog_NoOp(t *testing.T) {
	p := &Provider{}
	if err := p.ResetCatalog(context.Background()); err != nil {
		t.Errorf("ResetCatalog: err = %v, want nil", err)
	}
}

// --- shouldInjectIdempotencyKey --------------------------------

// shouldInjectIdempotencyKey only fires on POST and only when
// the path contains the canonical "transactions" segment
// somewhere along it. Non-POST requests get a false regardless
// of path.
func TestShouldInjectIdempotencyKey_NonPostReturnsFalse(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req, _ := http.NewRequest(method, "/transactions/foo", nil)
		if shouldInjectIdempotencyKey(req) {
			t.Errorf("%s /transactions/foo: got true, want false", method)
		}
	}
}

// POST /transactions/anything → true (the canonical nested-txn path).
func TestShouldInjectIdempotencyKey_PostTransactionsReturnsTrue(t *testing.T) {
	cases := []string{
		"/transactions",
		"/transactions/foo",
		"/transactions/foo/revise",
		"/subscriptions/s1/transactions/t1", // nested
	}
	for _, path := range cases {
		req, _ := http.NewRequest(http.MethodPost, path, nil)
		if !shouldInjectIdempotencyKey(req) {
			t.Errorf("POST %q: got false, want true", path)
		}
	}
}

// POST outside the idempotent write namespaces → false (e.g. /products,
// /customers, /transactions-foo unrelated names).
func TestShouldInjectIdempotencyKey_PostNonIdempotentNamespaceReturnsFalse(t *testing.T) {
	cases := []string{
		"/products",
		"/customers",
		"/transactions-foo", // substring, not segment
	}
	for _, path := range cases {
		req, _ := http.NewRequest(http.MethodPost, path, nil)
		if shouldInjectIdempotencyKey(req) {
			t.Errorf("POST %q: got true, want false", path)
		}
	}
}

// POST with empty / nil path → false (defensive — shouldn't
// happen on a real request, but the helper must not panic).
func TestShouldInjectIdempotencyKey_NilPathReturnsFalse(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/", nil)
	req.URL = nil
	if shouldInjectIdempotencyKey(req) {
		t.Error("nil URL: got true, want false")
	}
}
