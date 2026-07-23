package stripe

// Config is the stripex on-disk settings. Mirrors schedd/meterd — every
// field has a working default so a missing or partial file still yields
// a runnable daemon.
type Config struct {
	// APIKey is the Stripe secret key (sk_test_… / sk_live_…). Loaded
	// from STRIPE_API_KEY env in cmd/apid and cmd/meterd; empty in dev.
	APIKey string `toml:"api_key"`
	// WebhookSecret is the endpoint signing secret Stripe issues when
	// the webhook endpoint is configured. Loaded from
	// STRIPE_WEBHOOK_SECRET env. The webhook handler refuses payloads
	// without a matching signature.
	WebhookSecret string `toml:"webhook_secret"`
	// Tolerance is the Stripe-Signature timestamp tolerance window.
	// Defaults to 5 min (Stripe's recommended default).
	ToleranceSeconds int `toml:"tolerance_seconds"`
}
