package apphealth

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
)

func assessRequests(out *api.AppHealthResponse, e Evidence) {
	out.Requests = &api.AppHealthRequests{
		Coverage: "serving_deployments", DeploymentIDs: slices.Clone(out.ServingDeploymentIDs),
		WindowSeconds: int(api.AppHealthMetricsWindow / time.Second),
		Policy: api.AppHealthRequestPolicy{MinimumRequests: api.AppHealthMinRequests,
			MinimumServerErrors: api.AppHealthMinServerErrors, WarningErrorRatePct: api.AppHealthWarningErrorRatePct,
			UnhealthyErrorRatePct: api.AppHealthUnhealthyErrorRatePct},
	}
	if !e.MetricsAllowed {
		requestCheck(out, Unknown, "request_plan_restricted", "Request telemetry is unavailable on this plan; structural checks remain available.", "")
		return
	}
	if !e.DeploymentsKnown || len(out.ServingDeploymentIDs) == 0 {
		requestCheck(out, Unknown, "request_scope_unavailable", "No confirmed serving releases are available to scope request evidence.", "deployments")
		return
	}
	if e.Metrics.Source != appmetrics.SourcePrometheus {
		reason, detail := "request_evidence_unavailable", "Scoped request telemetry could not be read. Zero values are not treated as successful requests."
		switch e.Metrics.Reason {
		case "request_scope_unavailable":
			reason, detail = e.Metrics.Reason, "The serving release set exceeds the telemetry bound or cannot be identified."
		case "request_coverage_incomplete":
			reason, detail = e.Metrics.Reason, "Request coverage is incomplete: a serving release lacks samples or requests have no attributable deployment. App-wide metrics are not substituted."
		}
		requestCheck(out, Unknown, reason, detail, "metrics")
		return
	}
	at, err := time.Parse(time.RFC3339Nano, e.Metrics.AsOf)
	if err != nil || e.Now.Sub(at) > api.AppHealthEvidenceMaxAge || at.After(e.Now.Add(api.AppHealthEvidenceMaxAge)) {
		requestCheck(out, Unknown, "request_evidence_stale", "Scoped request telemetry has no current sample timestamp for every serving release.", "metrics")
		return
	}
	m := e.Metrics
	if math.IsNaN(m.ErrorRatePct) || math.IsInf(m.ErrorRatePct, 0) || m.ErrorRatePct < 0 || m.ErrorRatePct > 100 || m.RequestCount < 0 || m.ServerErrors < 0 || m.ServerErrors > m.RequestCount {
		requestCheck(out, Unknown, "request_evidence_invalid", "Scoped request telemetry contains invalid values.", "metrics")
		return
	}
	out.MetricsAsOf = at.UTC().Format(time.RFC3339Nano)
	out.Requests.Known, out.Requests.RequestCount = true, m.RequestCount
	out.Requests.ServerErrors, out.Requests.ErrorRatePct = m.ServerErrors, m.ErrorRatePct
	detail := fmt.Sprintf("%d of %d requests returned 5xx (%.2f%%) in the last 5 minutes across the current default-scope serving releases.", m.ServerErrors, m.RequestCount, m.ErrorRatePct)
	switch {
	case m.RequestCount == 0:
		requestCheck(out, NotApplicable, "requests_unexercised", "No requests were observed for the current serving releases in the last 5 minutes; request success has not been exercised.", "")
	case m.ServerErrors > 0 && m.RequestCount < api.AppHealthMinRequests:
		requestCheck(out, Unknown, "request_volume_insufficient", detail+" There are too few requests to classify error severity.", "errors")
	case m.ServerErrors >= api.AppHealthMinServerErrors && m.ErrorRatePct >= api.AppHealthUnhealthyErrorRatePct:
		requestCheck(out, Fail, "request_error_rate_severe", detail+" The severe error threshold is met.", "errors")
	case m.ServerErrors >= api.AppHealthMinServerErrors && m.ErrorRatePct >= api.AppHealthWarningErrorRatePct:
		requestCheck(out, Warning, "request_error_rate_elevated", detail+" The warning error threshold is met.", "errors")
	case m.ServerErrors > 0:
		requestCheck(out, Pass, "request_errors_below_threshold", detail+" Errors remain visible below the warning threshold.", "errors")
	default:
		requestCheck(out, Pass, "requests_observed", detail, "")
	}
}

func requestCheck(out *api.AppHealthResponse, status, reason, detail, action string) {
	add(out, "requests", status, detail, action, "")
	out.Checks[len(out.Checks)-1].Reason = reason
}
