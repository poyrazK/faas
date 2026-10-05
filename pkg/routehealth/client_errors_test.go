package routehealth

import (
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func clientErrorReport(scenario string) api.RouteHealthReport {
	now := time.Now().UTC()
	anchor := now.Add(-time.Hour)
	windows := Windows(now)
	f := api.RouteHealthFinding{Method: "POST", Path: "/checkout", WatchStatuses: []int{403, 422}, Windows: windows, ClientErrors: &api.RouteHealthClientErrorReport{}}
	for _, code := range f.WatchStatuses {
		signal := api.RouteHealthClientErrorFinding{StatusCode: code, Windows: []api.RouteHealthClientErrorWindow{}}
		for i, w := range windows {
			candidate, stable := int64(100), int64(100)
			responses, baseline := int64(0), int64(0)
			switch scenario {
			case "regressed":
				if code == 403 {
					responses = 20
				}
			case "expected":
				responses, baseline = 50, 50
			case "boundary":
				if code == 403 {
					responses = 5
				}
			case "sparse":
				candidate = 19
				if code == 403 {
					responses = 19
				}
			case "missing_side":
				stable = 0
			case "mixed":
				if code == 403 && i == 0 || code == 422 && i == 1 {
					responses = 20
				}
			case "below_delta":
				responses, baseline = 4, 1
			}
			f.Windows[i].Candidate.Requests, f.Windows[i].Stable.Requests = candidate, stable
			signal.Windows = append(signal.Windows, api.RouteHealthClientErrorWindow{Start: w.Start, End: w.End, Candidate: api.RouteHealthStatusCounts{Requests: candidate, Responses: responses}, Stable: api.RouteHealthStatusCounts{Requests: stable, Responses: baseline}})
		}
		f.ClientErrors.Statuses = append(f.ClientErrors.Statuses, signal)
	}
	return api.RouteHealthReport{Mode: "enforce", Status: "healthy", Reason: "comparisons_healthy", CheckedAt: now, ObservationAnchor: &anchor, StableDeploymentID: "stable", Routes: []api.RouteHealthFinding{f}}
}

func TestClientErrorsIndependentSignalsAndAdvisoryIsolation(t *testing.T) {
	for _, scenario := range []string{"regressed", "expected", "boundary", "sparse", "missing_side", "mixed", "below_delta", "anchor", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			r := clientErrorReport(scenario)
			if scenario == "anchor" {
				anchor := r.CheckedAt
				r.ObservationAnchor = &anchor
			}
			unavailable := ""
			if scenario == "unavailable" {
				unavailable = "telemetry_not_entitled"
				r.StableDeploymentID = ""
				r.Reason = unavailable
			}
			before := Decision(r)
			EvaluateClientErrors(&r, unavailable)
			want := "healthy"
			if scenario == "regressed" || scenario == "boundary" {
				want = "regressed"
			}
			if scenario == "sparse" || scenario == "missing_side" || scenario == "mixed" || scenario == "anchor" || scenario == "unavailable" {
				want = "unknown"
			}
			if r.ClientErrorStatus != want {
				t.Fatalf("%+v", r.Routes[0].ClientErrors)
			}
			if Decision(r) != before || r.Status != "healthy" {
				t.Fatal("4xx changed canary health decision")
			}
			if err := ValidateClientErrors(r); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestClientErrorEvidenceRejectsForgeryAndWrongBinding(t *testing.T) {
	for _, scenario := range []string{"false_healthy", "counts", "rate", "window", "duplicate", "threshold", "missing", "summaries", "pooled_counts", "overlaps_server_errors"} {
		t.Run(scenario, func(t *testing.T) {
			r := clientErrorReport("regressed")
			EvaluateClientErrors(&r, "")
			c := r.Routes[0].ClientErrors
			switch scenario {
			case "false_healthy":
				c.Status = "healthy"
			case "counts":
				c.Statuses[0].Windows[0].Candidate.Responses = 101
			case "rate":
				c.Statuses[0].Windows[0].Candidate.Rate = 0
			case "window":
				c.Statuses[0].Windows[0].Start = c.Statuses[0].Windows[0].Start.Add(time.Second)
			case "duplicate":
				c.Statuses[1].StatusCode = 403
			case "threshold":
				c.MinimumRequests = 1
			case "missing":
				r.Routes[0].ClientErrors = nil
			case "summaries":
				r.ClientErrorStatus = "healthy"
			case "overlaps_server_errors":
				r.Routes[0].Windows[0].Candidate.ServerErrors = 90
			case "pooled_counts":
				for i := range c.Statuses {
					c.Statuses[i].Windows[0].Candidate.Responses = 80
					c.Statuses[i].Windows[0].Candidate.Rate = .8
				}
			}
			if ValidateClientErrors(r) == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
}

func TestWatchStatusesValidationAndDeepCopy(t *testing.T) {
	for _, codes := range [][]int{{401, 403, 404, 422, 429}, {}, nil} {
		if err := ValidateWatchStatuses(codes); err != nil {
			t.Fatal(err)
		}
	}
	for _, codes := range [][]int{{403, 403}, {400}, {500}, {200}, {401, 403, 404, 422, 429, 401}} {
		if ValidateWatchStatuses(codes) == nil {
			t.Fatal("invalid selectors accepted")
		}
	}
	original := []api.RouteHealthRoute{{Method: "GET", Path: "/orders", WatchStatuses: []int{403}}}
	cloned := CloneRoutes(original)
	if !RoutesEqual(original, cloned) {
		t.Fatal("cloning changed selectors")
	}
	cloned[0].WatchStatuses[0] = 422
	if original[0].WatchStatuses[0] != 403 || RoutesEqual(original, cloned) {
		t.Fatal("selector mutation crossed ownership")
	}
	r := clientErrorReport("regressed")
	cohort := r.Routes[0]
	cohort.WatchStatuses = slices.Clone(cohort.WatchStatuses)
	r.Customers = &api.RouteCustomerHealthReport{GroupBy: "tenant", Routes: []api.RouteCustomerHealthRoute{{Method: "POST", Path: "/checkout", ObservedCustomers: 1, Customers: []api.RouteCustomerHealthCohort{{CustomerID: "private", Health: cohort}}}}}
	EvaluateCustomers(&r, "")
	if r.Customers.Status != "regressed" || r.Customers.Routes[0].Customers[0].Health.Status != "healthy" || r.Customers.Routes[0].Customers[0].Health.ClientErrors.Status != "regressed" || r.Customers.Routes[0].Customers[0].CustomerID != "" {
		t.Fatal("advisory customer error or privacy")
	}
}
