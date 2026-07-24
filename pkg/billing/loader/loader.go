// Package loader is the single source of truth for the
// FAAS_BILLING_PROVIDER selector. Both cmd/apid and cmd/meterd
// import this so the canonical-name list, the env-var name, and the
// default cannot drift between daemons.
//
// Lives in its own sub-package (not pkg/billing) because pkg/billing
// is imported by pkg/billing/{paddle,stripe} and the loader imports
// those — a same-package location would cycle.
//
// Two functions (not one) because the Stripe constructor needs
// stripe.PushDedupe for the meterd path and not for the apid path:
//
//   - LoadProviderForAPID returns a paddle.Provider when
//     FAAS_BILLING_PROVIDER=paddle (and runs EnsurePlanProducts so the
//     catalog is populated before the first webhook lands); nil + "stripe"
//     otherwise. The apid Stripe path stays inline (cmd/apid/server.go
//     reads FAAS_BILLING_PORTAL_URL + STRIPE_WEBHOOK_SECRET directly,
//     since apid doesn't need to construct a *stripe.Client — only the
//     webhook signature check + the billing-portal template URL).
//
//   - LoadProviderForMeterd returns a Provider for the meterd pusher
//     loop. For Stripe, constructs a *stripe.Client with the meterd
//     PushDedupe so the per-hour dedupe surface is wired. For Paddle,
//     constructs a *paddle.Provider; meterd doesn't need the webhook
//     secret (no ingress in meterd) so the second arg is the empty
//     string.
//
//     FAAS_BILLING_PROVIDER= anything else returns an error so a typo
//     ("braintree", "paypal") fails the daemon boot loudly rather than
//     silently defaulting to Stripe.
//
// Env vars consumed (all optional except per the per-branch docs):
//
//	FAAS_BILLING_PROVIDER   "" | "stripe" | "paddle"   default ""
//	STRIPE_API_KEY          required when Stripe is the active provider (apid + meterd)
//	STRIPE_WEBHOOK_SECRET   required when Stripe is the active provider (apid only)
//	FAAS_PADDLE_API_KEY     required when Paddle is the active provider (apid + meterd)
//	FAAS_PADDLE_WEBHOOK_SECRET  required when Paddle is the active provider (apid only)
//	FAAS_PADDLE_SANDBOX     "1" / "true" to use api.sandbox.paddle.com (apid + meterd)
package loader

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/paddle"
	"github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/onebox-faas/faas/pkg/state"
)

// LoadProviderForAPID returns a billing.Provider for apid's webhook
// ingress + changePlan handler.
//
//   - FAAS_BILLING_PROVIDER empty or "stripe" → returns (nil, "stripe", nil).
//     The apid Stripe path stays inline; apid reads FAAS_BILLING_PORTAL_URL
//
//   - STRIPE_WEBHOOK_SECRET directly because it doesn't need to
//     construct a *stripe.Client (only the webhook signature check +
//     the billing-portal template URL).
//
//   - FAAS_BILLING_PROVIDER "paddle" → constructs a *paddle.Provider and
//     best-effort runs EnsurePlanProducts so the price catalog is
//     populated before the first /v1/webhooks/paddle POST can land.
//     Returns the provider + the literal "paddle" even if the catalog
//     hydration fails — the catalog hydrates lazily on the first
//     CreateUpgradeTransaction / FlushOverageNow call, and a transient
//     Paddle outage at boot must not take down apid (the webhook
//     ingress is independent of the catalog). Returns the provider +
//     the literal "paddle".
//
//   - Any other value → error so a typo fails the boot loudly.
//
// ctx is the boot-level context — a daemon shutdown cancels it, which
// lets an in-flight EnsurePlanProducts call abort cleanly instead of
// racing the process exit.
func LoadProviderForAPID(ctx context.Context, env func(string) string, log *slog.Logger) (billing.Provider, string, error) {
	switch env("FAAS_BILLING_PROVIDER") {
	case "", "stripe":
		return nil, "stripe", nil
	case "paddle":
		apiKey := env("FAAS_PADDLE_API_KEY")
		webhookSecret := env("FAAS_PADDLE_WEBHOOK_SECRET")
		sandbox := env("FAAS_PADDLE_SANDBOX") == "1" || env("FAAS_PADDLE_SANDBOX") == "true"
		p := paddle.NewProvider(apiKey, webhookSecret, sandbox, log)
		// EnsurePlanProducts at boot so the price catalog is populated
		// before the first /v1/webhooks/paddle call (the dunning state
		// machine + the changePlan 402 path both read planMonthly /
		// planOverage). The call is bounded by the SDK's HTTP timeout
		// and the supplied ctx so a daemon shutdown can cancel it.
		//
		// Best-effort: a failed hydration is warn-logged, not fatal.
		// The upgrade 402 will degrade to a 500 "monthly price missing"
		// until the next EnsurePlanProducts run (e2e harness +
		// transient Paddle outage at boot both need this). The webhook
		// ingress is independent of the catalog — the dunning state
		// machine reads acct.Plan, not price handles, so the boot
		// failure mode is the upgrade 402 path only.
		if err := p.EnsurePlanProducts(ctx); err != nil {
			log.Warn("billing: paddle EnsurePlanProducts failed at boot — upgrade 402 will degrade to 500 until next run",
				"err", err)
		}
		return p, "paddle", nil
	default:
		return nil, "", fmt.Errorf("billing: unknown FAAS_BILLING_PROVIDER=%q", env("FAAS_BILLING_PROVIDER"))
	}
}

// LoadProviderForMeterd returns a billing.Provider for the meterd pusher
// loop. Always non-nil on success — the meterd pusher requires a Provider
// (the legacy *stripe.Client path is folded into the interface).
//
//   - FAAS_BILLING_PROVIDER empty or "stripe" → constructs a
//     *stripe.Client with the supplied PushDedupe (the meterd dedupe
//     surface is the HasStripePushHour / RecordStripePushHour pair on
//     state.Store, which stripe.NewClient needs).
//
//   - FAAS_BILLING_PROVIDER "paddle" → constructs a *paddle.Provider;
//     meterd doesn't need the webhook secret (no ingress) so the second
//     arg is empty.
//
//   - Any other value → error so a typo fails the boot loudly.
//
// Note: no ctx parameter — the Stripe + Paddle constructors here don't
// accept a context (they don't dial out at construction time; the
// ping happens later on the pusher tick). The pusher loop's own ctx
// governs the actual SDK calls. LoadProviderForAPID takes ctx because
// it eagerly runs EnsurePlanProducts at boot.
func LoadProviderForMeterd(env func(string) string, store state.Store, dedupe stripe.PushDedupe, log *slog.Logger) (billing.Provider, string, error) {
	switch env("FAAS_BILLING_PROVIDER") {
	case "", "stripe":
		return stripe.NewClient(store, dedupe, env("STRIPE_API_KEY"), env("STRIPE_WEBHOOK_SECRET"), log), "stripe", nil
	case "paddle":
		apiKey := env("FAAS_PADDLE_API_KEY")
		sandbox := env("FAAS_PADDLE_SANDBOX") == "1" || env("FAAS_PADDLE_SANDBOX") == "true"
		// meterd doesn't need the webhook secret (no ingress in meterd).
		p := paddle.NewProvider(apiKey, "", sandbox, log)
		return p, "paddle", nil
	default:
		return nil, "", fmt.Errorf("billing: unknown FAAS_BILLING_PROVIDER=%q", env("FAAS_BILLING_PROVIDER"))
	}
}
