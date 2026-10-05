package routehealth

import "github.com/onebox-faas/faas/pkg/api"

// EvaluateClientErrors adds live advisory verdicts without altering the health
// status used by Decision or automatic recovery. Codes are confirmed separately.
func EvaluateClientErrors(report *api.RouteHealthReport, unavailable string) {
	report.ClientErrorStatus, report.ClientErrorReason = "", ""
	for i := range report.Routes {
		route := &report.Routes[i]
		if len(route.WatchStatuses) == 0 {
			route.ClientErrors = nil
			continue
		}
		missing := route.ClientErrors == nil
		if missing {
			route.ClientErrors = &api.RouteHealthClientErrorReport{Statuses: []api.RouteHealthClientErrorFinding{}}
			for _, code := range route.WatchStatuses {
				f := api.RouteHealthClientErrorFinding{StatusCode: code, Windows: []api.RouteHealthClientErrorWindow{}}
				for _, w := range route.Windows {
					f.Windows = append(f.Windows, api.RouteHealthClientErrorWindow{Start: w.Start, End: w.End})
				}
				route.ClientErrors.Statuses = append(route.ClientErrors.Statuses, f)
			}
		}
		c := route.ClientErrors
		c.MinimumRequests, c.MinimumResponses = api.RouteHealthMinRequests, api.RouteHealthMinErrors
		c.RateFloor, c.RateDelta, c.RateFactor = api.RouteHealthErrorRateFloor, api.RouteHealthErrorRateDelta, api.RouteHealthErrorRateFactor
		c.Status, c.Reason = "healthy", "watched_status_comparisons_healthy"
		unavailableReason := unavailable
		if missing && unavailableReason == "" {
			unavailableReason = "status_evidence_unavailable"
		}
		for j := range c.Statuses {
			f := &c.Statuses[j]
			statuses := []string{}
			for k := range f.Windows {
				w := &f.Windows[k]
				candidate := api.RouteHealthCounts{Requests: w.Candidate.Requests, ServerErrors: w.Candidate.Responses}
				stable := api.RouteHealthCounts{Requests: w.Stable.Requests, ServerErrors: w.Stable.Responses}
				rate(&candidate)
				rate(&stable)
				w.Candidate.Rate, w.Stable.Rate = candidate.ErrorRate, stable.ErrorRate
				w.Status, w.Reason = errorWindow(api.RouteHealthWindowEvidence{Start: w.Start, End: w.End, Candidate: candidate, Stable: stable}, report.ObservationAnchor, unavailableReason)
				if w.Reason == "server_error_rate_increased" {
					w.Reason = "watched_status_rate_increased"
				}
				statuses = append(statuses, w.Status)
			}
			f.Status, f.Reason = consecutive(statuses, "consecutive_watched_status_regression")
			c.Status, c.Reason = combine(c.Status, c.Reason, f.Status, f.Reason)
		}
		if report.ClientErrorStatus == "" {
			report.ClientErrorStatus, report.ClientErrorReason = "healthy", "watched_status_comparisons_healthy"
		}
		report.ClientErrorStatus, report.ClientErrorReason = combine(report.ClientErrorStatus, report.ClientErrorReason, c.Status, c.Reason)
	}
}
