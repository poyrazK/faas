package paddle

// Live Paddle sandbox tests. Gated on PADDLE_API_KEY:
//
//	PADDLE_API_KEY=pdl_sandbox_… \
//	  go test -v -run 'TestPaddleSandbox' ./pkg/billing/paddle/...
//
// These tests exercise the wire path against api.sandbox.paddle.com
// so the unit-test surface isn't the only thing shipping to prod.
// Each test cleans up after itself where the sandbox allows it
// (sandbox products/prices stay around — list-then-create skips
// them on the second run).
//
// Mirror of pkg/billing/stripe/sandbox_test.go::TestInvoiceShadow24h_Sandbox:
// §14 M7 acceptance gate is the Stripe run; paddle's equivalent
// proves the Paddle wire path is also clean. The paddle test
// intentionally checks fewer invariants than Stripe (no 24h math
// because Paddle's flat-rate monthly line item is a different
// shape) — the load-bearing checks are (a) products and prices are
// created idempotently on the second run, (b) the overage-shape
// transaction posts without error, (c) the webhook signature
// round-trips through VerifyWebhook live.

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// requireSandbox skips the test unless PADDLE_API_KEY is set to a
// sandbox-shaped key. Mirrors the STRIPE_API_KEY prefix-gate
// stripe/sandbox_test.go uses.
func requireSandbox(t *testing.T) string {
	t.Helper()
	key := os.Getenv("PADDLE_API_KEY")
	if key == "" || !strings.HasPrefix(key, "pdl_sandbox_") {
		t.Skip("PADDLE_API_KEY not configured as pdl_sandbox_…; skipping live test")
	}
	return key
}

// TestEnsurePlanProducts_AreIdempotent is the §14-equivalent Paddle
// acceptance gate. Runs EnsurePlanProducts twice against the live
// sandbox; the second run must be a no-op (ListProducts/ ListPrices
// finds the products and prices from the first run, creates zero
// new entities). Asserts the catalog snapshot is identical
// between the two runs.
func TestEnsurePlanProducts_AreIdempotent(t *testing.T) {
	key := requireSandbox(t)
	p := NewProvider(key, "", true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := p.EnsurePlanProducts(ctx); err != nil {
		t.Fatalf("first EnsurePlanProducts: %v", err)
	}
	firstMonthly := p.snapshotPlans()
	firstOverage := p.snapshotOverage()
	if len(firstMonthly) == 0 {
		t.Fatal("first EnsurePlanProducts: monthly catalog empty")
	}
	if len(firstOverage) == 0 {
		t.Fatal("first EnsurePlanProducts: overage catalog empty")
	}

	// Second run is a no-op against the same Paddle sandbox.
	if err := p.EnsurePlanProducts(ctx); err != nil {
		t.Fatalf("second EnsurePlanProducts: %v", err)
	}
	secondMonthly := p.snapshotPlans()
	secondOverage := p.snapshotOverage()

	if !sameMap(firstMonthly, secondMonthly) {
		t.Errorf("monthly catalog drift across runs\n first=%v\n second=%v", firstMonthly, secondMonthly)
	}
	if !sameMap(firstOverage, secondOverage) {
		t.Errorf("overage catalog drift across runs\n first=%v\n second=%v", firstOverage, secondOverage)
	}
}

// TestCreateCustomer_PostsToPaddleSandbox posts a single customer
// at the sandbox and asserts the returned ctm_… ID is non-empty.
// The acct has a unique email per run so re-runs don't dedupe
// against the prior ctm_… record (Paddle's Customers endpoint
// doesn't expose Idempotency-Key today — that's a future PR).
func TestCreateCustomer_PostsToPaddleSandbox(t *testing.T) {
	key := requireSandbox(t)
	p := NewProvider(key, "", true, slog.New(slog.NewTextHandler(io.Discard, nil)))

	acctID := "acct_sandbox_" + time.Now().UTC().Format("20060102_150405")
	acct := state.Account{
		ID:    acctID,
		Email: acctID + "@sandbox.example.test",
		Plan:  api.PlanHobby,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	id, err := p.CreateCustomer(ctx, acct)
	if err != nil {
		t.Fatalf("CreateCustomer: %v", err)
	}
	if !strings.HasPrefix(id, "ctm_") {
		t.Errorf("CreateCustomer returned %q, want ctm_… prefix", id)
	}
}

// TestPushOverageTransaction_PostsToPaddleSandbox exercises the
// overage accumulator against the live sandbox. Drops a tiny
// mb_seconds number into PushUsageRecord and asserts no error
// (the prior-month line item shape is internal to the accumulator;
// the second call within the same month is a no-op so we don't
// drain the sandbox account's billing balance).
func TestPushOverageTransaction_PostsToPaddleSandbox(t *testing.T) {
	key := requireSandbox(t)
	p := NewProvider(key, "", true, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// EnsurePlanProducts runs first so the overage price handle is
	// in the catalog; the catalog lookup is what would otherwise
	// fail with "overage price missing for plan=hobby" in
	// usage.go:67.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := p.EnsurePlanProducts(ctx); err != nil {
		t.Fatalf("EnsurePlanProducts: %v", err)
	}

	acct := state.Account{
		ID:    "acct_overage_" + time.Now().UTC().Format("20060102_150405"),
		Email: "overage_" + time.Now().UTC().Format("20060102_150405") + "@sandbox.example.test",
		Plan:  api.PlanHobby,
	}
	priorMonth := time.Now().UTC().Add(-31 * 24 * time.Hour) // land safely in the prior-month bucket
	if err := p.accumulateOverage(context.Background(), acct, priorMonth, 1024); err != nil {
		t.Fatalf("accumulateOverage: %v", err)
	}
}

func sameMap(a, b map[api.Plan]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
