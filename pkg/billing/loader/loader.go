// Package loader is the single source of truth for the
// FAAS_BILLING_PROVIDER selector. Both cmd/apid and cmd/meterd
// import this so the canonical-name list, the env-var name, and the
// default cannot drift between daemons.
//
// Lives in its own sub-package (not pkg/billing) because pkg/billing
// is imported by pkg/billing/{paddle,stripe} and the loader imports
// those — a same-package location would cycle. PR-P2 keeps that
// direction-of-imports: the loader imports the providers for the
// constructors, and the per-provider packages don't import the loader.
//
// Two functions (not one) because the Stripe constructor needs
// state.Store (for the meterd PushDedupe surface) and not for the
// apid path:
//
//   - LoadProviderForAPID returns a paddle.Provider when the active
//     provider is Paddle (and runs EnsurePlanProducts so the catalog
//     is populated before the first webhook lands); nil + "stripe"
//     otherwise. The apid Stripe path stays inline (cmd/apid/server.go
//     reads FAAS_BILLING_PORTAL_URL + STRIPE_WEBHOOK_SECRET directly,
//     since apid doesn't need to construct a *stripe.Client — only the
//     webhook signature check + the billing-portal template URL).
//
//   - LoadProviderForMeterd returns a Provider for the meterd pusher
//     loop. For Stripe, the builder constructs a *stripe.Client with
//     the supplied state.Store as the PushDedupe. For Paddle, the
//     builder constructs a *paddle.Provider; meterd doesn't need the
//     webhook secret (no ingress in meterd).
//
//     An unknown FAAS_BILLING_PROVIDER value returns an error so a
//     typo ("braintree", "paypal") fails the daemon boot loudly
//     rather than silently defaulting to Stripe.
//
// Env vars consumed (all optional except per the per-branch docs):
//
//	FAAS_BILLING_PROVIDER   "" | "stripe" | "paddle" | "polar"   default "polar"
//	STRIPE_API_KEY          required when Stripe is the active provider (apid + meterd)
//	STRIPE_WEBHOOK_SECRET   required when Stripe is the active provider (apid only)
//	FAAS_PADDLE_API_KEY     required when Paddle is the active provider (apid + meterd)
//	FAAS_PADDLE_WEBHOOK_SECRET  required when Paddle is the active provider (apid only)
//	FAAS_PADDLE_SANDBOX     "1" / "true" to use api.sandbox.paddle.com (apid + meterd)
//	FAAS_POLAR_ACCESS_TOKEN required when Polar is active (apid + meterd)
//	FAAS_POLAR_WEBHOOK_SECRET required when Polar is active (apid only)
//	FAAS_POLAR_SANDBOX      "1" / "true" to use sandbox-api.polar.sh (apid + meterd)
//	FAAS_POLAR_METER_ID    required when Polar is active; usage meter + reconciliation
//	FAAS_POLAR_BASE_URL    optional; private API proxy / contract-test endpoint
//
// TOML config precedence: env > TOML > Defaults. The daemon's
// LoadConfig reads the [billing] block from its TOML file via
// LoadBillingConfig, then ApplyBillingEnvOverlay writes env values
// over the TOML-loaded fields. The loader functions below operate
// on the merged cfg + raw env().
package loader

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/paddle"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/onebox-faas/faas/pkg/state"
)

// Provider name literals (FAAS_BILLING_PROVIDER). Hoisted to constants
// so goconst sees one definition per value across LoadProviderForAPID
// + LoadProviderForMeterd + the README + the env-var docs string.
const (
	providerStripe = "stripe"
	providerPaddle = "paddle"
	providerPolar  = "polar"
	// Keep optional remote catalog hydration from blocking the API listener
	// indefinitely when the billing provider or internet path is unavailable.
	// The provider remains wired and can retry on its normal request path.
	providerBootHydrationTimeout = 10 * time.Second
)

