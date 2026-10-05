package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

func cliClientErrorReport() api.RouteHealthReport {
	r := cliHealthReport("healthy")
	anchor := r.CheckedAt.Add(-time.Hour)
	r.ObservationAnchor = &anchor
	f := &r.Routes[0]
	f.WatchStatuses = []int{403, 422}
	f.ClientErrors = &api.RouteHealthClientErrorReport{}
	for _, code := range f.WatchStatuses {
		signal := api.RouteHealthClientErrorFinding{StatusCode: code}
		for _, w := range f.Windows {
			responses := int64(0)
			if code == 403 {
				responses = 20
			}
			signal.Windows = append(signal.Windows, api.RouteHealthClientErrorWindow{Start: w.Start, End: w.End, Candidate: api.RouteHealthStatusCounts{Requests: w.Candidate.Requests, Responses: responses}, Stable: api.RouteHealthStatusCounts{Requests: w.Stable.Requests}})
		}
		f.ClientErrors.Statuses = append(f.ClientErrors.Statuses, signal)
	}
	routehealth.EvaluateClientErrors(&r, "")
	return r
}

func TestClientErrorCLIAdvisoryVerdictsAndBinding(t *testing.T) {
	for _, scenario := range []string{"json", "human", "customer", "wrong_counts", "false_verdict", "missing", "wrong_code"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			report := cliClientErrorReport()
			if scenario == "wrong_counts" {
				report.Routes[0].ClientErrors.Statuses[0].Windows[0].Candidate.Requests = 200
			}
			if scenario == "false_verdict" {
				report.ClientErrorStatus = "healthy"
			}
			if scenario == "missing" {
				report.Routes[0].ClientErrors = nil
			}
			if scenario == "wrong_code" {
				report.Routes[0].ClientErrors.Statuses[0].StatusCode = 429
			}
			if scenario == "customer" {
				body, _ := json.Marshal(report.Routes[0])
				var cohort api.RouteHealthFinding
				_ = json.Unmarshal(body, &cohort)
				report.Customers = &api.RouteCustomerHealthReport{GroupBy: "tenant", Routes: []api.RouteCustomerHealthRoute{{Method: "POST", Path: "/checkout", ObservedCustomers: 1, Candidate: api.RouteCustomerHealthAttribution{IdentifiedRequests: 200}, Stable: api.RouteCustomerHealthAttribution{IdentifiedRequests: 200}, Customers: []api.RouteCustomerHealthCohort{{CustomerID: "private", Health: cohort}}}}}
				routehealth.EvaluateCustomers(&report, "")
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSONTest(w, report) }))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			var out bytes.Buffer
			old := osStdout
			osStdout = &out
			t.Cleanup(func() { osStdout = old })
			args := []string{"routes", "health", "report", "demo", "--deployment", healthCandidateID, "--fail-on-unhealthy"}
			if scenario != "human" {
				args = append(args, "--json")
			}
			if scenario == "customer" {
				args = append(args, "--customers")
			}
			want := 0
			if scenario == "wrong_counts" || scenario == "false_verdict" || scenario == "missing" || scenario == "wrong_code" {
				want = 1
			}
			if code := run(args); code != want {
				t.Fatalf("exit %d want %d: %s", code, want, out.String())
			}
			if scenario == "human" && (!strings.Contains(out.String(), "HTTP 403: regressed") || !strings.Contains(out.String(), "20/100") || !strings.Contains(out.String(), "advisory")) {
				t.Fatal("missing watched status explanation")
			}
			if scenario == "json" {
				var result api.RouteHealthReport
				if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.ClientErrorStatus != "regressed" || result.Status != "healthy" {
					t.Fatalf("advisory result missing: %s", out.String())
				}
			}
			if strings.Contains(out.String(), "private") {
				t.Fatal("customer ID leakage")
			}
		})
	}
}
