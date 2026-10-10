package routemonitor

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// OnViolation normalizes the saved action; the empty value is report.
func OnViolation(value string) string {
	if value == "" {
		return "report"
	}
	return value
}

// Skip reasons are durable on the incident and stable for API clients.
const (
	RollbackSkipLatencyOnly      = "latency_only_violation"
	RollbackSkipOutsideWindow    = "outside_rollback_window"
	RollbackSkipNoBaseline       = "no_healthy_baseline"
	RollbackSkipTargetIneligible = "rollback_target_ineligible"
)

// RollbackDecision is what a store needs to claim or skip one incident.
type RollbackDecision struct {
	// Eligible incidents are claimed; Skip carries a durable skip reason.
	// Neither set means "not this incident" (already decided or not open).
	Eligible bool
	Skip     string
	Route    string
	Target   string
}

// DecideRollback applies ADR-952 to one open incident of a fully serving
// deployment. releasedAt is when that deployment last started a traffic stage
// or completed its rollout. It never inspects telemetry beyond the immutable
// opening report, so the decision is reproducible from the saved incident.
func DecideRollback(c api.RouteMonitorConfig, i api.RouteMonitorIncident, releasedAt time.Time) RollbackDecision {
	if !c.Enabled || OnViolation(c.OnViolation) != "rollback" || i.Status != "open" || i.Rollback != nil || i.Revision != c.Revision {
		return RollbackDecision{}
	}
	route := ""
	for _, f := range i.OpeningReport.Routes {
		if f.ErrorStatus == "violated" {
			route = f.Route.Method + " " + f.Route.Path
			break
		}
	}
	switch {
	case route == "":
		return RollbackDecision{Skip: RollbackSkipLatencyOnly}
	case releasedAt.IsZero() || i.OpenedAt.Sub(releasedAt) > api.RouteMonitorRollbackWindow:
		return RollbackDecision{Skip: RollbackSkipOutsideWindow, Route: route}
	case i.Baseline == nil || i.Baseline.DeploymentID == "" || i.Baseline.DeploymentID == i.DeploymentID:
		return RollbackDecision{Skip: RollbackSkipNoBaseline, Route: route}
	}
	return RollbackDecision{Eligible: true, Route: route, Target: i.Baseline.DeploymentID}
}

// ReleasedAt is the latest traffic transition of a serving deployment, the
// same deployment-owned anchors the monitor report uses.
func ReleasedAt(created time.Time, stageStarted, rolloutCompleted *time.Time) time.Time {
	at := created
	for _, t := range []*time.Time{stageStarted, rolloutCompleted} {
		if t != nil && t.After(at) {
			at = *t
		}
	}
	return at
}