// resolveSecret implements the env > TOML precedence for an individual
// secret. ApplyBillingEnvOverlay populates the cfg surface with the
// env value when non-empty, but the per-provider closures below are
// the consumer side — they need to pick the env value when the caller
// passed it, fall back to the TOML value when env is empty. This is
// not a second overlay; it's the read side of the same precedence.
//
// Why a helper instead of re-reading cfg? The cfg pointer is the
// merged value after ApplyBillingEnvOverlay, so a closure looking at
// cfg.Stripe.APIKey cannot tell whether the value came from env or
// TOML — and the production call site passes the *raw* env reader
// (the caller already applied the overlay). Using two parallel inputs
// (env + cfg) keeps the precedence explicit at the closure site.
func resolveSecret(envVal, tomlVal string) string {
	if envVal != "" {
		return envVal
	}
	return tomlVal
}

// resolveSandbox implements the env > TOML precedence for the
// FAAS_PADDLE_SANDBOX knob. Env spelling accepts "1" / "true" /
// "0" / "false" / "" (empty = no override, take TOML). TOML is a
// plain bool (Defaults() leaves it false). The boolean identity is
// preserved across the merge so a true TOML value remains true when
// env is unset.
func resolveSandbox(envVal string, tomlVal bool) bool {
	switch envVal {
	case paddleSandboxTrue1, paddleSandboxTrueWord:
		return true
	case "0", "false":
		return false
	default:
		return tomlVal
	}
}

func resolvedPolarConfig(cfg *RootBillingConfig, env func(string) string) polar.Config {
	var out polar.Config
	if cfg != nil && cfg.Polar != nil {
		out = *cfg.Polar
	}
	if v := env("FAAS_POLAR_ACCESS_TOKEN"); v != "" {
		out.APIKey = v
	} else if v := env("FAAS_POLAR_API_KEY"); v != "" {
		out.APIKey = v
	}
	if v := env("FAAS_POLAR_WEBHOOK_SECRET"); v != "" {
		out.WebhookSecret = v
	}
	if v := env("FAAS_POLAR_SANDBOX"); v != "" {
		out.Sandbox = resolveSandbox(v, out.Sandbox)
	}
	if v := env("FAAS_POLAR_WEBHOOK_TOLERANCE_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			out.ToleranceSeconds = n
		}
	}
	if v := env("FAAS_POLAR_HOBBY_PRODUCT_ID"); v != "" {
		out.HobbyProductID = v
	}
	if v := env("FAAS_POLAR_PRO_PRODUCT_ID"); v != "" {
		out.ProProductID = v
	}
	if v := env("FAAS_POLAR_SCALE_PRODUCT_ID"); v != "" {
		out.ScaleProductID = v
	}
	if v := env("FAAS_POLAR_USAGE_EVENT_NAME"); v != "" {
		out.UsageEventName = v
	}
	if v := env("FAAS_POLAR_METER_ID"); v != "" {
		out.MeterID = v
	}
	if v := env("FAAS_POLAR_SUCCESS_URL"); v != "" {
		out.SuccessURL = v
	}
	if v := env("FAAS_POLAR_RETURN_URL"); v != "" {
		out.ReturnURL = v
	}
	if v := env("FAAS_POLAR_BASE_URL"); v != "" {
		out.BaseURL = v
	}
	out.Defaults()
	return out
}

