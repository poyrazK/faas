package meter

import "time"

// Config is the meterd daemon's TOML-backed settings. Defaults match
// the spec §4.7 cadence:
//
//   - sample tick: 60 s  (every minute flush)
//   - quota tick:  60 s  (every minute verdict per account)
//   - stripe tick: 60 m  (every hour push)
//
// The three timers run independently — a slow quota loop never blocks the
// minute sampler and vice versa. Production wires this from
// cmd/meterd/main.go via wire.ConfigFromTOML; tests use zero-value
// defaults.
type Config struct {
	// SampleInterval is how often the sampler rolls one minute of usage.
	// Zero means the production default (60 s).
	SampleInterval time.Duration
	// QuotaInterval is how often the quota loop walks every account.
	// Zero means the production default (60 s).
	QuotaInterval time.Duration
	// StripeInterval is how often the Stripe pusher fires. Zero means
	// the production default (60 m).
	StripeInterval time.Duration
	// ScheddSocket is the unix socket meterd dials for ParkInstance.
	ScheddSocket string
	// NotifyBackend is the db.Notify implementation; defaults to the
	// production postgres one in cmd/meterd.
	NotifyBackend string
}

// Defaults fills zero fields with the production cadences. Call this
// before parsing TOML so a partial config still gets sane intervals.
func (c *Config) Defaults() {
	if c.SampleInterval == 0 {
		c.SampleInterval = 60 * time.Second
	}
	if c.QuotaInterval == 0 {
		c.QuotaInterval = 60 * time.Second
	}
	if c.StripeInterval == 0 {
		c.StripeInterval = 60 * time.Minute
	}
}
