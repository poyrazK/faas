package migrationcutover

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
)

// Traffic uses the existing read-only monitor preview. Old staging windows,
// pooled pre-cutover history and customer cohorts cannot qualify a cutover.
func Traffic(preview api.RouteMonitorPreview, plan api.MigrationCutover, appID string) api.MigrationCutoverTraffic {
	out := limitedTraffic("traffic_evidence_unavailable")
	r := preview.Report
	if !preview.PreviewOnly || r.AppID != appID || r.DeploymentID != plan.DeploymentID || plan.HostnameVerifiedAt == nil || time.Since(r.CheckedAt) > api.MigrationCutoverCheckInterval {
		return out
	}
	out.CheckedAt = &r.CheckedAt
	if len(r.Routes) != 1 {
		return out
	}
	f := r.Routes[0]
	if f.Route.Method != "GET" || f.Route.Path != plan.SmokePath {
		return out
	}
	budget := plan.Request.Max5xxRateBPS
	f.Route.Max5xxRateBPS, f.Route.MaxP95MS = &budget, plan.Request.MaxP95MS
	f.PooledWindows = nil
	r.Routes = []api.RouteMonitorFinding{f}
	r.ObservationAnchor = plan.HostnameVerifiedAt
	r.Enabled = true
	unavailable := ""
	if preview.Report.Status == "unknown" && preview.Report.Reason != "evidence_incomplete_or_unsettled" {
		unavailable = preview.Report.Reason
	}
	routemonitor.Evaluate(&r, unavailable)
	out.Status, out.Reason = r.Status, r.Reason
	for _, w := range r.Routes[0].Windows {
		if w.Start.Before(*plan.HostnameVerifiedAt) {
			continue
		}
		out.Requests += w.Observed.Requests
		out.ServerErrors += w.Observed.ServerErrors
		if w.Observed.P95LatencyMS != nil && (out.WorstWindowP95MS == nil || *w.Observed.P95LatencyMS > *out.WorstWindowP95MS) {
			p95 := *w.Observed.P95LatencyMS
			out.WorstWindowP95MS = &p95
		}
	}
	return out
}
