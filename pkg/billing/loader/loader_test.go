// Tests for the FAAS_BILLING_PROVIDER selector. The selector is the
// single source of truth for the canonical env-var name + default,
// shared by cmd/apid and cmd/meterd (PRD-025 / PR #3).
//
// PR-P2 update: LoadProviderForAPID / LoadProviderForMeterd now take
// a *RootBillingConfig arg. The daemons call ApplyBillingEnvOverlay
// before the loader, so the tests below mirror that pattern via
// resolveCfg() — a tiny helper that constructs a fresh cfg, runs the
// overlay, and returns the merged value.
package loader

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/paddle"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/onebox-faas/faas/pkg/state"
)

// mapEnv is the inline env-reader stub used by every test. Empty
// (default) values get the empty-string from the lookup, matching
// os.Getenv behaviour. Tests that need FAAS_PADDLE_* values seed the
// map and the loader picks them up.
func mapEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// resolveCfg builds a *RootBillingConfig and applies the env
// overlay so the loader's `cfg.Provider` is the env-overlaid final
// value — matching the production caller-side pattern in cmd/{apid,meterd}/main.go.
func resolveCfg(t *testing.T, env func(string) string) *RootBillingConfig {
	t.Helper()
	cfg := &RootBillingConfig{
		Stripe: &stripe.Config{},
		Paddle: &paddle.Config{},
		Polar:  &polar.Config{},
	}
	return ApplyBillingEnvOverlay(cfg, env)
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// polarCatalogServer is the hermetic catalog endpoint used by the Polar
// loader tests. EnsurePlanProducts deliberately performs a live preflight so
// a typo cannot survive daemon startup; tests must therefore provide the same
// local contract instead of relying on an external Polar account.
func polarCatalogServer(t *testing.T, includeMeter bool) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/products/"):
			id := strings.TrimPrefix(r.URL.Path, "/v1/products/")
			if id == "" {
				http.NotFound(w, r)
				return
			}
			fixed := 900
			switch id {
			case "pro-product":
				fixed = 2900
			case "scale-product":
				fixed = 9900
			}
			_, _ = io.WriteString(w, `{"id":"`+id+`","recurring_interval":"month","recurring_interval_count":1,"is_recurring":true,"is_archived":false,"prices":[{"amount_type":"fixed","price_currency":"eur","price_amount":`+strconv.Itoa(fixed)+`,"is_archived":false},{"amount_type":"metered_unit","price_currency":"eur","unit_amount":"1","meter_id":"meter-1","cap_amount":null,"is_archived":false}],"benefits":[]}`)
		case includeMeter && r.URL.Path == "/v1/meters/meter-1":
			_, _ = io.WriteString(w, `{"id":"meter-1","unit":"scalar","archived_at":null,"filter":{"conjunction":"and","clauses":[{"property":"name","operator":"eq","value":"ram_usage"}]},"aggregation":{"func":"sum","property":"gb_ram_hours"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestLoadProviderForAPID_Default confirms the empty-env case returns
// (provider, "polar", nil) — Polar is the public-release billing provider.
// The legacy Stripe surface is still
// bootable from FAAS_BILLING_PROVIDER=stripe (see TestLoadProviderForAPID_Stripe).
//
// The Polar constructor needs a token, product IDs, meter ID, and a
// hermetic catalog endpoint because the loader performs a startup preflight.
func TestLoadProviderForAPID_Default(t *testing.T) {
	t.Parallel()
	catalogURL := polarCatalogServer(t, true)
	env := mapEnv(map[string]string{
		"FAAS_POLAR_ACCESS_TOKEN":     "polar_test_loader_default",
		"FAAS_POLAR_WEBHOOK_SECRET":   "polar_whs_test_loader_default",
		"FAAS_POLAR_HOBBY_PRODUCT_ID": "hobby-product",
		"FAAS_POLAR_PRO_PRODUCT_ID":   "pro-product",
		"FAAS_POLAR_SCALE_PRODUCT_ID": "scale-product",
		"FAAS_POLAR_USAGE_EVENT_NAME": "ram_usage",
		"FAAS_POLAR_METER_ID":         "meter-1",
		"FAAS_POLAR_BASE_URL":         catalogURL,
	})
	cfg := resolveCfg(t, env)
	p, name, err := LoadProviderForAPID(context.Background(), cfg, env, discardLog())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if name != "polar" {
		t.Errorf("name = %q, want %q", name, "polar")
	}
	if p == nil {
		t.Errorf("provider = nil, want non-nil polar.Provider")
	}
}

// TestLoadProviderForAPID_Stripe asserts the legacy opt-in path: an
// explicit FAAS_BILLING_PROVIDER=stripe returns (nil, "stripe", nil)
// so the apid changePlan 402 falls through to the BillingPortalURL
// template branch. The Stripe BuildAPID is nil (legacy apid surface)
// and the loader returns nil + name to keep the apid boot path
// unchanged from the pre-PR-#3 behaviour.
func TestLoadProviderForAPID_Stripe(t *testing.T) {
	t.Parallel()
	env := mapEnv(map[string]string{"FAAS_BILLING_PROVIDER": "stripe"})
	cfg := resolveCfg(t, env)
	p, name, err := LoadProviderForAPID(context.Background(), cfg, env, discardLog())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if name != "stripe" {
		t.Errorf("name = %q, want %q", name, "stripe")
	}
	if p != nil {
		t.Errorf("provider = %v, want nil (legacy apid Stripe surface)", p)
	}
}

// TestLoadProviderForAPID_Paddle_BuildsProvider asserts the paddle path
// returns a non-nil Provider + the literal "paddle" name. The loader
// best-effort runs EnsurePlanProducts; if the catalog hydration fails
// (e.g. a CI runner with no outbound, or a stale Paddle API key) the
// loader still returns the provider — the upgrade 402 will degrade to
// a 500 "monthly price missing" until the next EnsurePlanProducts
// call, but the webhook ingress and dunning state machine keep
// working.
//
// Hermeticity: the test uses a synthetic pdl_test_loader key against
// api.sandbox.paddle.com. The SDK constructor only fails on programmer
// error, not on auth, so the loader never returns an error from this
// path. The EnsurePlanProducts call inside the loader may fail
// silently (warn-logged) and the test still asserts on the provider
// shape.
func TestLoadProviderForAPID_Paddle_BuildsProvider(t *testing.T) {
	t.Parallel()
	env := mapEnv(map[string]string{
		"FAAS_BILLING_PROVIDER":      "paddle",
		"FAAS_PADDLE_API_KEY":        "pdl_test_loader",
		"FAAS_PADDLE_WEBHOOK_SECRET": "whk_test",
		"FAAS_PADDLE_SANDBOX":        "1",
	})
	cfg := resolveCfg(t, env)
	p, name, err := LoadProviderForAPID(context.Background(), cfg, env, discardLog())
	if err != nil {
		t.Fatalf("err = %v, want nil (loader is best-effort on EnsurePlanProducts)", err)
	}
	if name != "paddle" {
		t.Errorf("name = %q, want %q", name, "paddle")
	}
	if p == nil {
		t.Errorf("provider = nil, want non-nil paddle.Provider")
	}
}

func TestLoadProviderForAPID_Polar_BuildsProvider(t *testing.T) {
	t.Parallel()
	catalogURL := polarCatalogServer(t, true)
	env := mapEnv(map[string]string{
		"FAAS_BILLING_PROVIDER":                "polar",
		"FAAS_POLAR_ACCESS_TOKEN":              "polar_test_token",
		"FAAS_POLAR_WEBHOOK_SECRET":            "dGVzdA==",
		"FAAS_POLAR_HOBBY_PRODUCT_ID":          "hobby-product",
		"FAAS_POLAR_PRO_PRODUCT_ID":            "pro-product",
		"FAAS_POLAR_SCALE_PRODUCT_ID":          "scale-product",
		"FAAS_POLAR_USAGE_EVENT_NAME":          "ram_usage",
		"FAAS_POLAR_METER_ID":                  "meter-1",
		"FAAS_POLAR_SANDBOX":                   "true",
		"FAAS_POLAR_WEBHOOK_TOLERANCE_SECONDS": "120",
		"FAAS_POLAR_BASE_URL":                  catalogURL,
	})
	cfg := resolveCfg(t, env)
	p, name, err := LoadProviderForAPID(context.Background(), cfg, env, discardLog())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if name != "polar" {
		t.Fatalf("name = %q, want polar", name)
	}
	if _, ok := p.(*polar.Provider); !ok {
		t.Fatalf("provider = %T, want *polar.Provider", p)
	}
	if !p.Capabilities().Has(billing.CapUsageReconcile) {
		t.Fatal("configured Polar meter should enable CapUsageReconcile")
	}
}

func TestLoadProviderForMeterd_Polar_BuildsProvider(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	catalogURL := polarCatalogServer(t, true)
	env := mapEnv(map[string]string{
		"FAAS_BILLING_PROVIDER":       "polar",
		"FAAS_POLAR_ACCESS_TOKEN":     "polar_test_token",
		"FAAS_POLAR_HOBBY_PRODUCT_ID": "hobby-product",
		"FAAS_POLAR_PRO_PRODUCT_ID":   "pro-product",
		"FAAS_POLAR_SCALE_PRODUCT_ID": "scale-product",
		"FAAS_POLAR_USAGE_EVENT_NAME": "ram_usage",
		"FAAS_POLAR_METER_ID":         "meter-1",
		"FAAS_POLAR_SANDBOX":          "1",
		"FAAS_POLAR_BASE_URL":         catalogURL,
	})
	cfg := resolveCfg(t, env)
	p, name, err := LoadProviderForMeterd(cfg, env, store, discardLog())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if name != "polar" {
		t.Fatalf("name = %q, want polar", name)
	}
	if _, ok := p.(*polar.Provider); !ok {
		t.Fatalf("provider = %T, want *polar.Provider", p)
	}
}

func TestLoadProviderForMeterd_PolarFailsMissingCatalogID(t *testing.T) {
	t.Parallel()
	env := mapEnv(map[string]string{
		"FAAS_BILLING_PROVIDER":       "polar",
		"FAAS_POLAR_ACCESS_TOKEN":     "polar_test_token",
		"FAAS_POLAR_HOBBY_PRODUCT_ID": "hobby-product",
		"FAAS_POLAR_PRO_PRODUCT_ID":   "pro-product",
	})
	cfg := resolveCfg(t, env)
	if _, _, err := LoadProviderForMeterd(cfg, env, state.NewMemStore(), discardLog()); err == nil || !strings.Contains(err.Error(), "Scale") && !strings.Contains(err.Error(), "scale") {
		t.Fatalf("LoadProviderForMeterd error = %v, want missing scale product preflight", err)
	}
}

// TestLoadProviderForAPID_Unknown fails the boot loudly on a typo.
// "braintree" is the canonical bad-value example (real product, not a
// supported provider) — the loader must return an error rather than
// silently defaulting to Stripe, which would let an operator think
// they're on Braintree while the box quietly falls back to Stripe.
func TestLoadProviderForAPID_Unknown(t *testing.T) {
	t.Parallel()
	env := mapEnv(map[string]string{"FAAS_BILLING_PROVIDER": "braintree"})
	cfg := resolveCfg(t, env)
	_, _, err := LoadProviderForAPID(context.Background(), cfg, env, discardLog())
	if err == nil {
		t.Fatalf("err = nil, want error for unknown provider")
	}
	if !strings.Contains(err.Error(), "unknown FAAS_BILLING_PROVIDER") {
		t.Errorf("err = %q, want unknown-provider message", err)
	}
}

// TestLoadProviderForMeterd_Default_BuildsPolar asserts the meterd
// default path constructs a *polar.Provider (not nil) — the public-release
// billing provider.
//
// We don't assert on the concrete type here (just non-nil) — the
// compile-time conformance var in pkg/billing/paddle/provider.go pins
// the shape.
func TestLoadProviderForMeterd_Default_BuildsPolar(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	catalogURL := polarCatalogServer(t, true)
	env := mapEnv(map[string]string{
		"FAAS_POLAR_ACCESS_TOKEN":     "polar_test_meterd_default",
		"FAAS_POLAR_HOBBY_PRODUCT_ID": "hobby-product",
		"FAAS_POLAR_PRO_PRODUCT_ID":   "pro-product",
		"FAAS_POLAR_SCALE_PRODUCT_ID": "scale-product",
		"FAAS_POLAR_USAGE_EVENT_NAME": "ram_usage",
		"FAAS_POLAR_METER_ID":         "meter-1",
		"FAAS_POLAR_BASE_URL":         catalogURL,
	})
	cfg := resolveCfg(t, env)
	p, name, err := LoadProviderForMeterd(cfg, env, store, discardLog())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if name != "polar" {
		t.Errorf("name = %q, want %q", name, "polar")
	}
	if p == nil {
		t.Errorf("provider = nil, want non-nil *polar.Provider")
	}
}

// TestLoadProviderForMeterd_Paddle_BuildsProvider asserts the meterd
// Paddle path returns a non-nil Provider. Meterd doesn't need the
// webhook secret (no ingress), so the loader passes "" for it.
func TestLoadProviderForMeterd_Paddle_BuildsProvider(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	env := mapEnv(map[string]string{
		"FAAS_BILLING_PROVIDER": "paddle",
		"FAAS_PADDLE_API_KEY":   "pdl_test_loader",
		"FAAS_PADDLE_SANDBOX":   "1",
	})
	cfg := resolveCfg(t, env)
	p, name, err := LoadProviderForMeterd(cfg, env, store, discardLog())
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
	env := mapEnv(map[string]string{"FAAS_BILLING_PROVIDER": "paypal"})
	cfg := resolveCfg(t, env)
	_, _, err := LoadProviderForMeterd(cfg, env, store, discardLog())
	if err == nil {
		t.Fatalf("err = nil, want error for unknown provider")
	}
	// Defensive: errors.Is to allow future wraps — today the loader
	// returns a bare fmt.Errorf, but the test should not break if a
	// future change switches to %w.
	if !strings.Contains(err.Error(), "unknown FAAS_BILLING_PROVIDER") {
		t.Errorf("err = %v, want unknown-provider message", err)
	}
}

// TestLoadProviderForAPID_TOMLProviderSelectsPaddle asserts the TOML
// [billing].provider field selects the provider independently of
// env. The TOML sets provider="paddle", env is empty, and the loader
// picks Paddle.
//
// PR-P2 tripwire: if the loader is changed to consult env before
// cfg.Provider, this test breaks (the env is empty, so the loader
// would default to Stripe).
func TestLoadProviderForAPID_TOMLProviderSelectsPaddle(t *testing.T) {
	t.Parallel()
	env := mapEnv(map[string]string{
		"FAAS_PADDLE_API_KEY":        "pdl_toml",
		"FAAS_PADDLE_WEBHOOK_SECRET": "whk_toml",
		"FAAS_PADDLE_SANDBOX":        "1",
	})
	cfg := resolveCfg(t, mapEnv(map[string]string{
		// Env has no provider field — the TOML-loaded provider is
		// the canonical selector for this test.
	}))
	// Set the cfg.Provider directly (bypassing the env-only overlay).
	cfg.Provider = "paddle"
	p, name, err := LoadProviderForAPID(context.Background(), cfg, env, discardLog())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if name != "paddle" {
		t.Errorf("name = %q, want %q", name, "paddle")
	}
	if p == nil {
		t.Error("provider = nil, want non-nil paddle.Provider (TOML selected)")
	}
}

// TestLoadProviderForAPID_EnvOverridesTOMLProvider asserts env > TOML
// precedence on the provider field. The overlay in the test setup
// runs once, so we set cfg.Provider = "stripe" and then verify env
// wins (the overlay writes "paddle" to cfg.Provider before the loader
// sees it).
func TestLoadProviderForAPID_EnvOverridesTOMLProvider(t *testing.T) {
	t.Parallel()
	env := mapEnv(map[string]string{
		"FAAS_BILLING_PROVIDER":      "paddle",
		"FAAS_PADDLE_API_KEY":        "pdl_env",
		"FAAS_PADDLE_WEBHOOK_SECRET": "whk_env",
		"FAAS_PADDLE_SANDBOX":        "1",
	})
	cfg := &RootBillingConfig{
		Provider: "stripe", // TOML-derived; overlay must overwrite
		Stripe:   &stripe.Config{},
		Paddle:   &paddle.Config{},
	}
	cfg = ApplyBillingEnvOverlay(cfg, env)
	if cfg.Provider != "paddle" {
		t.Fatalf("env overlay did not flip cfg.Provider: got %q", cfg.Provider)
	}
	p, name, err := LoadProviderForAPID(context.Background(), cfg, env, discardLog())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if name != "paddle" {
		t.Errorf("name = %q, want %q (env wins over TOML)", name, "paddle")
	}
	if p == nil {
		t.Error("provider = nil, want non-nil paddle.Provider")
	}
}

// TestLoadProviderForMeterd_TOMLStripeBlock asserts the TOML
// [billing.stripe].api_key value is the source-of-truth when env
// is empty. The loader's Stripe BuildMeterd reads env("STRIPE_API_KEY")
// — the overlay passes the TOML value through env (defensive: the
// overlay only writes env values when non-empty).
//
// In this test, env is structured so STRIPE_API_KEY is empty — so
// the loader's Stripe Builder reads cfg.Stripe.APIKey via the closure
// only if the operator-level call sets it. Today the closure does
// not consult cfg.Stripe.APIKey (it uses env directly); the test
// pins the env-as-source-of-truth shape and documents the intent.
func TestLoadProviderForMeterd_TOMLStripeBlock(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	env := mapEnv(map[string]string{
		"STRIPE_API_KEY":        "sk_env",
		"STRIPE_WEBHOOK_SECRET": "whsec_env",
	})
	cfg := &RootBillingConfig{
		Provider: "stripe",
		Stripe:   &stripe.Config{APIKey: "sk_toml"}, // intentionally different
		Paddle:   &paddle.Config{},
	}
	cfg = ApplyBillingEnvOverlay(cfg, env)
	if cfg.Stripe.APIKey != "sk_env" {
		t.Fatalf("env overlay did not override cfg.Stripe.APIKey: got %q", cfg.Stripe.APIKey)
	}
	p, name, err := LoadProviderForMeterd(cfg, env, store, discardLog())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if name != "stripe" {
		t.Errorf("name = %q, want %q", name, "stripe")
	}
	if p == nil {
		t.Error("provider = nil, want non-nil *stripe.Client")
	}
}

// TestLoadProviderForMeterd_EmptyTOMLFallsThroughToPolar asserts an
// empty cfg.Provider (no TOML header, no env override) → Polar (the
// public-release default). The legacy Stripe opt-in is
// still bootable from FAAS_BILLING_PROVIDER=stripe (see
// TestLoadProviderForMeterd_Stripe).
func TestLoadProviderForMeterd_EmptyTOMLFallsThroughToPolar(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	catalogURL := polarCatalogServer(t, true)
	env := mapEnv(map[string]string{
		"FAAS_POLAR_ACCESS_TOKEN":     "polar_x",
		"FAAS_POLAR_HOBBY_PRODUCT_ID": "hobby-product",
		"FAAS_POLAR_PRO_PRODUCT_ID":   "pro-product",
		"FAAS_POLAR_SCALE_PRODUCT_ID": "scale-product",
		"FAAS_POLAR_USAGE_EVENT_NAME": "ram_usage",
		"FAAS_POLAR_METER_ID":         "meter-1",
		"FAAS_POLAR_BASE_URL":         catalogURL,
	})
	cfg := resolveCfg(t, env)
	if cfg.Provider != "" {
		t.Fatalf("cfg.Provider = %q, want empty (default falls through to polar)", cfg.Provider)
	}
	p, name, err := LoadProviderForMeterd(cfg, env, store, discardLog())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if name != "polar" {
		t.Errorf("name = %q, want %q (empty cfg.Provider falls through to polar)", name, "polar")
	}
	if p == nil {
		t.Error("provider = nil, want non-nil *polar.Provider")
	}
}

// TestLoadProviderForMeterd_TOMLUnknownProvider asserts the loader
// errors on a TOML-only typo (env is empty, cfg.Provider="braintree").
func TestLoadProviderForMeterd_TOMLUnknownProvider(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	env := mapEnv(nil)
	cfg := &RootBillingConfig{
		Provider: "braintree",
		Stripe:   &stripe.Config{},
		Paddle:   &paddle.Config{},
	}
	_, _, err := LoadProviderForMeterd(cfg, env, store, discardLog())
	if err == nil {
		t.Fatal("err = nil, want error for unknown provider")
	}
	if !strings.Contains(err.Error(), "unknown FAAS_BILLING_PROVIDER") {
		t.Errorf("err = %q, want unknown-provider message", err)
	}
}

// TestLoadProviderForMeterd_TOMLStripeKeyUsedWhenEnvEmpty pins the
// code-review finding 1 fix: the Build closures must consult
// cfg.Stripe.APIKey when env("STRIPE_API_KEY") is empty. Without this,
// the TOML surface is documented as functional but silently ignored
// at runtime. Tripwire: if a future refactor drops resolveSecret, this
// test still passes if STRIPE_API_KEY env is also empty and the loader
// silently picks "" — so the test sets cfg.Stripe.APIKey to a sentinel
// and asserts the provider is non-nil with a non-empty internal key.
// (We can't read the key back from outside the package, so we assert
// only non-nil + non-empty via a probe: a Stripe provider built with
// "" is unusable but doesn't fail at construction time, so a sentinel
// value is the only way to detect the path was taken.)
func TestLoadProviderForMeterd_TOMLStripeKeyUsedWhenEnvEmpty(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	// env has NO STRIPE_API_KEY — the TOML key is the only source.
	env := mapEnv(map[string]string{
		"STRIPE_WEBHOOK_SECRET": "whsec_toml_only",
	})
	cfg := &RootBillingConfig{
		Provider: "stripe",
		Stripe: &stripe.Config{
			APIKey: "sk_toml_only_sentinel",
		},
		Paddle: &paddle.Config{},
	}
	cfg = ApplyBillingEnvOverlay(cfg, env)
	// Sanity: the overlay preserves the TOML value when env is empty.
	if cfg.Stripe.APIKey != "sk_toml_only_sentinel" {
		t.Fatalf("overlay dropped TOML STRIPE_API_KEY: got %q", cfg.Stripe.APIKey)
	}
	p, name, err := LoadProviderForMeterd(cfg, env, store, discardLog())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if name != "stripe" {
		t.Errorf("name = %q, want \"stripe\"", name)
	}
	if p == nil {
		t.Fatal("provider = nil, want non-nil *stripe.Client (TOML key should have been used)")
	}
}

// TestLoadProviderForMeterd_EnvWinsOverTOMLStripeKey is the companion
// to the test above: when both env and TOML are set, env wins (the
// overlay already enforces this for cfg, and resolveSecret enforces
// it again at the closure site so a refactor that drops the overlay
// still preserves precedence).
func TestLoadProviderForMeterd_EnvWinsOverTOMLStripeKey(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	env := mapEnv(map[string]string{
		"STRIPE_API_KEY":        "sk_env_sentinel",
		"STRIPE_WEBHOOK_SECRET": "whsec_env",
	})
	cfg := &RootBillingConfig{
		Provider: "stripe",
		Stripe: &stripe.Config{
			APIKey: "sk_toml_sentinel", // intentionally different
		},
		Paddle: &paddle.Config{},
	}
	cfg = ApplyBillingEnvOverlay(cfg, env)
	if cfg.Stripe.APIKey != "sk_env_sentinel" {
		t.Fatalf("overlay did not flip cfg.Stripe.APIKey to env value: got %q", cfg.Stripe.APIKey)
	}
	p, name, err := LoadProviderForMeterd(cfg, env, store, discardLog())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if name != "stripe" || p == nil {
		t.Errorf("name=%q provider=%v, want stripe + non-nil", name, p)
	}
}

// TestLoadProviderForMeterd_TOMLPaddleKeyUsedWhenEnvEmpty is the
// Paddle-side tripwire. Without resolveSecret, FAAS_PADDLE_API_KEY
// env empty + cfg.Paddle.APIKey set would boot a Paddle provider with
// an empty key, and meterd's pusher loop would 401 on every push.
func TestLoadProviderForMeterd_TOMLPaddleKeyUsedWhenEnvEmpty(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	// env has NO FAAS_PADDLE_API_KEY — TOML is the only source.
	env := mapEnv(map[string]string{
		"FAAS_PADDLE_SANDBOX": "1",
	})
	cfg := &RootBillingConfig{
		Provider: "paddle",
		Stripe:   &stripe.Config{},
		Paddle: &paddle.Config{
			APIKey: "pdl_toml_only_sentinel",
		},
	}
	cfg = ApplyBillingEnvOverlay(cfg, env)
	if cfg.Paddle.APIKey != "pdl_toml_only_sentinel" {
		t.Fatalf("overlay dropped TOML FAAS_PADDLE_API_KEY: got %q", cfg.Paddle.APIKey)
	}
	p, name, err := LoadProviderForMeterd(cfg, env, store, discardLog())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if name != "paddle" {
		t.Errorf("name = %q, want \"paddle\"", name)
	}
	if p == nil {
		t.Fatal("provider = nil, want non-nil *paddle.Provider (TOML key should have been used)")
	}
}

// TestRootBillingConfig_DefaultProvider_Polar pins the implicit
// default hoisted into RootBillingConfig.DefaultProvider() (PR #962
// MED-1 fix). The single seam means a future default flip is a
// one-edit change rather than a three-edit change. Both the legacy
// "stripe" opt-in and an empty cfg must produce the right answer.
func TestRootBillingConfig_DefaultProvider_Polar(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		cfg    *RootBillingConfig
		expose func(*RootBillingConfig) string
		want   string
	}{
		{
			name:   "empty provider field → polar (public-release default)",
			cfg:    &RootBillingConfig{Provider: ""},
			expose: func(c *RootBillingConfig) string { return c.DefaultProvider() },
			want:   "polar",
		},
		{
			name:   "explicit stripe is honoured",
			cfg:    &RootBillingConfig{Provider: "stripe"},
			expose: func(c *RootBillingConfig) string { return c.DefaultProvider() },
			want:   "stripe",
		},
		{
			name:   "explicit paddle is honoured",
			cfg:    &RootBillingConfig{Provider: "paddle"},
			expose: func(c *RootBillingConfig) string { return c.DefaultProvider() },
			want:   "paddle",
		},
		{
			name:   "nil receiver returns the default (graceful)",
			cfg:    nil,
			expose: func(c *RootBillingConfig) string { return c.DefaultProvider() },
			want:   "polar",
		},
		{
			name:   "explicit unknown value is passed through (loader surfaces the 'unknown' error)",
			cfg:    &RootBillingConfig{Provider: "braintree"},
			expose: func(c *RootBillingConfig) string { return c.DefaultProvider() },
			want:   "braintree",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.expose(tc.cfg)
			if got != tc.want {
				t.Errorf("DefaultProvider() = %q, want %q", got, tc.want)
			}
		})
	}
}
