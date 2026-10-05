package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/debugger"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

func cliLatencyInvestigation(t *testing.T, opts api.RouteHealthInvestigationOptions) api.RouteHealthInvestigation {
	t.Helper()
	r := cliHealthReport("healthy")
	r.Routes[0].CheckLatency = true
	candidate, stable := float64(600), float64(100)
	for i := range r.Routes[0].Windows {
		w := &r.Routes[0].Windows[i]
		w.Candidate.P95LatencyMS = &candidate
		w.Stable.P95LatencyMS = &stable
	}
	routehealth.Evaluate(&r, r.ObservationAnchor, "")
	out := routehealth.NewInvestigation(r, r.Routes[0], opts.Selection())
	out.EvidenceStatus = "observed"
	for i := range out.Windows {
		w := &out.Windows[i]
		samples := make([][]debugger.RouteLatencyRow, 2)
		for j, side := range []*api.RouteHealthInvestigationSide{&w.Candidate, &w.Stable} {
			side.MatchingRequests, side.ObservedRows = 100, 2
			ms, latency := int64(500), int64(600)
			if j == 1 {
				ms, latency = 40, 100
			}
			for k := 0; k < 2; k++ {
				id := uuid.NewString()
				at := w.Start.Add(time.Second)
				e := api.RouteHealthInvestigationExample{TelemetryID: id, ReceivedAt: at, Status: 200, LatencyMS: latency, RepresentedRequests: 50, EvidencePath: "/v1/apps/demo/debug/requests/" + id + "/evidence"}
				side.Examples = append(side.Examples, e)
				span := debugger.StoredSpan{SpanID: "db", StartTimeUnixNano: uint64(at.UnixNano()), EndTimeUnixNano: uint64(at.Add(time.Duration(ms) * time.Millisecond).UnixNano()), DurationNanos: uint64(ms) * uint64(time.Millisecond), Attributes: map[string]string{"gregale.dependency.type": "managed_binding", "gregale.dependency.kind": "postgres"}}
				body, _ := json.Marshal([]debugger.StoredSpan{span})
				samples[j] = append(samples[j], debugger.RouteLatencyRow{Example: e, Spans: body})
			}
		}
		w.Diagnostics = debugger.RouteLatencyDiagnostics(samples[0], samples[1], 2, 2)
	}
	return out
}

func TestRouteLatencyInvestigationSharedSDKFixture(t *testing.T) {
	body, err := os.ReadFile("../../tests/fixtures/route-latency-investigation.json")
	if err != nil {
		t.Fatal(err)
	}
	var r api.RouteHealthInvestigation
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatal(err)
	}
	opts := api.RouteHealthInvestigationOptions{Method: r.Selection.Method, Path: r.Selection.Path, Signal: "latency", CustomerID: r.Selection.CustomerID, CustomerGroupBy: r.Selection.CustomerGroupBy}
	if err := validateCLIInvestigation(r, opts, "demo", r.Report.DeploymentID); err != nil {
		t.Fatal(err)
	}
}

func TestRouteLatencyCLIWireHumanAndRejectsForgedDiagnostics(t *testing.T) {
	for _, scenario := range []string{"human", "json", "customer", "delta", "sample_count", "missing_diagnostics", "wrong_link", "cross_side", "unexpected_stage", "exclusive_missing", "wrong_signal", "wrong_route_p95", "mixed_filters"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			opts := api.RouteHealthInvestigationOptions{Method: "POST", Path: "/checkout", Signal: "latency"}
			if scenario == "customer" {
				opts.CustomerID = uuid.NewString()
			}
			r := cliLatencyInvestigation(t, opts)
			switch scenario {
			case "delta":
				*r.Windows[0].Diagnostics.Dependencies[0].P95DeltaMS = 999
			case "sample_count":
				r.Windows[0].Diagnostics.Candidate.SampledRequests++
			case "missing_diagnostics":
				r.Windows[0].Diagnostics = nil
			case "wrong_link":
				r.Windows[0].Diagnostics.Dependencies[0].Candidate.Examples[0].EvidencePath = "/v1/apps/foreign/debug/requests/row/evidence"
			case "cross_side":
				r.Windows[0].Diagnostics.Dependencies[0].Stable.Examples[0] = r.Windows[0].Diagnostics.Dependencies[0].Candidate.Examples[0]
			case "unexpected_stage":
				r.Windows[0].Diagnostics.Candidate.GuestP95MS = new(int64)
			case "exclusive_missing":
				r.Windows[0].Diagnostics.Dependencies[0].Candidate.ExclusiveP95MS = nil
			case "wrong_route_p95":
				r.Windows[0].Candidate.Examples[0].LatencyMS++
			case "wrong_signal":
				r.Selection.Signal = ""
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls++
				if req.Method != "GET" || req.URL.Query().Get("signal") != "latency" || req.URL.Query().Get("status_code") != "0" || req.URL.Query().Get("customer_id") != opts.CustomerID {
					t.Error("latency request lost scope")
				}
				writeJSONTest(w, r)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			var output bytes.Buffer
			old := osStdout
			osStdout = &output
			t.Cleanup(func() { osStdout = old })
			args := []string{"routes", "health", "investigate", "demo", "--deployment", healthCandidateID, "--route", "POST /checkout", "--signal", "latency"}
			if scenario != "human" {
				args = append(args, "--json")
			}
			if opts.CustomerID != "" {
				args = append(args, "--customer-id", opts.CustomerID)
			}
			if scenario == "mixed_filters" {
				args = append(args, "--status", "403")
			}
			want := 1
			if scenario == "human" || scenario == "json" || scenario == "customer" {
				want = 0
			}
			if code := run(args); code != want {
				t.Fatalf("exit %d want %d: %s", code, want, output.String())
			}
			if scenario == "human" {
				for _, text := range []string{"Full observed route p95: candidate 600.0ms; stable 100.0ms", "managed_binding/postgres", "change +460ms", "missing spans:", "distinct wakes", "cannot be added", "gregale debug requests inspect demo"} {
					if !strings.Contains(output.String(), text) {
						t.Fatalf("missing human evidence %q: %s", text, output.String())
					}
				}
			}
			if scenario == "mixed_filters" && calls != 0 {
				t.Fatal("invalid filters sent to API")
			}
		})
	}
}
