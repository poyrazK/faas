package appmetrics

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

// RequestHealth is confirmed only when Source is Prometheus. Reason is a safe
// closed vocabulary; raw query/backend errors never enter the health response.
type RequestHealth struct {
	RequestCount int64
	ServerErrors int64
	ErrorRatePct float64
	AsOf         string
	Source       string
	Reason       string
}

// FetchRequestHealth reads the current serving releases only. Deployment label
// overflow and pre-routing 5xx cannot be assigned to an environment; they
// prevent confirmation rather than falling back to app-wide counters. All
// counts share a window; coverage requires two samples for every selected
// deployment, and freshness is the oldest selected deployment's latest sample.
func FetchRequestHealth(ctx context.Context, client *promql.Client, appID string, deploymentIDs []string) RequestHealth {
	out := RequestHealth{Source: SourceDegraded, Reason: "request_evidence_unavailable"}
	ids := slices.Clone(deploymentIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if client == nil {
		return out
	}
	if appID == "" || len(ids) == 0 || len(ids) > api.AppHealthMetricsDeploymentLimit {
		out.Reason = "request_scope_unavailable"
		return out
	}
	labels := make([]string, len(ids))
	for n, id := range ids {
		if id == "" || id == "__other__" {
			out.Reason = "request_scope_unavailable"
			return out
		}
		labels[n] = regexp.QuoteMeta(id)
	}
	selector := fmt.Sprintf(`gateway_request_duration_by_deployment_seconds_count{app=%q,deployment=~%q}`, appID, strings.Join(labels, "|"))
	count, err := client.QueryScalar(ctx, fmt.Sprintf(`sum(increase(%s[%s]))`, selector, api.AppHealthMetricsRange))
	if errors.Is(err, promql.ErrNoData) {
		out.Reason = requestsUnobservedReason(ctx, client)
		return out
	}
	if err != nil || !validHealthCount(count) {
		return out
	}
	errorSelector := strings.TrimSuffix(selector, "}") + `,class="5xx"}`
	failures, err := client.QueryScalar(ctx, fmt.Sprintf(`(sum(increase(%s[%s])) or vector(0))`, errorSelector, api.AppHealthMetricsRange))
	if err != nil || !validHealthCount(failures) || failures > count {
		return out
	}
	// A new status-class series with only one scrape cannot produce an
	// increase. Exclude that partial coverage instead of hiding its errors.
	coverageQuery := fmt.Sprintf(`count(count by (deployment) (increase(%s[%s])) and on (deployment) count by (deployment) (%s)) - (count(%s unless (count_over_time(%s[%s]) >= %d)) or vector(0))`, selector, api.AppHealthMetricsRange, selector, selector, selector, api.AppHealthMetricsRange, api.AppHealthCounterMinSamples)
	coverage, err := client.QueryScalar(ctx, coverageQuery)
	if errors.Is(err, promql.ErrNoData) && count == 0 {
		// The release's series came back after a gateway restart with no
		// request since: one scrape has no increase. That is an idle
		// release, not missing telemetry (prod 2026-11-23: health read
		// "unknown" for minutes after every gateway restart).
		out.Reason = requestsUnobservedReason(ctx, client)
		return out
	}
	if err != nil {
		return out
	}
	if coverage != float64(len(ids)) {
		out.Reason = "request_coverage_incomplete"
		return out
	}
	seconds, err := client.QueryScalar(ctx, fmt.Sprintf(`min(max by (deployment) (timestamp(%s)))`, selector))
	if err != nil || !validHealthCount(seconds) || seconds <= 0 || seconds >= float64(math.MaxInt64)/float64(time.Second) {
		return out
	}
	// Unattributed traffic blocks confirmation unless the gateway answered it
	// itself before routing. "__other__" is release traffic past this
	// gateway's deployment-label cap, so any class of it blocks. The empty
	// label is pre-routing: there only a 5xx blocks. Its 2xx/3xx/4xx are
	// responses the gateway gave without reaching a release, mostly platform
	// auth refusals of a client with no API key, plus CORS preflights and
	// edge-answered health checks. They say nothing about the selected
	// releases, yet one keyless uptime monitor left an app "unconfirmed" for
	// five minutes after every probe (prod 2026-11-23: a 401 GET /healthz
	// every ~15 min). These requests were never part of count or failures;
	// this only narrows which of them withhold confirmation.
	overflowSelector := fmt.Sprintf(`gateway_request_duration_by_deployment_seconds_count{app=%q,deployment="__other__"}`, appID)
	preRoutingSelector := fmt.Sprintf(`gateway_request_duration_by_deployment_seconds_count{app=%q,deployment="",class="5xx"}`, appID)
	unassignedQuery := unattributedQuery(overflowSelector) + " + " + unattributedQuery(preRoutingSelector)
	unassigned, err := client.QueryScalar(ctx, unassignedQuery)
	if err != nil || !validHealthCount(unassigned) {
		return out
	}
	if unassigned > 0 {
		out.Reason = "request_coverage_incomplete"
		return out
	}
	out.RequestCount = int64(SafeRoundNonNeg(count))
	out.ServerErrors = int64(SafeRoundNonNeg(failures))
	// The rate is of the counts it is shown with: increase() extrapolates,
	// and "24 of 28 requests returned 5xx (86.75%)" read as a miscount
	// (prod hunt r33).
	if out.RequestCount > 0 {
		out.ErrorRatePct = float64(out.ServerErrors) / float64(out.RequestCount) * 100
	}
	out.AsOf = time.Unix(0, int64(seconds*float64(time.Second))).UTC().Format(time.RFC3339Nano)
	out.Source, out.Reason = SourcePrometheus, ""
	return out
}

// unattributedQuery counts selector's requests in the health window. It adds
// the current value of a newly introduced series; otherwise its first request
// could disappear behind an empty increase.
func unattributedQuery(selector string) string {
	return fmt.Sprintf(`(sum(increase(%s[%s])) or vector(0)) + (sum(%s unless (count_over_time(%s[%s]) >= %d)) or vector(0))`, selector, api.AppHealthMetricsRange, selector, selector, api.AppHealthMetricsRange, api.AppHealthCounterMinSamples)
}

func validHealthCount(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value < float64(math.MaxInt64)
}

// RequestsUnobservedReason is a serving release with no request series yet:
// the gateway creates a release's counter on its first request, so an app
// nobody called since its deploy (or since the gateway restarted) has none.
// That is reported only while the gateway exports the series for other apps;
// with none anywhere the telemetry itself is missing and stays unavailable
// (prod 2026-11-16: an idle gRPC service read "telemetry could not be read").
const RequestsUnobservedReason = "requests_unobserved"

func requestsUnobservedReason(ctx context.Context, client *promql.Client) string {
	live, err := client.QueryScalar(ctx, `count(gateway_request_duration_by_deployment_seconds_count)`)
	if err != nil || !validHealthCount(live) || live < 1 {
		return "request_evidence_unavailable"
	}
	return RequestsUnobservedReason
}
