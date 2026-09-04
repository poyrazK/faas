// handlers_dashboard_safe_releases_test.go — SAFE-RELEASES-OBS PR-C
// (issue #976 / ADR-122). Pins the two closed-vocabulary admission
// gates that filter the /dashboard/safe-releases surface. Both
// helpers are pure functions; we test them in isolation rather
// than spinning up a full authed server (handlers_dashboard_test.go
// already covers the authed-request path for sibling handlers).
package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestSafeReleasesAuditKind_PinsPRAKinds pins that the
// /dashboard/safe-releases recent-audit table renders exactly the 5
// audit kinds PR-A widened into deployment_audit_kind_chk
// (migrations/20260905000000000). A new kind added to the migration that this
// helper forgets to admit would render as "missing" — this test
// fails so the operator's view is never silently incomplete.
func TestSafeReleasesAuditKind_PinsPRAKinds(t *testing.T) {
	admit := []state.DeploymentAuditKind{
		state.DeployRolloutStarted,
		state.DeployRolloutCompleted,
		state.DeployRolloutAborted,
		state.DeployCanaryStepAdvanced,
		state.DeployAlertRuleFired,
	}
	for _, k := range admit {
		if !safeReleasesAuditKind(k) {
			t.Errorf("safeReleasesAuditKind(%q) = false; want true (PR-A widened kind)", k)
		}
	}
	// Pre-PR-A kinds stay excluded — the recent-audit table is
	// scoped to the operator's canary/safedeploy lifecycle, NOT the
	// generic deployment_audit stream (which /dashboard/deployments
	// already covers).
	reject := []state.DeploymentAuditKind{
		state.DeployCreated,
		state.DeploySourceRef,
		state.DeployLocalTarball,
		state.DeployTrafficChanged,
		state.DeployHealthProbeFailed,
		state.DeployHealthRecovered,
		state.DeployRolledBack,
		state.DeployRemoved,
		"",
	}
	for _, k := range reject {
		if safeReleasesAuditKind(k) {
			t.Errorf("safeReleasesAuditKind(%q) = true; want false (out of scope)", k)
		}
	}
}

// TestSafeReleasesAlertMetric_PinsPRBMetrics pins that the
// /dashboard/safe-releases active-alerts table renders exactly the
// 4 alert metric kinds PR-B introduced. Drift between this
// admission gate and pkg/state.AlertMetric* / pkg/api.AllowedAlertRuleMetrics
// / the catalog seed migrations/20260905000000001 fails here rather than at
// the dashboard smoke-test.
func TestSafeReleasesAlertMetric_PinsPRBMetrics(t *testing.T) {
	admit := []string{
		string(state.AlertMetricCanaryStuckStep),
		string(state.AlertMetricSafedeployAuditEmitFailing),
		string(state.AlertMetricDeploymentAuditGCFailing),
		string(state.AlertMetricCanaryFleetInFlightHigh),
	}
	for _, m := range admit {
		if !safeReleasesAlertMetric(m) {
			t.Errorf("safeReleasesAlertMetric(%q) = false; want true (PR-B metric)", m)
		}
	}
	reject := []string{
		string(state.AlertMetricErrorRate),
		string(state.AlertMetricAPIUp),
		string(state.AlertMetricFailedDeployments),
		"",
		"bogus_metric",
	}
	for _, m := range reject {
		if safeReleasesAlertMetric(m) {
			t.Errorf("safeReleasesAlertMetric(%q) = true; want false (out of scope)", m)
		}
	}
}
