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
//     runs EnsurePlanProducts so the catalog is populated before the
//     first /v1/webhooks/paddle POST can land. Returns the provider +
//     the literal "paddle".
//
//   - Any other value → error so a typo fails the boot loudly.
func LoadProviderForAPID(env func(string) string, log *slog.Logger) (billing.Provider, string, error) {
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
		// planOverage). Uses context.Background — the boot path doesn't
		// have a request context, and the call is bounded by the SDK's
		// HTTP timeout.
		if err := p.EnsurePlanProducts(context.Background()); err != nil {
			return nil, "", fmt.Errorf("billing: paddle EnsurePlanProducts: %w", err)
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