// ProviderMeta describes one registered billing provider for the
// admin/CLI surface (GET /v1/admin/billing-provider, `faas billing
// status`). The PR-P2 extension adds BuildAPID / BuildMeterd closures
// so the loader can dispatch to the right provider without a switch.
//
// The Build closures return `any` (the concrete provider type) so
// the per-provider package doesn't need to import pkg/billing — the
// loader type-asserts the result to billing.Provider before returning
// to the caller. The compile-time assertion in each provider package
// (e.g. `var _ billing.Provider = (*Client)(nil)`) guarantees the
// concrete type satisfies the interface.
type ProviderMeta struct {
	// Name is the FAAS_BILLING_PROVIDER literal ("stripe", "paddle").
	// Stable; the canonical-name list.
	Name string
	// Capabilities is the bitset the Provider reports.
	Capabilities billing.CapabilitySet
	// EnvVars lists the env-var names the provider reads at boot.
	// Surfaced for operator documentation; the runtime semantics
	// live in the provider's config.go.
	EnvVars []string
	// BuildAPID constructs the provider for apid's webhook ingress
	// + changePlan handler. nil is the "apid doesn't need a
	// Provider instance" case (the legacy apid Stripe path).
	//
	// The closure receives the env-overlaid cfg so it can resolve
	// secrets via env > TOML precedence (resolveSecret helper). The
	// raw env reader is also passed so callers that pre-apply the
	// overlay can still distinguish env-originated vs TOML-originated
	// values.
	BuildAPID func(cfg *RootBillingConfig, env func(string) string, log *slog.Logger) (any, error)
	// BuildMeterd constructs the provider for meterd's pusher loop.
	// Always non-nil for registered providers — meterd requires a
	// Provider on every path. The cfg is the env-overlaid merge;
	// the env reader is the raw source. Same env > TOML precedence
	// pattern as BuildAPID.
	BuildMeterd func(cfg *RootBillingConfig, env func(string) string, store state.Store, log *slog.Logger) (any, error)
}

