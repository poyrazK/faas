package faas_test

// adr: 456

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestRouteHealthHistoryClientSavedEvidence(t *testing.T) {
	at := time.Date(2026, 10, 2, 21, 10, 45, 0, time.UTC)
	anchor := at.Add(-time.Hour)
	zero := 0.0
	entry := faas.RouteHealthHistoryEntry{Version: 1, ID: "decision", CheckedAt: at, Source: "worker", TrafficPercent: 1, RequestedTrafficPercent: 10,
		Policy:   faas.RouteHealthEvaluationPolicy{Version: 1, Windows: 2, MinLatencyRequests: 100, ErrorRateFactor: 3, LatencyFactor: 1.5},
		Decision: faas.RouteHealthDecision{DeploymentID: "candidate", HistoryID: "decision", Status: "blocked", CheckedAt: at},
		Report:   faas.RouteHealthReport{DeploymentID: "candidate", ObservationAnchor: &anchor, CheckedAt: at, Routes: []faas.RouteHealthFinding{{Method: "POST", Path: "/checkout", MaxP95MS: 300, Windows: []faas.RouteHealthWindowEvidence{{Candidate: faas.RouteHealthCounts{Requests: 100, P95LatencyMS: &zero}, Stable: faas.RouteHealthCounts{Requests: 99}}}}}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer token" {
			t.Error("history request auth")
		}
		switch r.URL.Path {
		case "/v1/apps/demo/route-health/deployments/candidate/history":
			if r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("before") != "cursor&value" {
				t.Error("history cursor binding")
			}
			_ = json.NewEncoder(w).Encode(faas.RouteHealthHistoryPage{AppID: "app", DeploymentID: "candidate", Entries: []faas.RouteHealthHistoryEntry{entry}, NextCursor: "decision"})
		case "/v1/apps/demo/route-health/deployments/candidate/history/decision":
			if r.URL.RawQuery != "" {
				t.Error("single decision query")
			}
			_ = json.NewEncoder(w).Encode(entry)
		default:
			t.Error("history path", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.ListRouteHealthHistory(t.Context(), "demo", "candidate", 2, "cursor&value")
	if err != nil || len(page.Entries) != 1 || page.NextCursor != entry.ID {
		t.Fatal("history page", err)
	}
	got, err := client.GetRouteHealthHistoryEntry(t.Context(), "demo", "candidate", entry.ID)
	if err != nil || got.Decision.HistoryID != entry.ID || !got.Report.ObservationAnchor.Equal(anchor) || got.Policy.MinLatencyRequests != 100 || got.Source != "worker" {
		t.Fatal("saved context lost", err)
	}
	window := got.Report.Routes[0].Windows[0]
	if window.Candidate.P95LatencyMS == nil || *window.Candidate.P95LatencyMS != 0 || window.Stable.P95LatencyMS != nil {
		t.Fatal("zero versus unavailable evidence lost")
	}
}

func TestRouteHealthAbortHistoryRoundtrip(t *testing.T) {
	body := []byte(`{"version":1,"purpose":"abort","source":"worker","traffic_percent":1,"requested_traffic_percent":0,"report":{"mode":"enforce","on_regression":"abort","status":"regressed"},"decision":{"mode":"enforce","on_regression":"abort","status":"aborted","history_id":"abort"}}`)
	var entry faas.RouteHealthHistoryEntry
	if err := json.Unmarshal(body, &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Purpose != "abort" || entry.Decision.Status != "aborted" || entry.Report.OnRegression != "abort" || entry.Decision.OnRegression != "abort" || entry.RequestedTrafficPercent != 0 {
		t.Fatal("committed recovery metadata lost", entry)
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip faas.RouteHealthHistoryEntry
	if err := json.Unmarshal(encoded, &roundtrip); err != nil || roundtrip.Purpose != entry.Purpose || roundtrip.Decision != entry.Decision {
		t.Fatal("abort roundtrip", err)
	}
}
