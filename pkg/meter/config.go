package meter

import "time"

// Config is the meterd daemon's TOML-backed settings. Defaults match
// the spec §4.7 cadence:
//
//   - sample tick: 60 s  (every minute flush)
//   - quota tick:  60 s  (every minute verdict per account)
//   - stripe tick: 24 h  (every day push, integer-arithmetic wire quantity)
//   - dunning tick: 1 h  (dunning state machine 7d/21d clocks)
//
// The four timers run independently — a slow quota loop never blocks the
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
	// the production default (24 h). The integer-arithmetic wire
	// quantity (pkg/billing/stripe/usage.go) is deterministic across the full
	// window, so the pusher posts *one* metered usage record per
	// account per 24h instead of one per hour — eliminates per-hour
	// fractional truncation loss on the wire (was ~0.3 % of the bill
	// for a Hobby instance; spec gate is 0.1 %).
	StripeInterval time.Duration
	// DunningInterval is how often the dunning timer sweeps accounts
	// for the past_due → 7d → suspended and suspended → 21d →
	// deleted_pending transitions (spec §4.7, §17 dunning state
	// machine). Zero means the production default (1 h). The 7d / 21d
	// thresholds are exact — the tick frequency only affects how soon
	// after the deadline a row is transitioned, never the deadline
	// itself (the comparison is against PastDueAt).
	DunningInterval time.Duration
	// ResidencyInterval is how often the residency timer emits the
	// §12 "Resident GB per paying customer" gauge (ADR-031, PR #141).
	// Zero means the production default (60 s). 60 s matches the
	// §12 alert rule's `for: 1h` window with enough resolution to
	// catch a fast-migrating plan without spending DB scans.
	ResidencyInterval time.Duration
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
		c.StripeInterval = 24 * time.Hour
	}
	if c.DunningInterval == 0 {
		c.DunningInterval = time.Hour
	}
	if c.ResidencyInterval == 0 {
		c.ResidencyInterval = 60 * time.Second
	}
}
