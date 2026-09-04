package meter

import (
	"time"

	"github.com/onebox-faas/faas/pkg/billing/reconciler"
)

// DefaultCertExpiryRefresherInterval is the production cadence
// for the ADR-123 cert-expiry refresher (issue #1233). The renewer
// bot updates tenant_surfaces.cert_not_after daily; one hour keeps
// the gauge within ~4 % of true remaining-seconds even if a
// renewal slips. Mirrored in cmd/meterd/alert_presets_ticks.go
// so the goroutine's standalone nil-default path stays consistent
// with pkg/meter.Config.Defaults().
const DefaultCertExpiryRefresherInterval = 1 * time.Hour

// DefaultCanaryEvalInterval is the production cadence for the
// canary_progression meterd tick (issue #976 / ADR-122 /
// SAFE-RELEASES-A). 30 s balances Postgres budget against
// step-boundary responsiveness — the shortest shipped stage
// duration is 1 min (aggressive preset), so 30 s gives the
// orchestrator a window to advance within half a stage. Faster
// (5 s, ADR-122's sketch) would burn Postgres budget for no
// observable benefit on the shortest shipped ladder.
const DefaultCanaryEvalInterval = 30 * time.Second

// DefaultSafeDeployInterval is the production cadence for the
// safedeploy orchestrator meterd tick (issue #976 / ADR-122 /
// SAFE-RELEASES-F). 30 s matches DefaultCanaryEvalInterval so the
// two ticks advance on a unified cadence — the canary_progression
// tick stamps canary_step + traffic_percent while the
// orchestrator stamps rollout_state; running both at 30 s keeps
// the operator dashboard's two counters in lockstep. ADR-122's
// sketched 5 s cadence was renamed because the per-tick-signal
// is per-rollout-state, not per-traffic-percent — 5 s would burn
// Postgres budget on rollouts whose state hasn't moved.
const DefaultSafeDeployInterval = 30 * time.Second

// DefaultAccountSpendAggregatorInterval is the production cadence
// for the ADR-123 MTD-spend gauge refresher. The upstream
// account_spend_snapshot is fed by the RollupLoop tick every
// 5 min, so refreshing the gauge faster than that is wasted work.
// Mirrored in cmd/meterd/alert_presets_ticks.go.
const DefaultAccountSpendAggregatorInterval = 5 * time.Minute

// DefaultAPIReachabilitySweepInterval is the production cadence
// for the ADR-123 meterd_api_reachable gauge refresher. 5 min
// matches the reachability window the gauge encodes (1.0 if the
// app served a successful invocation within the last 5 min, 0.0
// otherwise) — refreshing faster than 5 min would flip the gauge
// before a real outage window closes; refreshing slower would let
// the alert preset api_down fire on transient idle periods that
// recover within the window. Mirrored in
// cmd/meterd/api_reachability_sweep.go.
const DefaultAPIReachabilitySweepInterval = 5 * time.Minute

// DefaultDeploymentFailureSweepInterval is the production cadence
// for the ADR-123 apid_deployment_failed_total counter refresher.
// 60 s matches AlertEvalInterval so the counter increments keep
// pace with the alert evaluator's view of "deployments that
// transitioned to failed since the last tick". The counter is
// delta-shaped (previous sweep timestamp seeds the next SELECT's
// WHERE updated_at >= $lastSweep clause) so a slow tick can
// double-count on restart but never under-count during normal
// operation. Mirrored in cmd/meterd/deployment_failure_sweep.go.
const DefaultDeploymentFailureSweepInterval = 60 * time.Second

