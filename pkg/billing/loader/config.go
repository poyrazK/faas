package loader

import (
	"fmt"
	"os"
	"strconv"

	"github.com/BurntSushi/toml"

	"github.com/onebox-faas/faas/pkg/billing/paddle"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/billing/stripe"
)

// paddleSandboxTrue1 + paddleSandboxTrueWord are the two accepted
// env-var truthy spellings for FAAS_PADDLE_SANDBOX. Constants let the
// goconst linter fold the duplicate string literals into a single
// declaration; the closure also calls these in the BuildPaddle closure
// at loader.go so a future string-typo rename trips one tripwire.
const (
	paddleSandboxTrue1    = "1"
	paddleSandboxTrueWord = "true"
)

// RootBillingConfig is the [billing] block in apid.toml / meterd.toml.
// Both daemons load the same shape so the operator-facing config
// surface is identical across control-plane daemons — the loader
// applies the env overlay after LoadBillingConfig returns so env
// wins over TOML (matching the cmd/meterd/main.go:687 applyEnvTick
// precedent).
//
// Pre-PR-P2 the provider switch was env-only (FAAS_BILLING_PROVIDER).
// PR-P2 introduces the TOML surface for typed provider config while
// keeping env as the documentation-grade override for containers
// (where a TOML file is awkward) and the systemd EnvironmentFile=
// path (where the operator pulls secret values from sealed.env).
//
// This type lives in pkg/billing/loader (not pkg/billing) because
// pkg/billing/billing_config.go would import pkg/billing/paddle
// for *paddle.Config, and pkg/billing/paddle/products.go imports
// pkg/billing — that's a hard cycle. The loader package sits
// "below" pkg/billing: it imports paddle/stripe and the per-provider
// init.go files import the loader back, which is fine because the
// loader doesn't import pkg/billing.
type RootBillingConfig struct {
	// Provider selects the active provider. Empty defaults to
	// "polar" (the production billing provider for the public
	// release). Use cfg.DefaultProvider() to read with the
	// implicit default applied. Valid values: "stripe", "paddle",
	// "polar".
	// Unknown values fail the daemon boot with the same error
	// message the loader would have raised on a typo'd env var.
	Provider string `toml:"provider"`

	// Stripe is the [billing.stripe] block. nil when no
	// [billing.stripe] table is present in TOML — the loader
	// treats that as "all defaults" and applies the env overlay
	// after Defaults() runs.
	Stripe *stripe.Config `toml:"stripe"`

	// Paddle is the [billing.paddle] block. Same nil semantics
	// as Stripe.
	Paddle *paddle.Config `toml:"paddle"`

	// Polar is the [billing.polar] block. Product IDs refer to
	// recurring products configured in the Polar dashboard.
	Polar *polar.Config `toml:"polar"`
}

// DefaultProvider returns the active provider literal with the
// implicit-default applied (public release = "polar"). LoadProviderForAPID
// and LoadProviderForMeterd both go through this method so the
// default lives in exactly one place. A future ADR that flips the
// default (e.g. LemonSqueezy) updates this method + the test pin
// at TestRootBillingConfig_DefaultProvider_Polar, and the two
// loader sites change mechanically.
//
// The legacy "stripe" opt-in (FAAS_BILLING_PROVIDER=stripe) is
// unaffected: an explicit value still wins; this method only fires
// when Provider == "".
func (c *RootBillingConfig) DefaultProvider() string {
	if c == nil || c.Provider == "" {
		return providerPolar
	}
	return c.Provider
}

// BillingFile wraps the [billing] sub-block. Used by the daemon's
// LoadConfig to extract the billing sub-table via the BillingFile
// wrapper pattern. The daemon's Config struct embeds `Billing
// *loader.RootBillingConfig` (or a typed wrapper) to participate
// in the decode.
type BillingFile struct {
	// Billing is the [billing] sub-block. nil when the daemon's
	// TOML has no [billing] header (the legacy env-only path).
	Billing *RootBillingConfig `toml:"billing"`
}

