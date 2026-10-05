package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

func customerCLIReport() api.RouteHealthReport {
	r := cliHealthReport("healthy")
	for i := range r.Routes[0].Windows {
		r.Routes[0].Windows[i].Candidate.Requests = 10000
		r.Routes[0].Windows[i].Candidate.ServerErrors = 20
		r.Routes[0].Windows[i].Candidate.ErrorRate = .002
		r.Routes[0].Windows[i].Stable.Requests = 10000
	}
	f := api.RouteHealthFinding{Method: "POST", Path: "/checkout", Windows: routehealth.Windows(r.CheckedAt)}
	for i := range f.Windows {
		f.Windows[i].Candidate.Requests = 100
		f.Windows[i].Candidate.ServerErrors = 20
		f.Windows[i].Stable.Requests = 100
	}
	r.Customers = &api.RouteCustomerHealthReport{GroupBy: "tenant", DetailsIncluded: true, Routes: []api.RouteCustomerHealthRoute{{Method: "POST", Path: "/checkout", ObservedCustomers: 1, Candidate: api.RouteCustomerHealthAttribution{IdentifiedRequests: 200, UnattributedRequests: 19800}, Stable: api.RouteCustomerHealthAttribution{IdentifiedRequests: 200, UnattributedRequests: 19800}, Customers: []api.RouteCustomerHealthCohort{{CustomerID: "44444444-4444-4444-8444-444444444444", Health: f}}}}}
	routehealth.EvaluateCustomers(&r, "")
	return r
}

func TestCustomerHealthCLIReportsPrivacyAndAdvisoryExit(t *testing.T) {
	for _, scenario := range []string{"default", "details", "consumer", "human", "unrequested", "false_verdict", "wrong_group", "bad_counts", "bad_options", "bad_dimension", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			report := customerCLIReport()
			if scenario == "consumer" {
				report.Customers.GroupBy = "consumer"
			}
			if scenario == "false_verdict" {
				report.Customers.Status = "healthy"
			}
			if scenario == "wrong_group" {
				report.Customers.GroupBy = "consumer"
			}
			if scenario == "bad_counts" {
				report.Customers.Routes[0].Candidate.IdentifiedRequests++
			}
			if scenario == "missing" {
				report.Customers = nil
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if scenario != "unrequested" && r.URL.Query().Get("customers") != "true" {
					t.Error("missing customer query")
				}
				if scenario == "details" && r.URL.Query().Get("customer_details") != "true" {
					t.Error("details query")
				}
				if scenario == "consumer" && r.URL.Query().Get("customer_group_by") != "consumer" {
					t.Error("dimension query")
				}
				if scenario == "unrequested" && r.URL.RawQuery != "" {
					t.Error("default query changed")
				}
				writeJSONTest(w, report)
			}))
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
			if scenario != "unrequested" && scenario != "bad_options" {
				args = append(args, "--customers")
			}
			if scenario == "details" || scenario == "bad_options" {
				args = append(args, "--customer-details")
			}
			if scenario == "consumer" {
				args = append(args, "--customer-group-by", "consumer")
			}
			if scenario == "bad_dimension" {
				args = append(args, "--customer-group-by", "arbitrary")
			}
			want := 0
			if scenario == "false_verdict" || scenario == "wrong_group" || scenario == "bad_counts" || scenario == "bad_options" || scenario == "bad_dimension" || scenario == "missing" {
				want = 1
			}
			if code := run(args); code != want {
				t.Fatalf("exit %d want %d: %s", code, want, out.String())
			}
			if scenario == "bad_options" || scenario == "bad_dimension" {
				if calls != 0 {
					t.Fatal("invalid options reached server")
				}
				return
			}
			if scenario != "details" && strings.Contains(out.String(), "44444444") {
				t.Fatal("customer identity leaked")
			}
			if scenario == "details" && !strings.Contains(out.String(), "44444444") {
				t.Fatal("requested identity missing")
			}
			if scenario == "human" && (!strings.Contains(out.String(), "advisory") || !strings.Contains(out.String(), "20/100") || !strings.Contains(out.String(), "ID hidden")) {
				t.Fatal("human evidence missing")
			}
			if scenario == "unrequested" && strings.Contains(out.String(), `"customers"`) {
				t.Fatal("unexpected evidence exposed")
			}
		})
	}
}
