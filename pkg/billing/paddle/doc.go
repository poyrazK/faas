// Package paddle is the Paddle Billing v2 implementation of the
// billing.Provider interface (ADR-025, PR #2 of the paddle-mor
// series). It maps the per-deployment abstraction onto Paddle's
// flat-rate line-item shape — Paddle Billing v2 has no equivalent
// to Stripe's metered subscription_item, so overage is posted as
// a monthly line item at month-rollover instead of per-hour.
//
// The package depends on github.com/PaddleHQ/paddle-go-sdk/v5 for
// the data-plane calls (Customers / Products / Prices / Transactions
// / Subscriptions). The webhook signature scheme is re-implemented
// here (~30 LOC) instead of going through the SDK's *http.Request
// middleware so the Provider surface (bytes + headers + tolerance)
// stays interface-friendly. The canonical signed-string format is
// `<unix>:<body>` and the HMAC-SHA256 is verified via crypto/hmac.Equal.
//
// Configuration knobs (env-driven from cmd/apid/main.go in PR #3):
//
//	PADDLE_API_KEY       — production vs sandbox chosen at
//	                        construction time (sandbox=true uses
//	                        api.sandbox.paddle.com)
//	PADDLE_WEBHOOK_SECRET — webhook shared secret
//	PADDLE_SANDBOX        — "1"/"true" to force sandbox; flips
//	                        the SDK client selector
package paddle
