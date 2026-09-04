package polar

// DefaultUsageEventName is the event name used by the meterd usage pusher.
// The Polar meter must filter on this name and sum metadata.gb_ram_hours.
const DefaultUsageEventName = "faas_ram_usage"

// Config is the Polar on-disk settings. Polar product, meter, and webhook
// resources are intentionally configured in the Polar dashboard; this
// provider only stores their stable IDs and sends/normalizes runtime traffic.
type Config struct {
	// APIKey is a Polar organization access token. The canonical environment
	// variable is FAAS_POLAR_ACCESS_TOKEN; APIKey is retained as the local
	// config name because the billing loader already uses that vocabulary for
	// Stripe and Paddle.
	APIKey string `toml:"api_key"`
	// WebhookSecret is the Standard Webhooks secret for the Polar endpoint.
	WebhookSecret string `toml:"webhook_secret"`
	// Sandbox selects sandbox-api.polar.sh instead of api.polar.sh.
	Sandbox bool `toml:"sandbox"`
	// ToleranceSeconds is the accepted webhook timestamp age in either
	// direction. The default is five minutes.
	ToleranceSeconds int `toml:"webhook_tolerance_seconds"`

	// Product IDs for the recurring paid plans. Free is local-only and has no
	// Polar product. Products should contain both the fixed recurring price and
	// the metered price backed by the configured usage meter.
	HobbyProductID string `toml:"hobby_product_id"`
	ProProductID   string `toml:"pro_product_id"`
	ScaleProductID string `toml:"scale_product_id"`

	// UsageEventName must match the name used by the Polar meter filter.
	UsageEventName string `toml:"usage_event_name"`
	// MeterID is the Polar meter UUID used by usage ingestion and
	// reconciliation. It is required in production: the provider validates
	// that every paid product points at this meter before either daemon starts.
	MeterID string `toml:"meter_id"`
	// SuccessURL and ReturnURL are optional hosted-checkout redirects.
	SuccessURL string `toml:"success_url"`
	ReturnURL  string `toml:"return_url"`
	// BaseURL exists for local HTTP contract tests and private API proxies. In
	// production leave it empty so Sandbox selects Polar's documented host.
	BaseURL string `toml:"base_url"`
}

// Defaults fills in safe non-secret defaults. Product IDs and credentials
// remain empty because they are deployment-specific.
func (c *Config) Defaults() {
	if c == nil {
		return
	}
	if c.ToleranceSeconds <= 0 {
		c.ToleranceSeconds = 300
	}
	if c.UsageEventName == "" {
		c.UsageEventName = DefaultUsageEventName
	}
}
