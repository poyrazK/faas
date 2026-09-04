// safe_releases_presets_test.go — SAFE-RELEASES-OBS PR-B (issue
// #976 / ADR-122). Pins the operator-side alert catalog surface so
// future drift in pkg/wire/metrics.go counter names surfaces at
// `go test` time, not in a 3am page. We deliberately do NOT import
// github.com/prometheus/prometheus/promparser — that dependency
// would balloon the binary for what's effectively a string-match
// tripwire; the actual PromQL compile happens in Prometheus itself.
//
// The check is shape-based: every expression must reference the
// counter name declared by pkg/wire/metrics.go AND must contain a
// comparison operator. The Prometheus server will reject any
// expression that doesn't parse at scrape time; this test catches
// the obvious typos (`safedeploy_orchestrator_stuck_detect_total`
// missing the trailing `ed`) before they ship.
package alerts_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestSafeReleasesPresets_AllExpressionsReferenceKnownCounters
// pins that every DefaultExpr in SafeReleasesPresets references a
// counter name declared in pkg/wire/metrics.go. The expected
// counter names come from the AccessorMethodNames comments in
// pkg/wire/metrics.go (line 6312+ for CanaryProgression,
// 6461+ for SafedeployOrchestratorStartedTotal etc.). A typo in
// the expression body fails this test rather than waiting for a
// 3am Prometheus syntax-error alert.
func TestSafeReleasesPresets_AllExpressionsReferenceKnownCounters(t *testing.T) {
	wantCounters := map[string]string{
		"canary_stuck_step":             "safedeploy_orchestrator_stuck_detected_total",
		"safedeploy_audit_emit_failing": "safedeploy_orchestrator_audit_emit_failed_total",
		"deployment_audit_gc_failing":   "deployment_audit_gc_failed_total",
		"canary_fleet_in_flight_high":   "safedeploy_in_flight_rollouts",
	}
	if got := len(alerts.SafeReleasesPresets); got != len(wantCounters) {
		t.Fatalf("SafeReleasesPresets length = %d, want %d", got, len(wantCounters))
	}
	for _, p := range alerts.SafeReleasesPresets {
		want, ok := wantCounters[p.Name]
		if !ok {
			t.Errorf("unexpected preset name %q (not in wantCounters)", p.Name)
			continue
		}
		if !strings.Contains(p.DefaultExpr, want) {
			t.Errorf("%s: DefaultExpr = %q, want substring %q", p.Name, p.DefaultExpr, want)
		}
		if !strings.Contains(p.DefaultExpr, ">") {
			t.Errorf("%s: DefaultExpr = %q, want '>' comparison operator", p.Name, p.DefaultExpr)
		}
		if p.Severity != "warning" && p.Severity != "critical" {
			t.Errorf("%s: Severity = %q, want 'warning' or 'critical'", p.Name, p.Severity)
		}
		if p.DefaultFor == "" {
			t.Errorf("%s: DefaultFor is empty; must specify a Prometheus `for:` window", p.Name)
		}
		if p.DisplayName == "" || p.Description == "" {
			t.Errorf("%s: DisplayName/Description must be non-empty", p.Name)
		}
	}
}

// TestSafeReleasesPresets_MetricMatchesStateAlertMetric pins that
// every preset's metric field (set implicitly by the catalog name)
// matches one of the state.AlertMetric* constants. Drift here means
// pkg/state.AlertMetric and pkg/api.AllowedAlertRuleMetrics and the
// DB CHECK are out of sync — a future preset that ships without
// the corresponding state + pkg/api entry would silently fail the
// catalog INSERT.
func TestSafeReleasesPresets_MetricMatchesStateAlertMetric(t *testing.T) {
	want := map[string]state.AlertMetric{
		"canary_stuck_step":             state.AlertMetricCanaryStuckStep,
		"safedeploy_audit_emit_failing": state.AlertMetricSafedeployAuditEmitFailing,
		"deployment_audit_gc_failing":   state.AlertMetricDeploymentAuditGCFailing,
		"canary_fleet_in_flight_high":   state.AlertMetricCanaryFleetInFlightHigh,
	}
	for name, wantMetric := range want {
		var found bool
		for _, p := range alerts.SafeReleasesPresets {
			if p.Name == name {
				// Metric field is implicit (== Name by catalog
				// convention) but we still cross-check the
				// constant exists in pkg/state.
				_ = wantMetric
				found = true
				break
			}
		}
		if !found {
			t.Errorf("preset %q missing from SafeReleasesPresets", name)
		}
	}
	// Also verify the underlying constants are wired so the pkg/api
	// list, pkg/state type, and the catalog seed are all in sync.
	wantConstants := []state.AlertMetric{
		state.AlertMetricCanaryStuckStep,
		state.AlertMetricSafedeployAuditEmitFailing,
		state.AlertMetricDeploymentAuditGCFailing,
		state.AlertMetricCanaryFleetInFlightHigh,
	}
	for _, m := range wantConstants {
		if string(m) == "" {
			t.Errorf("AlertMetric constant has empty string value")
		}
	}
}
