package main

// adr: 456

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

const healthDecisionID = "44444444-4444-4444-8444-444444444444"

func cliSavedHealth(scenario string) api.RouteHealthHistoryEntry {
	r := cliLatencyReport(scenario)
	// Historical evidence may be arbitrarily old, but window bounds remain
	// tied to the saved check time rather than the reader's clock.
	r.CheckedAt = time.Date(2026, 9, 1, 12, 0, 45, 0, time.UTC)
	anchor := r.CheckedAt.Add(-time.Hour)
	r.ObservationAnchor = &anchor
	windows := routehealth.Windows(r.CheckedAt)
	for i := range windows {
		r.Routes[0].Windows[i].Start, r.Routes[0].Windows[i].End = windows[i].Start, windows[i].End
	}
	routehealth.Evaluate(&r, &anchor, "")
	e := api.RouteHealthHistoryEntry{Version: 1, ID: healthDecisionID, CheckedAt: r.CheckedAt, Source: "manual", TrafficPercent: 1, RequestedTrafficPercent: 10, Policy: routehealth.HistoryPolicy(), Report: r, Decision: routehealth.Decision(r)}
	e.Decision.HistoryID = e.ID
	return e
}

func TestRouteHealthExplainCLIHistoryAndValidation(t *testing.T) {
	for _, scenario := range []string{"regressed", "unknown", "healthy", "json", "empty", "detail", "cursor", "wrong_decision", "wrong_deployment", "wrong_policy", "false_healthy", "missing_anchor", "late_anchor", "duplicate", "wrong_cursor", "wrong_order", "abort", "abort_latency_only", "abort_manual", "wrong_purpose"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			entry := cliSavedHealth("regressed")
			if scenario == "healthy" || scenario == "missing_anchor" || scenario == "late_anchor" || scenario == "false_healthy" {
				entry = cliSavedHealth("healthy")
			}
			if scenario == "unknown" {
				entry = cliSavedHealth("unknown")
			}
			if strings.HasPrefix(scenario, "abort") {
				entry.Source, entry.Purpose, entry.RequestedTrafficPercent = "worker", "abort", 0
				entry.Report.OnRegression = "abort"
				if scenario != "abort_latency_only" {
					for i := range entry.Report.Routes[0].Windows {
						entry.Report.Routes[0].Windows[i].Candidate.ServerErrors = 10
					}
					routehealth.Evaluate(&entry.Report, entry.Report.ObservationAnchor, "")
				}
				entry.Decision = routehealth.AbortDecision(entry.Report)
				entry.Decision.HistoryID = entry.ID
				if scenario == "abort_manual" {
					entry.Source = "manual"
				}
			}
			switch scenario {
			case "wrong_purpose":
				entry.Purpose = "promote"
			case "wrong_decision":
				entry.Decision.Status = "allowed"
			case "wrong_deployment":
				entry.Report.DeploymentID = entry.Report.StableDeploymentID
			case "wrong_policy":
				entry.Policy.Version++
			case "false_healthy":
				*entry.Report.Routes[0].Windows[0].Candidate.P95LatencyMS = 500
			case "missing_anchor":
				entry.Report.ObservationAnchor = nil
			case "late_anchor":
				*entry.Report.ObservationAnchor = entry.CheckedAt
			}
			page := api.RouteHealthHistoryPage{AppID: entry.Report.AppID, DeploymentID: healthCandidateID, Entries: []api.RouteHealthHistoryEntry{entry}}
			if scenario == "empty" {
				page.Entries = []api.RouteHealthHistoryEntry{}
			}
			if scenario == "duplicate" {
				page.Entries = append(page.Entries, entry)
			}
			if scenario == "wrong_cursor" {
				page.NextCursor = "55555555-5555-4555-8555-555555555555"
			}
			if scenario == "wrong_order" {
				newer := entry
				newer.ID = "55555555-5555-4555-8555-555555555555"
				newer.Decision.HistoryID = newer.ID
				page.Entries = append(page.Entries, newer)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := "/v1/apps/demo/route-health/deployments/" + healthCandidateID + "/history"
				if scenario == "detail" {
					path += "/" + healthDecisionID
				}
				if r.Method != "GET" || r.URL.Path != path {
					t.Error("explain path binding")
				}
				if scenario != "detail" && r.URL.Query().Get("limit") != "5" {
					t.Error("explain page bound")
				}
				if scenario == "cursor" && r.URL.Query().Get("before") != "55555555-5555-4555-8555-555555555555" {
					t.Error("cursor lost")
				}
				if scenario == "detail" {
					writeJSONTest(w, entry)
				} else {
					writeJSONTest(w, page)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			var out bytes.Buffer
			old := osStdout
			osStdout = &out
			t.Cleanup(func() { osStdout = old })
			args := []string{"routes", "health", "explain", "demo", "--deployment", healthCandidateID}
			if scenario == "json" {
				args = append(args, "--json")
			}
			if scenario == "detail" {
				args = append(args, "--decision", healthDecisionID)
			}
			if scenario == "cursor" {
				args = append(args, "--before", "55555555-5555-4555-8555-555555555555")
			}
			valid := scenario == "abort" || scenario == "regressed" || scenario == "unknown" || scenario == "healthy" || scenario == "json" || scenario == "empty" || scenario == "detail" || scenario == "cursor"
			want := 1
			if valid {
				want = 0
			}
			if code := run(args); code != want {
				t.Fatalf("exit %d want %d: %s", code, want, out.String())
			}
			if !valid && out.Len() != 0 {
				t.Fatal("inconsistent saved evidence displayed")
			}
			if scenario == "regressed" || scenario == "detail" {
				for _, text := range []string{"Traffic was held", "500.0ms vs stable 120.0ms", "above 300ms budget", "Saved thresholds", "2026-09-01", healthDecisionID} {
					if !strings.Contains(out.String(), text) {
						t.Fatalf("missing %q: %s", text, out.String())
					}
				}
			}
			if scenario == "abort" && (!strings.Contains(out.String(), "stable traffic was restored") || !strings.Contains(out.String(), "aborted")) {
				t.Fatal("abort evidence not explained", out.String())
			}
			if scenario == "unknown" && !strings.Contains(out.String(), "too few observed requests") {
				t.Fatal("sample gap not explained")
			}
			if scenario == "json" {
				var got api.RouteHealthHistoryPage
				if json.Unmarshal(out.Bytes(), &got) != nil || got.Entries[0].ID != entry.ID {
					t.Fatal("JSON snapshot lost")
				}
			}
		})
	}
}

