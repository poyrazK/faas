package polar

const (
	// DefaultUsageEventName is the event name used by the meterd compute
	// pusher. The Polar meter must filter on this name and sum
	// metadata.gb_ram_hours.
	DefaultUsageEventName = "faas_ram_usage"
	// DefaultEgressUsageEventName is the event name reserved for canonical
	// host-interface egress. It is inert while EgressBillingMode is off.
	DefaultEgressUsageEventName = "faas_egress_usage"

	EgressBillingOff    = "off"
	EgressBillingShadow = "shadow"
	EgressBillingLive   = "live"
)

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

	// EgressBillingMode is off, shadow, or live. Off is the default. Shadow
	// calculates monthly net overage and records local delivery receipts, but
	// never posts a Polar event. Live is the only mode that advertises the
	// egress billing capability.
	EgressBillingMode string `toml:"egress_billing_mode"`
	// EgressBillingFrom is the explicit RFC3339 UTC-hour activation boundary.
	// Older durable windows are acknowledged locally and never sent.
	EgressBillingFrom string `toml:"egress_billing_from"`
	// EgressUsageEventName and EgressMeterID identify a separate Polar meter
	// that sums metadata.egress_gib. They must not reuse the compute meter.
	EgressUsageEventName string `toml:"egress_usage_event_name"`
	EgressMeterID        string `toml:"egress_meter_id"`
	// EgressMillicentsPerGiB is the catalog price for one GiB above the
	// calendar-month allowance. Polar prices have cent precision, so runtime
	// validation rejects values that are not a whole number of euro cents.
	EgressMillicentsPerGiB int64 `toml:"egress_millicents_per_gib"`
	// Included egress is configured explicitly per paid plan. No defaults are
	// guessed: shadow/live startup fails until all three values are positive.
	HobbyIncludedEgressGiB int64 `toml:"hobby_included_egress_gib"`
	ProIncludedEgressGiB   int64 `toml:"pro_included_egress_gib"`
	ScaleIncludedEgressGiB int64 `toml:"scale_included_egress_gib"`
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
	if c.EgressBillingMode == "" {
		c.EgressBillingMode = EgressBillingOff
	}
	if c.EgressUsageEventName == "" {
		c.EgressUsageEventName = DefaultEgressUsageEventName
	}
}
