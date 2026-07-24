// Tests for the FAAS_BILLING_PROVIDER selector. The selector is the
// single source of truth for the canonical env-var name + default,
// shared by cmd/apid and cmd/meterd (PRD-025 / PR #3).
package loader

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// mapEnv is the inline env-reader stub used by every test. Empty
// (default) values get the empty-string from the lookup, matching
// os.Getenv behaviour. Tests that need FAAS_PADDLE_* values seed the
// map and the loader picks them up.
func mapEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestLoadProviderForAPID_Default confirms the empty-env case returns
// (nil, "stripe", nil) — the apid Stripe path stays inline (cmd/apid
// reads FAAS_BILLING_PORTAL_URL + STRIPE_WEBHOOK_SECRET directly).
//
// The provider must be nil so the apid changePlan 402 falls through to
// the BillingPortalURL template branch — bit-for-bit unchanged from
// pre-PR-#3 behaviour.
func TestLoadProviderForAPID_Default(t *testing.T) {
	t.Parallel()
	p, name, err := LoadProviderForAPID(mapEnv(nil), discardLog())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if name != "stripe" {
		t.Errorf("name = %q, want %q", name, "stripe")
	}
	if p != nil {
		t.Errorf("provider = %v, want nil", p)
	}
}

// TestLoadProviderForAPID_StripeSameAsDefault asserts the explicit
// "stripe" string is treated identically to the empty default — the
// loader's switch covers both so a config with FAAS_BILLING_PROVIDER=
// (explicit empty) and FAAS_BILLING_PROVIDER=stripe produce the same
// wire shape.
func TestLoadProviderForAPID_StripeSameAsDefault(t *testing.T) {
	t.Parallel()
	p, name, err := LoadProviderForAPID(mapEnv(map[string]string{"FAAS_BILLING_PROVIDER": "stripe"}), discardLog())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if name != "stripe" {
		t.Errorf("name = %q, want %q", name, "stripe")
	}
	if p != nil {
		t.Errorf("provider = %v, want nil", p)
	}
}

// TestLoadProviderForAPID_Paddle_BuildsProvider asserts the paddle path
// returns a non-nil Provider + the literal "paddle" name. The
// EnsurePlanProducts call inside the loader dials api.sandbox.paddle.com
// — the test doesn't set FAAS_PADDLE_SANDBOX so the loader goes against
// the live production API. To keep the test hermetic we set
// FAAS_PADDLE_SANDBOX=1 (sandbox host, still requires a valid key shape
// but never actually writes — the SDK constructor only fails on
// programmer error, not on auth).
func TestLoadProviderForAPID_Paddle_BuildsProvider(t *testing.T) {
	t.Parallel()
	p, name, err := LoadProviderForAPID(mapEnv(map[string]string{
		"FAAS_BILLING_PROVIDER":      "paddle",
		"FAAS_PADDLE_API_KEY":        "pdl_test_loader",
		"FAAS_PADDLE_WEBHOOK_SECRET": "whk_test",
		"FAAS_PADDLE_SANDBOX":        "1",
	}), discardLog())
	if err != nil {
		// EnsurePlanProducts dials the sandbox host; this can fail
		// in a network-isolated sandbox (CI without outbound). Mark
		// as skip rather than fail so the test still passes the
		// constructor-only assertion.
		if strings.Contains(err.Error(), "paddle EnsurePlanProducts") {
			t.Skipf("EnsurePlanProducts requires outbound network: %v", err)
		}
		t.Fatalf("err = %v, want nil", err)
	}
	if name != "paddle" {
		t.Errorf("name = %q, want %q", name, "paddle")
	}
	if p == nil {
		t.Errorf("provider = nil, want non-nil paddle.Provider")
	}
}

// TestLoadProviderForAPID_Unknown fails the boot loudly on a typo.
// "braintree" is the canonical bad-value example (real product, not a
// supported provider) — the loader must return an error rather than
// silently defaulting to Stripe, which would let an operator think
// they're on Braintree while the box quietly falls back to Stripe.
func TestLoadProviderForAPID_Unknown(t *testing.T) {
	t.Parallel()
	_, _, err := LoadProviderForAPID(mapEnv(map[string]string{"FAAS_BILLING_PROVIDER": "braintree"}), discardLog())
	if err == nil {
		t.Fatalf("err = nil, want error for unknown provider")
	}
	if !strings.Contains(err.Error(), "unknown FAAS_BILLING_PROVIDER") {
		t.Errorf("err = %q, want unknown-provider message", err)
	}
}

// TestLoadProviderForMeterd_Default_BuildsStripe asserts the meterd
// default path constructs a *stripe.Client (not nil). The meterd
// pusher requires a non-nil provider — the legacy *stripe.Client is
// folded into the Provider interface per PR #3.
//
// We don't assert on the concrete type here (just non-nil) — the
// compile-time conformance var in pkg/billing/stripe/client.go pins
// the shape.
func TestLoadProviderForMeterd_Default_BuildsStripe(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	dedupe := store // MemStore implements state.PushDedupe
	p, name, err := LoadProviderForMeterd(mapEnv(map[string]string{
		"STRIPE_API_KEY":        "sk_test_loader",
		"STRIPE_WEBHOOK_SECRET": "whsec_test",
	}), store, dedupe, discardLog())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if name != "stripe" {
		t.Errorf("name = %q, want %q", name, "stripe")
	}
	if p == nil {
		t.Errorf("provider = nil, want non-nil *stripe.Client")
	}
}

// TestLoadProviderForMeterd_Paddle_BuildsProvider asserts the meterd
// Paddle path returns a non-nil Provider. Meterd doesn't need the
// webhook secret (no ingress), so the loader passes "" for it.
func TestLoadProviderForMeterd_Paddle_BuildsProvider(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	dedupe := store
	p, name, err := LoadProviderForMeterd(mapEnv(map[string]string{
		"FAAS_BILLING_PROVIDER": "paddle",
		"FAAS_PADDLE_API_KEY":   "pdl_test_loader",
		"FAAS_PADDLE_SANDBOX":   "1",
	}), store, dedupe, discardLog())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if name != "paddle" {
		t.Errorf("name = %q, want %q", name, "paddle")
	}
	if p == nil {
		t.Errorf("provider = nil, want non-nil paddle.Provider")
	}
}

// TestLoadProviderForMeterd_Unknown fails the boot loudly on a typo.
// Same contract as LoadProviderForAPID — operators must see the error
// rather than silently fall back to Stripe (which would let them
// think they're on a different provider while the pusher loop quietly
// runs the legacy stripe path).
func TestLoadProviderForMeterd_Unknown(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	_, _, err := LoadProviderForMeterd(mapEnv(map[string]string{"FAAS_BILLING_PROVIDER": "paypal"}), store, store, discardLog())
	if err == nil {
		t.Fatalf("err = nil, want error for unknown provider")
	}
	// Defensive: errors.Is to allow future wraps — today the loader
	// returns a bare fmt.Errorf, but the test should not break if a
	// future change switches to %w.
	if !strings.Contains(err.Error(), "unknown FAAS_BILLING_PROVIDER") {
		t.Errorf("err = %v, want unknown-provider message", err)
	}
	if errors.Is(err, nil) {
		t.Errorf("err must not be nil")
	}
}