// Config is the meterd daemon's TOML-backed settings. Defaults match
// the spec §4.7 cadence:
//
//   - sample tick:    60 s  (every minute flush)
//   - quota tick:     60 s  (every minute verdict per account)
//   - stripe tick:    60 m  (hourly push with durable backfill)
//   - dunning tick:   1 h   (dunning state machine 7d/21d clocks)
//   - residency tick: 60 s  (§12 dashboard panel)
//   - alerts tick:    60 s  (issue #396 / ADR-045 alert evaluator)
//
// The six timers run independently — a slow quota loop never blocks the
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
	// StripeInterval is the cadence of the provider usage backfill pass.
	// The historical field name is retained for config compatibility. Zero
	// means the production default (1 h); each pass scans BillingLookback
	// of completed UTC-hour windows and provider idempotency keys make replay
	// safe after a restart or transient outage.
	StripeInterval time.Duration
	// BillingLookback is the completed usage history meterd rechecks on each
	// provider pass. Zero means 30 days, which covers a prolonged outage while
	// remaining bounded by the usage retention/index range.
	BillingLookback time.Duration
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
	// AlertEvalInterval is how often the alert evaluator walks
	// ListEnabledAlertRules (issue #396 / ADR-045, PR 4). Zero means
	// the production default (60 s). The 60 s cadence matches the
	// §12 dashboard's "alert evaluations per minute" panel and
	// matches the cool-down bucket for the shortest meaningful
	// customer-side cool-down window (1 minute). The evaluator loop
	// is single-goroutine today; a future meterd replica would
	// parallelise via the alert_deliveries.idempotency_key UNIQUE.
	AlertEvalInterval time.Duration
	// RollupInterval (ADR-048) is how often meterd rolls the
	// minute-grain usage_minutes rows into usage_daily (PK
	// account_id, app_id, day). Zero means the production default
	// (5 min). The rollup is idempotent — re-running on the same
	// window adds onto the prior partial sum, never overwrites —
	// so a missed tick is safe; the next tick covers the gap.
	RollupInterval time.Duration
	// ReconcileInterval (ADR-049 §B.1) is how often the drift
	// detector (pkg/billing/reconciler) walks the paid-account
	// list and compares local usage_minutes totals against the
	// provider's pushed totals. Zero means the production
	// default (6 h). Fail-soft per account: a transient
	// provider error skips that account, the loop continues.
	ReconcileInterval time.Duration
	// StorageRollupInterval (ADR-049 §B.3) is how often the
	// storage rollup tick (pkg/meter/storage.go) populates
	// snapshot_storage_daily. Zero means the production default
	// (1 h). The rollup overwrites the row for the current
	// day — distinct from usage_daily's additive-merge contract.
	StorageRollupInterval time.Duration
	// RetentionInterval (ADR-049 §B.4) is how often the retention
	// cron (pkg/meter/retention.go) DELETEs usage_minutes rows
	// older than 13 months. Zero means the production default
	// (1 day). The DELETE is idempotent — a second run on the
	// same day finds nothing to delete.
	RetentionInterval time.Duration
	// UpstreamProbeInterval (ADR-098 PR-C) is how often the
	// meterd probe loop walks the dedup'd (host_redacted_hash,
	// kind, port) target set and dials every entry via
	// crypto/tls.Dial. Zero means the production default
	// (30 s). The probe writes one row per
	// (host_redacted_hash, region) per tick.
	UpstreamProbeInterval time.Duration
	// UpstreamPartitionCreateInterval (ADR-098 PR-C) is how
	// often the partition-create cron ensures a forward-
	// rolling window of pre-created data_upstream_probes
	// partitions. Zero means the production default (1 h).
	UpstreamPartitionCreateInterval time.Duration
	// CertExpiryRefresherInterval (ADR-123) is how often the
	// meterd_tenant_surface_cert_expiry refresher walks
	// tenant_surfaces WHERE cert_state='issued', upserts the
	// meterd_tenant_surface_cert_expiry_state mirror, and stamps
	// the apid_tenant_surface_cert_expiry_seconds gauge (the
	// metric name keeps the legacy apid_ prefix for backward-
	// compat with already-deployed alert rules; the table itself
	// moved to meterd_ in migrations/00351 per the CLAUDE.md
	// ownership rule).
	// Zero means the production default (1 h). The renewer bot
	// rotates certs daily, so 1 h keeps the gauge within ~4 % of
	// true remaining-seconds even if a renewal slips.
	CertExpiryRefresherInterval time.Duration
	// AccountSpendAggregatorInterval (ADR-123) is how often
	// the MTD-spend gauge refresher walks every account and
	// stamps meterd_account_spend_eur{account_id}. Zero means
	// the production default (5 min). The upstream
	// account_spend_snapshot row is fed by the RollupLoop
	// tick every 5 min, so refreshing faster than that is
	// wasted work.
	AccountSpendAggregatorInterval time.Duration
	// APIReachabilitySweepInterval (ADR-123 PR-B, issue
	// #1233) is how often the API reachability gauge refresher
	// walks every (account_id, app_id) pair and stamps
	// meterd_api_reachable = 1.0 if the app served a successful
	// invocation within the last 5 min, 0.0 otherwise. Zero
	// means the production default (5 min) — matches the
	// reachability window the gauge encodes.
	APIReachabilitySweepInterval time.Duration
	// DeploymentFailureSweepInterval (ADR-123 PR-B, issue
	// #1233) is how often the deployment-failure counter
	// refresher walks every (account_id, app_id) pair, queries
	// CountFailedDeploymentsSince for the delta since the
	// previous sweep, and increments
	// apid_deployment_failed_total by that delta. Zero means
	// the production default (60 s) — matches AlertEvalInterval.
	DeploymentFailureSweepInterval time.Duration
	// CanaryEvalInterval (issue #976 / ADR-122 /
	// SAFE-RELEASES-A) is how often the canary_progression
	// meterd tick walks ListCanaryInFlight and advances
	// wall-clock-eligible steps. Zero means the production
	// default (30 s — DefaultCanaryEvalInterval).
	CanaryEvalInterval time.Duration
	// SafeDeployInterval (issue #976 / ADR-122 /
	// SAFE-RELEASES-F) is how often the safedeploy orchestrator
	// meterd tick walks SafedeployListPendingRollouts and
	// advances the rollout_state machine. Zero means the
	// production default (30 s — DefaultSafeDeployInterval).
	// The orchestrator tick is the complementary twin of the
	// canary_progression tick — both run on a unified cadence.
	SafeDeployInterval time.Duration
	// DeploymentAuditRetentionInterval (issue #976 / ADR-122 /
	// SAFE-RELEASES production-leveling Stream D) is how often
	// the deployment_audit GC cron
	// (pkg/meter/retention.go::RetentionLoopDeploymentAudit)
	// DELETEs deployment_audit rows older than 90 days. Zero
	// means the production default (6 h —
	// DefaultDeploymentAuditRetentionInterval). The DELETE is
	// idempotent — a second run on the same window finds
	// nothing to delete. Mirrors RetentionInterval's pattern
	// for the usage_minutes sweep.
	DeploymentAuditRetentionInterval time.Duration
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
		c.StripeInterval = time.Hour
	}
	if c.BillingLookback == 0 {
		c.BillingLookback = 30 * 24 * time.Hour
	}
	if c.DunningInterval == 0 {
		c.DunningInterval = time.Hour
	}
	if c.ResidencyInterval == 0 {
		c.ResidencyInterval = 60 * time.Second
	}
	if c.AlertEvalInterval == 0 {
		c.AlertEvalInterval = 60 * time.Second
	}
	if c.RollupInterval == 0 {
		c.RollupInterval = 5 * time.Minute
	}
	if c.ReconcileInterval == 0 {
		c.ReconcileInterval = reconciler.DefaultInterval
	}
	if c.StorageRollupInterval == 0 {
		c.StorageRollupInterval = DefaultStorageRollupInterval
	}
	if c.RetentionInterval == 0 {
		c.RetentionInterval = DefaultRetentionInterval
	}
	if c.UpstreamProbeInterval == 0 {
		c.UpstreamProbeInterval = DefaultUpstreamProbeInterval
	}
	if c.UpstreamPartitionCreateInterval == 0 {
		c.UpstreamPartitionCreateInterval = DefaultUpstreamPartitionCreateInterval
	}
	if c.CertExpiryRefresherInterval == 0 {
		c.CertExpiryRefresherInterval = DefaultCertExpiryRefresherInterval
	}
	if c.AccountSpendAggregatorInterval == 0 {
		c.AccountSpendAggregatorInterval = DefaultAccountSpendAggregatorInterval
	}
	if c.APIReachabilitySweepInterval == 0 {
		c.APIReachabilitySweepInterval = DefaultAPIReachabilitySweepInterval
	}
	if c.DeploymentFailureSweepInterval == 0 {
		c.DeploymentFailureSweepInterval = DefaultDeploymentFailureSweepInterval
	}
	if c.CanaryEvalInterval == 0 {
		c.CanaryEvalInterval = DefaultCanaryEvalInterval
	}
	if c.SafeDeployInterval == 0 {
		c.SafeDeployInterval = DefaultSafeDeployInterval
	}
	if c.DeploymentAuditRetentionInterval == 0 {
		c.DeploymentAuditRetentionInterval = DefaultDeploymentAuditRetentionInterval
	}
}