// Providers returns the canonical list of registered billing providers.
// The list is stable across runs and is the single source of truth
// for the provider's identity + capabilities. PR-P2 keeps the
// pre-PR-P2 inline-construction shape (the loader package imports
// both providers) so the package graph stays acyclic. A future
// PR-P5 stub can build its own ProviderMeta and either replace
// this list or extend it via a separate Register() helper.
//
// The returned slice is sorted alphabetically by Name so the
// deterministic-order invariant is locked (the test
// TestProviders_RegistersAllProviders pins the order).
func Providers() []ProviderMeta {
	// Both providers expose a static Capabilities() helper
	// (stripe.StripeCapabilities, paddle.PaddleCapabilities) so the
	// metadata-only lookup here does not have to construct a *Client
	// / *Provider just to read the bits. Providers() is invoked once
	// at boot, not per-request, but the asymmetry was a /code-review
	// finding (PR #802 follow-up). Capabilities() never reads
	// c.api / p.client — both functions return a constant.
	out := []ProviderMeta{
		{
			Name:         providerStripe,
			Capabilities: stripe.StripeCapabilities(),
			EnvVars:      []string{"STRIPE_API_KEY", "STRIPE_WEBHOOK_SECRET"},
			// BuildAPID nil → apid reads STRIPE_WEBHOOK_SECRET +
			// FAAS_BILLING_PORTAL_URL inline (cmd/apid/main.go).
			BuildAPID: nil,
			BuildMeterd: func(cfg *RootBillingConfig, env func(string) string, store state.Store, log *slog.Logger) (any, error) {
				var stripeCfg *stripe.Config
				if cfg != nil {
					stripeCfg = cfg.Stripe
				}
				tomlAPIKey, tomlSecret := "", ""
				if stripeCfg != nil {
					tomlAPIKey = stripeCfg.APIKey
					tomlSecret = stripeCfg.WebhookSecret
				}
				return stripe.NewClient(
					store,
					store,
					resolveSecret(env("STRIPE_API_KEY"), tomlAPIKey),
					resolveSecret(env("STRIPE_WEBHOOK_SECRET"), tomlSecret),
					log,
				), nil
			},
		},
		{
			Name:         providerPaddle,
			Capabilities: paddle.PaddleCapabilities(),
			EnvVars:      []string{"FAAS_PADDLE_API_KEY", "FAAS_PADDLE_WEBHOOK_SECRET", "FAAS_PADDLE_SANDBOX"},
			BuildAPID: func(cfg *RootBillingConfig, env func(string) string, log *slog.Logger) (any, error) {
				var paddleCfg *paddle.Config
				if cfg != nil {
					paddleCfg = cfg.Paddle
				}
				tomlAPIKey, tomlSecret, tomlSandbox, tomlTolerance := "", "", false, 0
				if paddleCfg != nil {
					tomlAPIKey = paddleCfg.APIKey
					tomlSecret = paddleCfg.WebhookSecret
					tomlSandbox = paddleCfg.Sandbox
					tomlTolerance = paddleCfg.ToleranceSeconds
				}
				sandbox := resolveSandbox(env("FAAS_PADDLE_SANDBOX"), tomlSandbox)
				p, err := paddle.NewProvider(
					resolveSecret(env("FAAS_PADDLE_API_KEY"), tomlAPIKey),
					resolveSecret(env("FAAS_PADDLE_WEBHOOK_SECRET"), tomlSecret),
					sandbox,
					log,
				)
				if err != nil {
					return nil, fmt.Errorf("billing/loader: build Paddle provider for apid: %w", err)
				}
				// PR-P4 — install the operator-configured webhook
				// tolerance. The single source of truth is
				// tomlTolerance (cfg.Paddle.ToleranceSeconds), already
				// populated by ApplyBillingEnvOverlay with the
				// env-vs-TOML precedence applied (env wins; bad
				// parse is silently dropped — see
				// TestApplyBillingEnvOverlay_FAAS_PADDLE_WEBHOOK_TOLERANCE_SECONDS).
				//
				// PR-P4 review finding #3: an earlier revision also
				// read env("FAAS_PADDLE_WEBHOOK_TOLERANCE_SECONDS")
				// here and re-parsed it. That duplicated the
				// overlay's parser and created two paths that could
				// drift if one was updated and the other wasn't.
				// Removed; if you need to change the parser, edit
				// ApplyBillingEnvOverlay in config.go.
				//
				// A value <= 0 leaves p.webhookTolerance at 0, which
				// WebhookTolerance() clamps to the default —
				// pre-PR-P4 behaviour is preserved when the operator
				// has not configured the knob.
				if tomlTolerance > 0 {
					p.SetWebhookTolerance(time.Duration(tomlTolerance) * time.Second)
				}
				return p, nil
			},
			BuildMeterd: func(cfg *RootBillingConfig, env func(string) string, store state.Store, log *slog.Logger) (any, error) {
				var paddleCfg *paddle.Config
				if cfg != nil {
					paddleCfg = cfg.Paddle
				}
				tomlAPIKey, tomlSandbox := "", false
				if paddleCfg != nil {
					tomlAPIKey = paddleCfg.APIKey
					tomlSandbox = paddleCfg.Sandbox
				}
				p, err := paddle.NewProviderWithDedupe(
					resolveSecret(env("FAAS_PADDLE_API_KEY"), tomlAPIKey),
					resolveSandbox(env("FAAS_PADDLE_SANDBOX"), tomlSandbox),
					log,
					store,
				)
				if err != nil {
					return nil, fmt.Errorf("billing/loader: build Paddle provider for meterd: %w", err)
				}
				return p, nil
			},
		},
		{
			Name:         providerPolar,
			Capabilities: polar.PolarCapabilities(),
			EnvVars: []string{
				"FAAS_POLAR_ACCESS_TOKEN", "FAAS_POLAR_WEBHOOK_SECRET",
				"FAAS_POLAR_SANDBOX", "FAAS_POLAR_HOBBY_PRODUCT_ID",
				"FAAS_POLAR_PRO_PRODUCT_ID", "FAAS_POLAR_SCALE_PRODUCT_ID",
				"FAAS_POLAR_METER_ID", "FAAS_POLAR_BASE_URL",
			},
			BuildAPID: func(cfg *RootBillingConfig, env func(string) string, log *slog.Logger) (any, error) {
				p, err := polar.NewProvider(resolvedPolarConfig(cfg, env), log)
				if err != nil {
					return nil, fmt.Errorf("billing/loader: build Polar provider for apid: %w", err)
				}
				return p, nil
			},
			BuildMeterd: func(cfg *RootBillingConfig, env func(string) string, store state.Store, log *slog.Logger) (any, error) {
				p, err := polar.NewProviderWithDedupe(resolvedPolarConfig(cfg, env), log, store)
				if err != nil {
					return nil, fmt.Errorf("billing/loader: build Polar provider for meterd: %w", err)
				}
				return p, nil
			},
		},
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// LoadProviderForAPID returns a billing.Provider for apid's webhook
// ingress + changePlan handler.
//
//   - cfg.Provider empty or "polar" → constructs a *polar.Provider.
//     Polar is the public-release default and requires a configured
//     access token, catalog products, and meter preflight.
//
//   - cfg.Provider "stripe" → returns (nil, "stripe", nil).
//     The apid Stripe path stays inline; apid reads
//     FAAS_BILLING_PORTAL_URL + STRIPE_WEBHOOK_SECRET directly because
//     it doesn't need to construct a *stripe.Client (only the webhook
//     signature check + the billing-portal template URL).
//
//   - cfg.Provider "paddle" → constructs a *paddle.Provider and
//     best-effort runs EnsurePlanProducts so the price catalog is
//     populated before the first /v1/webhooks/paddle POST can land.
//     Returns the provider + the literal "paddle" even if the catalog
//     hydration fails — the catalog hydrates lazily on the first
//     CreateUpgradeTransaction / PushUsageRecord call, and a transient
//     Paddle outage at boot must not take down apid (the webhook
//     ingress is independent of the catalog). Returns the provider +
//     the literal "paddle".
//
//   - cfg.Provider "polar" → constructs a *polar.Provider and
//     requires its configured products and meter to pass the live
//     catalog preflight before apid starts accepting billing traffic.
//
//   - Any other value → error so a typo fails the boot loudly.
//
// cfg is the env-overlaid TOML config (the caller called
// ApplyBillingEnvOverlay before this function). env is the wake-up
// reader that the closure-built providers use to read individual
// secrets — env already reflects the env-overlaid final value.
//
// ctx is the boot-level context — a daemon shutdown cancels it, which
// lets an in-flight EnsurePlanProducts call abort cleanly instead of
// racing the process exit.
func LoadProviderForAPID(ctx context.Context, cfg *RootBillingConfig, env func(string) string, log *slog.Logger) (billing.Provider, string, error) {
	if cfg == nil {
		cfg = &RootBillingConfig{}
	}
	// cfg.DefaultProvider() applies the implicit default (public release = Polar)
	// when Provider is empty. The legacy Stripe opt-in
	// (FAAS_BILLING_PROVIDER=stripe) is unaffected — an explicit value
	// still wins. The default lives in exactly one place; both loader
	// entry points and any future sibling go through
	// RootBillingConfig.DefaultProvider() rather than carrying their
	// own `if name == ""` literal.
	name := cfg.DefaultProvider()
	for _, m := range Providers() {
		if m.Name != name {
			continue
		}
		if m.BuildAPID == nil {
			// Legacy apid surface (Stripe) — apid reads its env vars
			// inline. Returning nil + name keeps the rest of the apid
			// boot path unchanged for the legacy opt-in.
			return nil, m.Name, nil
		}
		p, err := m.BuildAPID(cfg, env, log)
		if err != nil {
			return nil, m.Name, fmt.Errorf("billing: build %s for apid: %w", m.Name, err)
		}
		// Type-assert the closure's `any` return back to billing.Provider.
		// The compile-time assertion in each provider package guarantees
		// the concrete type satisfies the interface.
		bp, ok := p.(billing.Provider)
		if !ok {
			return nil, m.Name, fmt.Errorf("billing: provider %s BuildAPID returned %T, does not satisfy billing.Provider", m.Name, p)
		}
		// EnsurePlanProducts at boot so the price catalog is populated
		// before the first /v1/webhooks/paddle call (the dunning state
		// machine + the changePlan 402 path both read planMonthly /
		// planOverage). The call is bounded by the SDK's HTTP timeout
		// and the supplied ctx so a daemon shutdown can cancel it.
		//
		bootCtx, cancel := context.WithTimeout(ctx, providerBootHydrationTimeout)
		err = bp.EnsurePlanProducts(bootCtx)
		cancel()
		if err != nil {
			if m.Name == providerPolar {
				return nil, m.Name, fmt.Errorf("billing: Polar catalog preflight failed: %w", err)
			}
			log.Warn("billing: EnsurePlanProducts failed at boot — upgrade 402 will degrade to 500 until next run",
				"provider", m.Name, "err", err)
		}
		return bp, m.Name, nil
	}
	return nil, "", fmt.Errorf("billing: unknown FAAS_BILLING_PROVIDER=%q", name)
}

// LoadProviderForMeterd returns a billing.Provider for the meterd pusher
// loop. Always non-nil on success — the meterd pusher requires a Provider
// (the legacy *stripe.Client path is folded into the interface).
//
//   - cfg.Provider "stripe" → the Stripe BuildMeterd closure
//     constructs a *stripe.Client with the supplied state.Store as both
//     the StateStore and the PushDedupe (the Stripe provider's
//     NewClient takes both args; today every Store implementation
//     satisfies both interfaces).
//
//   - cfg.Provider "paddle" → the Paddle BuildMeterd closure constructs
//     a *paddle.Provider with the state.Store as the cross-process
//     overage dedupe. meterd doesn't need the webhook secret (no
//     ingress) so the second arg is empty.
//
//   - cfg.Provider "polar" → constructs a *polar.Provider and refuses
//     to start meterd when the configured product catalog preflight fails.
//
//   - Any other value → error so a typo fails the boot loudly.
//
// cfg is the env-overlaid TOML config. env is the env-var reader the
// per-provider closure uses. store is the meterd-side state (passed
// through to the closure; the Stripe + Paddle closures each interpret
// it differently).
//
// Note: no ctx parameter — the Stripe + Paddle constructors here don't
// accept a context (they don't dial out at construction time; the
// ping happens later on the pusher tick). The pusher loop's own ctx
// governs the actual SDK calls. LoadProviderForAPID takes ctx because
// it eagerly runs EnsurePlanProducts at boot.
func LoadProviderForMeterd(cfg *RootBillingConfig, env func(string) string, store state.Store, log *slog.Logger) (billing.Provider, string, error) {
	if cfg == nil {
		cfg = &RootBillingConfig{}
	}
	// Mirror LoadProviderForAPID — go through DefaultProvider() so the
	// implicit-default literal lives in exactly one place
	// (RootBillingConfig.DefaultProvider, not two `if name == ""` checks
	// in this file). See comment at DefaultProvider for the rationale.
	name := cfg.DefaultProvider()
	for _, m := range Providers() {
		if m.Name != name {
			continue
		}
		if m.BuildMeterd == nil {
			return nil, m.Name, fmt.Errorf("billing: provider %s registered with nil BuildMeterd", m.Name)
		}
		p, err := m.BuildMeterd(cfg, env, store, log)
		if err != nil {
			return nil, m.Name, fmt.Errorf("billing: build %s for meterd: %w", m.Name, err)
		}
		bp, ok := p.(billing.Provider)
		if !ok {
			return nil, m.Name, fmt.Errorf("billing: provider %s BuildMeterd returned %T, does not satisfy billing.Provider", m.Name, p)
		}
		if m.Name == providerPolar {
			// Polar's product and usage-event IDs are deployment-owned config,
			// not lazily discovered data. Refuse to start meterd when the
			// catalog cannot satisfy the billing contract; otherwise the box
			// would sample usage forever while every checkout/push is unusable.
			preflightCtx, cancel := context.WithTimeout(context.Background(), providerBootHydrationTimeout)
			err := bp.EnsurePlanProducts(preflightCtx)
			cancel()
			if err != nil {
				return nil, m.Name, fmt.Errorf("billing: Polar catalog preflight failed for meterd: %w", err)
			}
		}
		return bp, m.Name, nil
	}
	return nil, "", fmt.Errorf("billing: unknown FAAS_BILLING_PROVIDER=%q", name)
}