// LoadBillingConfig decodes a raw TOML body and returns the populated
// [billing] sub-block. Missing [billing] header is not an error —
// defaults are returned. Bad TOML is a wrapped error.
//
// This is the primary entry point used by cmd/apid and cmd/meterd —
// each daemon's LoadConfig calls LoadBillingConfig on the file body
// it just read. The function takes bytes (not a path) so the daemon
// can read the file once and extract both its own fields and the
// [billing] sub-block without re-reading the file.
func LoadBillingConfig(tomlBody []byte) (*RootBillingConfig, error) {
	if len(tomlBody) == 0 {
		// Empty body (missing file or zero-length file) → defaults.
		cfg := &RootBillingConfig{
			Stripe: &stripe.Config{},
			Paddle: &paddle.Config{},
			Polar:  &polar.Config{},
		}
		cfg.Stripe.Defaults()
		cfg.Paddle.Defaults()
		cfg.Polar.Defaults()
		return cfg, nil
	}
	wrapper := &BillingFile{}
	if _, err := toml.Decode(string(tomlBody), wrapper); err != nil {
		return nil, fmt.Errorf("billing loader: parse TOML: %w", err)
	}
	if wrapper.Billing == nil {
		// No [billing] header in the file → defaults (env-only legacy).
		wrapper.Billing = &RootBillingConfig{
			Stripe: &stripe.Config{},
			Paddle: &paddle.Config{},
			Polar:  &polar.Config{},
		}
	}
	if wrapper.Billing.Stripe == nil {
		wrapper.Billing.Stripe = &stripe.Config{}
	}
	if wrapper.Billing.Paddle == nil {
		wrapper.Billing.Paddle = &paddle.Config{}
	}
	if wrapper.Billing.Polar == nil {
		wrapper.Billing.Polar = &polar.Config{}
	}
	wrapper.Billing.Stripe.Defaults()
	wrapper.Billing.Paddle.Defaults()
	wrapper.Billing.Polar.Defaults()
	return wrapper.Billing, nil
}

// LoadBillingConfigFromPath is a convenience wrapper for callers
// that don't already have the file body in memory. Reads the file
// at path, then defers to LoadBillingConfig(body). Missing file
// returns defaults (matches cmd/meterd/config.go:69-87 precedent).
func LoadBillingConfigFromPath(path string) (*RootBillingConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LoadBillingConfig(nil)
		}
		return nil, fmt.Errorf("billing loader: read %q: %w", path, err)
	}
	return LoadBillingConfig(b)
}