func TestRouteHealthExplainTimelineContext(t *testing.T) {
	before, after := cliSavedHealth("regressed"), cliSavedHealth("healthy")
	if got := routeHealthHistoryTransition(before, after); !strings.Contains(got, "healthy evidence restored") {
		t.Fatal(got)
	}
	for _, change := range []string{"revision", "stage", "stable", "anchor"} {
		changed := after
		switch change {
		case "revision":
			changed.Report.Revision++
		case "stage":
			changed.Report.CanaryStep++
		case "stable":
			changed.Report.StableDeploymentID = healthCandidateID
		case "anchor":
			anchor := after.Report.ObservationAnchor.Add(time.Second)
			changed.Report.ObservationAnchor = &anchor
		}
		if got := routeHealthHistoryTransition(before, changed); strings.Contains(got, "restored") || !strings.Contains(got, "context changed") {
			t.Fatal("claimed recovery across context", got)
		}
	}
}

func TestRouteHealthExplainInvalidLookupBeforeAPI(t *testing.T) {
	for _, flags := range [][]string{{}, {"--deployment", "invalid"}, {"--deployment", healthCandidateID, "--limit", "0"}, {"--deployment", healthCandidateID, "--limit", "11"}, {"--deployment", healthCandidateID, "--before", "invalid"}, {"--deployment", healthCandidateID, "--decision", healthDecisionID, "--limit", "5"}, {"--deployment", healthCandidateID, "--decision", healthDecisionID, "--before", healthDecisionID}} {
		resetJSONOut(t)
		setPreviewTestAuth(t)
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
		t.Cleanup(server.Close)
		t.Setenv("FAAS_API", server.URL)
		if code := run(append([]string{"routes", "health", "explain", "demo"}, flags...)); code != 1 || calls != 0 {
			t.Fatal("invalid lookup reached API")
		}
	}
}
