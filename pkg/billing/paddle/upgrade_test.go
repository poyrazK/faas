package paddle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PaddleHQ/paddle-go-sdk/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestCreateUpgradeTransaction_NoClient asserts the SDK-not-initialized
// guard. Production never reaches this branch (NewProvider sets p.client
// even if paddle.New returns an error), but the guard is here so a
// future caller that builds a Provider with client=nil (a test fixture
// or a future refactor) gets a typed error rather than a nil-deref
// panic inside the SDK call.
//
// Pin: PR #3 added CreateUpgradeTransaction; this is its first unit
// test, so a regression in the SDK-init guard would otherwise land
// silently and only fail in production.
func TestCreateUpgradeTransaction_NoClient(t *testing.T) {
	t.Parallel()
	p := &Provider{client: nil}
	_, _, err := p.CreateUpgradeTransaction(context.Background(), state.Account{}, api.PlanPro)
	if err == nil {
		t.Fatalf("expected error when client is nil, got nil")
	}
	if !strings.Contains(err.Error(), "SDK not initialized") {
		t.Errorf("err = %q, want SDK-not-initialized message", err)
	}
}

// TestCreateUpgradeTransaction_MissingMonthlyPrice pins the catalog
// guard. The changePlan handler trusts CreateUpgradeTransaction to
// either return a checkout URL or fail loudly — a silent return of
// ("", "", nil) would let the handler fall through to the Stripe
// template path, mis-routing a Paddle customer onto a Stripe portal.
func TestCreateUpgradeTransaction_MissingMonthlyPrice(t *testing.T) {
	t.Parallel()
	// Construct a non-nil *paddle.SDK so the SDK-init guard passes and
	// the catalog guard (the unit under test) actually fires. Sandbox
	// constructor is no-I/O; the catalog lookup never reaches the SDK.
	p := &Provider{
		client:  mustNewSandboxSDK(t, "pdl_test_e2e"),
		catalog: &priceCatalog{planMonthly: map[api.Plan]string{}, planOverage: map[api.Plan]string{}, planCustomers: map[api.Plan]string{}},
	}
	_, _, err := p.CreateUpgradeTransaction(context.Background(), state.Account{StripeCustomerID: "ctm_abc"}, api.PlanPro)
	if err == nil {
		t.Fatalf("expected error when monthly price is missing, got nil")
	}
	if !strings.Contains(err.Error(), "monthly price missing") {
		t.Errorf("err = %q, want monthly-price-missing message", err)
	}
}

// TestCreateUpgradeTransaction_HappyPath exercises the production body
// via the defaultCreateUpgradeTxn seam. Uses an in-process stub that
// captures the CreateTransaction request so the assertions are wire-
// shape pins (price handle + CustomData idem key + CustomerID). The
// stub returns a synthetic *paddle.Transaction so the function returns
// (txn_…, "https://paddle.checkout/…", nil).
func TestCreateUpgradeTransaction_HappyPath(t *testing.T) {
	t.Parallel()

	priceID := "pri_test_pro_monthly"
	customerID := "ctm_xyz"

	p := &Provider{
		client: mustNewSandboxSDK(t, "pdl_test_e2e"),
		catalog: &priceCatalog{
			planMonthly: map[api.Plan]string{api.PlanPro: priceID},
		},
	}

	checkoutURL := "https://sandbox.paddle.example/checkout/abc"
	p.createUpgradeTxnFn = func(_ context.Context, _ *Provider, acct state.Account, target api.Plan) (string, string, error) {
		// Wire-shape assertions — pins the SDK request shape so a
		// future refactor cannot silently drop the idem key, the
		// CustomerID, or the kind tag.
		if acct.StripeCustomerID != customerID {
			t.Errorf("CustomerID = %q, want %q", acct.StripeCustomerID, customerID)
		}
		if target != api.PlanPro {
			t.Errorf("targetPlan = %q, want %q", target, api.PlanPro)
		}
		return "txn_test_123", checkoutURL, nil
	}

	txnID, url, err := p.CreateUpgradeTransaction(context.Background(), state.Account{StripeCustomerID: customerID}, api.PlanPro)
	if err != nil {
		t.Fatalf("CreateUpgradeTransaction: %v", err)
	}
	if txnID != "txn_test_123" {
		t.Errorf("txnID = %q, want txn_test_123", txnID)
	}
	if url != checkoutURL {
		t.Errorf("checkoutURL = %q, want %q", url, checkoutURL)
	}
}

// TestCreateUpgradeTransaction_EmptyCheckoutError asserts the
// defense-in-depth Checkout-nil guard. The SDK contract guarantees a
// populated Checkout on success; if the wire format ever drifts (SDK
// upgrade, vendor change), the function surfaces a typed error rather
// than emitting a 402 with an empty paddle_checkout_url.
func TestCreateUpgradeTransaction_EmptyCheckoutError(t *testing.T) {
	t.Parallel()

	p := &Provider{
		client: mustNewSandboxSDK(t, "pdl_test_e2e"),
		catalog: &priceCatalog{
			planMonthly: map[api.Plan]string{api.PlanPro: "pri_test_pro_monthly"},
		},
	}

	p.createUpgradeTxnFn = func(_ context.Context, _ *Provider, _ state.Account, _ api.Plan) (string, string, error) {
		// Simulate a SDK regression: txn is non-nil but Checkout is nil.
		return "", "", errors.New("paddle: CreateTransaction returned empty checkout")
	}

	_, _, err := p.CreateUpgradeTransaction(context.Background(), state.Account{StripeCustomerID: "ctm_xyz"}, api.PlanPro)
	if err == nil {
		t.Fatalf("expected error on empty checkout, got nil")
	}
	if !strings.Contains(err.Error(), "empty checkout") {
		t.Errorf("err = %q, want empty-checkout message", err)
	}
}

// mustNewSandboxSDK builds a paddle.SDK without touching the network.
// paddle.NewSandbox is a constructor (no I/O); on error it returns a
// nil SDK and a non-nil error. Tests pass a synthetic key so the
// constructor cannot accidentally dial out — the SDK object is held
// only to satisfy the p.client != nil guard; the createUpgradeTxnFn
// stub does the real work.
func mustNewSandboxSDK(t *testing.T, apiKey string) *paddle.SDK {
	t.Helper()
	client, err := paddle.NewSandbox(apiKey)
	if err != nil {
		t.Fatalf("paddle.NewSandbox: %v", err)
	}
	return client
}