// ApplyBillingEnvOverlay overwrites TOML-loaded fields with env
// values where set. Env wins over TOML. The set of env vars is the
// union of every provider's EnvVars surface (Stripe, Paddle, and Polar) so a
// Stripe-only deploy doesn't need Paddle env vars and vice versa.
//
// Pattern matches cmd/meterd/main.go:687-693 applyEnvTick — same
// "env > TOML > Defaults" precedence. The helper is exported so
// both cmd/apid and cmd/meterd share one implementation.
func ApplyBillingEnvOverlay(cfg *RootBillingConfig, env func(string) string) *RootBillingConfig {
	if cfg == nil {
		return nil
	}
	if cfg.Stripe == nil {
		cfg.Stripe = &stripe.Config{}
	}
	if cfg.Paddle == nil {
		cfg.Paddle = &paddle.Config{}
	}
	if cfg.Polar == nil {
		cfg.Polar = &polar.Config{}
	}
	if v := env("STRIPE_API_KEY"); v != "" {
		cfg.Stripe.APIKey = v
	}
	if v := env("STRIPE_WEBHOOK_SECRET"); v != "" {
		cfg.Stripe.WebhookSecret = v
	}
	// Tolerance is TOML-only in PR-P2 — adding a STRIPE_TOLERANCE
	// env var is out of scope (the 5-minute default covers every
	// documented Stripe integration).
	if v := env("FAAS_PADDLE_API_KEY"); v != "" {
		cfg.Paddle.APIKey = v
	}
	if v := env("FAAS_PADDLE_WEBHOOK_SECRET"); v != "" {
		cfg.Paddle.WebhookSecret = v
	}
	if v := env("FAAS_PADDLE_SANDBOX"); v != "" {
		cfg.Paddle.Sandbox = v == paddleSandboxTrue1 || v == paddleSandboxTrueWord
	}
	// PR-P4 — FAAS_PADDLE_WEBHOOK_TOLERANCE_SECONDS exposes the
	// replay-protection window as an operator knob (sandbox VMs with
	// bad NTP need this). Mirrors stripe.Config.ToleranceSeconds.
	// Bad parse (non-integer) falls through silently — the verifier
	// clamps <= 0 to webhookDefaultTolerance, so a stale TOML value
	// is safer than a noisy Warn on every boot. envTick in
	// cmd/meterd/main.go is the operator-side diagnostic surface
	// for tolerance drift.
	if v := env("FAAS_PADDLE_WEBHOOK_TOLERANCE_SECONDS"); v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil {
			cfg.Paddle.ToleranceSeconds = n
		}
	}
	if v := env("FAAS_POLAR_ACCESS_TOKEN"); v != "" {
		cfg.Polar.APIKey = v
	} else if v := env("FAAS_POLAR_API_KEY"); v != "" {
		// Compatibility alias for operators who prefer the generic
		// API-key vocabulary used by the other providers.
		cfg.Polar.APIKey = v
	}
	if v := env("FAAS_POLAR_WEBHOOK_SECRET"); v != "" {
		cfg.Polar.WebhookSecret = v
	}
	if v := env("FAAS_POLAR_SANDBOX"); v != "" {
		cfg.Polar.Sandbox = v == paddleSandboxTrue1 || v == paddleSandboxTrueWord
	}
	if v := env("FAAS_POLAR_WEBHOOK_TOLERANCE_SECONDS"); v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil {
			cfg.Polar.ToleranceSeconds = n
		}
	}
	if v := env("FAAS_POLAR_HOBBY_PRODUCT_ID"); v != "" {
		cfg.Polar.HobbyProductID = v
	}
	if v := env("FAAS_POLAR_PRO_PRODUCT_ID"); v != "" {
		cfg.Polar.ProProductID = v
	}
	if v := env("FAAS_POLAR_SCALE_PRODUCT_ID"); v != "" {
		cfg.Polar.ScaleProductID = v
	}
	if v := env("FAAS_POLAR_USAGE_EVENT_NAME"); v != "" {
		cfg.Polar.UsageEventName = v
	}
	if v := env("FAAS_POLAR_METER_ID"); v != "" {
		cfg.Polar.MeterID = v
	}
	if v := env("FAAS_POLAR_SUCCESS_URL"); v != "" {
		cfg.Polar.SuccessURL = v
	}
	if v := env("FAAS_POLAR_RETURN_URL"); v != "" {
		cfg.Polar.ReturnURL = v
	}
	if v := env("FAAS_POLAR_BASE_URL"); v != "" {
		cfg.Polar.BaseURL = v
	}
	// The [billing].provider field is also env-overridable: an
	// operator who sets FAAS_BILLING_PROVIDER in the systemd
	// EnvironmentFile= overrides any TOML [billing].provider value.
	// This matches the pre-PR-P2 env-only behavior so the migration
	// is transparent — operators who set the env var today keep
	// working after PR-P2 lands.
	if v := env("FAAS_BILLING_PROVIDER"); v != "" {
		cfg.Provider = v
	}
	cfg.Stripe.Defaults()
	cfg.Paddle.Defaults()
	cfg.Polar.Defaults()
	return cfg
}
