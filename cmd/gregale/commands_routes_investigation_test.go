package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

func cliInvestigation(opts api.RouteHealthInvestigationOptions) api.RouteHealthInvestigation {
	r := cliClientErrorReport()
	i := routehealth.NewInvestigation(r, r.Routes[0], opts.Selection())
	i.EvidenceStatus = "observed"
	for j := range i.Windows {
		w := &i.Windows[j]
		w.Candidate.MatchingRequests, w.Candidate.ObservedRows, w.Candidate.ExamplesTruncated = 20, 4, true
		for k := 0; k < i.ExamplesLimit; k++ {
			id := uuid.NewString()
			w.Candidate.Examples = append(w.Candidate.Examples, api.RouteHealthInvestigationExample{TelemetryID: id, ReceivedAt: w.Start.Add(time.Second), Status: 403, RepresentedRequests: 5, LatencyMS: 100, TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", EvidencePath: "/v1/apps/demo/debug/requests/" + id + "/evidence"})
		}
	}
	return i
}

func TestRouteInvestigationCLIWireExportAndEvidenceBinding(t *testing.T) {
	for _, scenario := range []string{"json", "human", "customer", "out", "overwrite", "wrong_window", "wrong_side_counts", "wrong_code", "wrong_target", "wrong_link", "duplicate", "unexpected_identities", "invalid_filter"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			opts := api.RouteHealthInvestigationOptions{Method: "POST", Path: "/checkout", StatusCode: 403}
			if scenario == "customer" {
				opts.CustomerID = "44444444-4444-4444-8444-444444444444"
			}
			report := cliInvestigation(opts)
			if scenario == "human" {
				for i := range report.Windows {
					report.Windows[i].Start = report.Windows[i].Start.In(time.FixedZone("database", 3*60*60))
					report.Windows[i].End = report.Windows[i].End.In(time.FixedZone("database", 3*60*60))
				}
			}
			switch scenario {
			case "wrong_window":
				report.Windows[0].End = report.Windows[0].End.Add(time.Second)
			case "wrong_side_counts":
				report.Windows[0].Candidate.MatchingRequests++
			case "wrong_code":
				report.Windows[0].Candidate.Examples[0].Status = 422
			case "wrong_target":
				report.Selection.CustomerID = "44444444-4444-4444-8444-444444444444"
			case "wrong_link":
				report.Windows[0].Candidate.Examples[0].EvidencePath = "/v1/apps/other/debug/requests/row/evidence"
			case "duplicate":
				report.Windows[1].Candidate.Examples[0] = report.Windows[0].Candidate.Examples[0]
			case "unexpected_identities":
				report.Report.Customers = &api.RouteCustomerHealthReport{}
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/v1/apps/demo/route-health/deployments/"+healthCandidateID+"/investigation" || r.URL.Query().Get("method") != "POST" || r.URL.Query().Get("path") != "/checkout" || r.URL.Query().Get("status_code") != "403" || r.URL.Query().Get("customer_id") != opts.CustomerID {
					t.Error("investigation request lost scope")
				}
				if scenario == "customer" && r.URL.Query().Get("customer_group_by") != "tenant" {
					t.Error("customer dimension was not normalized")
				}
				writeJSONTest(w, report)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			var out bytes.Buffer
			old := osStdout
			osStdout = &out
			t.Cleanup(func() { osStdout = old })
			args := []string{"routes", "health", "investigate", "demo", "--deployment", healthCandidateID, "--route", "POST /checkout", "--status", "403"}
			if scenario != "human" {
				args = append(args, "--json")
			}
			if scenario == "customer" {
				args = append(args, "--customer-id", opts.CustomerID)
			}
			if scenario == "invalid_filter" {
				args = append(args, "--customer-group-by", "tenant")
			}
			file := filepath.Join(t.TempDir(), "investigation.json")
			if scenario == "out" || scenario == "overwrite" {
				args = append(args, "--out", file)
			}
			if scenario == "overwrite" {
				if err := os.WriteFile(file, []byte("preserved"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			want := 1
			if scenario == "json" || scenario == "human" || scenario == "customer" || scenario == "out" {
				want = 0
			}
			if code := run(args); code != want {
				t.Fatalf("exit %d want %d: %s", code, want, out.String())
			}
			if scenario == "json" {
				var got api.RouteHealthInvestigation
				if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Status != "regressed" || got.Report.Status != "healthy" || got.Windows[0].Candidate.MatchingRequests != 20 {
					t.Fatal("investigation output lost independent signal or weights")
				}
			}
			if scenario == "human" && (!strings.Contains(out.String(), "gregale debug requests inspect demo") || !strings.Contains(out.String(), "gregale debug requests trace demo") || !strings.Contains(out.String(), "represents 5 requests")) {
				t.Fatal("request investigation links or collapse disclosure missing")
			}
			if scenario == "human" && !strings.Contains(out.String(), report.Finding.Windows[0].Start.UTC().Format("15:04:05Z")) {
				t.Fatal("database timezone was printed as UTC without conversion")
			}
			if scenario == "out" {
				body, err := os.ReadFile(file)
				info, statErr := os.Stat(file)
				if err != nil || statErr != nil || !json.Valid(body) || info.Mode().Perm() != 0600 {
					t.Fatal("export was not private JSON")
				}
			}
			if (scenario == "overwrite" || scenario == "invalid_filter") && calls != 0 {
				t.Fatal("invalid local intent sent to the API")
			}
		})
	}
}
