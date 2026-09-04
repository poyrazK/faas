// Package alerts — SAFE-RELEASES-OBS PR-B (issue #976 / ADR-122).
//
// SafeReleasesPresets documents the 4 Prometheus alert rules that
// back the safe-releases lifecycle. These are NOT customer-facing
// alert_rules rows; they are operator-only Prometheus alert rules
// that the meterd exposes via the wire.OpsMetrics counters
// declared in pkg/wire/metrics.go:
//
//	canary_stuck_step              → safedeploy_orchestrator_stuck_detected_total rate
//	safedeploy_audit_emit_failing  → safedeploy_orchestrator_audit_emit_failed_total rate
//	deployment_audit_gc_failing    → deployment_audit_gc_failed_total rate
//	canary_fleet_in_flight_high    → safedeploy_in_flight_rollouts gauge
//
// The catalog seed in migrations/20260905000000001_alert_presets_safe_releases_seed.sql
// inserts matching alert_presets rows so the /dashboard/alerts
// grid surfaces them; the AlertMetric* constants in pkg/state/types.go
// admit the metric strings; pkg/api.AllowedAlertRuleMetrics validates
// them at the handler boundary. The actual firing happens in
// Prometheus against the wire counters — meterd does not evaluate
// these. A future PR (likely PR-D follow-up) wires a Prometheus
// alertmanager webhook that bumps the *_alert_fired_total counters
// on the meterd side via the legacy AlertEvalFiredTotal precedent.
//
// The DefaultExpr strings are PromQL and MUST parse under the
// Prometheus version that the §12 dashboards ship. The
// TestSafeReleasesPresets_AllExpressionsParse test compiles each
// one with the prometheus/promparser library used elsewhere in the
// alert pipeline (see pkg/alerts/preset_test.go for the pattern).
package alerts

// SafeReleasesPreset is the in-Go description of a single operator
// alert rule. Mirrors the AlertPresetResponse shape so the dashboard
// can render the same JSON without re-mapping.
type SafeReleasesPreset struct {
	Name        string
	DisplayName string
	Description string
	Severity    string // "warning" | "critical"
	DefaultExpr string // PromQL
	DefaultFor  string // Prometheus `for:` clause
}

// SafeReleasesPresets is the ordered catalog. Order matches the
// migrations/20260905000000001 INSERT VALUES for documentation consistency.
var SafeReleasesPresets = []SafeReleasesPreset{
	{
		Name:        "canary_stuck_step",
		DisplayName: "Canary stuck at same step",
		Description: "safedeploy orchestrator detected a rollout stuck at the same canary step past StuckAfterDuration (default 30 min).",
		Severity:    "warning",
		DefaultExpr: "rate(safedeploy_orchestrator_stuck_detected_total[5m]) > 0",
		DefaultFor:  "5m",
	},
	{
		Name:        "safedeploy_audit_emit_failing",
		DisplayName: "Safedeploy audit emit failing",
		Description: "deployment_audit writes are failing for the orchestrator at >0.1/sec for 10 min. Likely cause: deployment_audit_kind_chk widened-missed (PR-A closes this).",
		Severity:    "critical",
		DefaultExpr: "rate(safedeploy_orchestrator_audit_emit_failed_total[10m]) > 0.1",
		DefaultFor:  "10m",
	},
	{
		Name:        "deployment_audit_gc_failing",
		DisplayName: "Deployment audit GC failing",
		Description: "90-day deployment_audit GC cron is failing for 1 h. Disk-fill risk.",
		Severity:    "warning",
		DefaultExpr: "rate(deployment_audit_gc_failed_total[1h]) > 0",
		DefaultFor:  "1h",
	},
	{
		Name:        "canary_fleet_in_flight_high",
		DisplayName: "Canary fleet in-flight high",
		Description: "more than 50 canaries in flight fleet-wide for 10 min. Operator back-pressure signal.",
		Severity:    "warning",
		DefaultExpr: "safedeploy_in_flight_rollouts > 50",
		DefaultFor:  "10m",
	},
}
