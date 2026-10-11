package faas_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestProductionRouteMonitorClient(t *testing.T) {
	body, err := os.ReadFile("../../tests/fixtures/production-route-incident.json")
	if err != nil {
		t.Fatal(err)
	}
	var i faas.RouteMonitorIncident
	if err := json.Unmarshal(body, &i); err != nil {
		t.Fatal(err)
	}
	i.Baseline = &faas.RouteMonitorDeploymentBaseline{DeploymentID: "66666666-6666-4666-8666-666666666666", CommitSHA: strings.Repeat("b", 40), Repository: "github.com/team/service", SourceRoot: "."}
	timelineRoutes := make([]faas.RouteMonitorIncidentTimelineRoute, 0, len(i.OpeningReport.Routes))
	for index, finding := range i.OpeningReport.Routes {
		timelineRoutes = append(timelineRoutes, faas.RouteMonitorIncidentTimelineRoute{RouteIndex: index, Status: finding.Status, ErrorStatus: finding.ErrorStatus, LatencyStatus: finding.LatencyStatus})
	}
	i.Timeline = []faas.RouteMonitorIncidentTimelineEntry{{CheckedAt: i.OpenedAt, Coverage: "observed_only", Status: i.OpeningReport.Status, Reason: i.OpeningReport.Reason, Routes: timelineRoutes}}
	i.Escalations = []faas.RouteMonitorIncidentEscalation{{
		TransitionID: "44444444-4444-4444-8444-444444444444", CheckedAt: i.OpenedAt.Add(2 * time.Minute), PreviousCheckedAt: i.OpenedAt.Add(time.Minute),
		NewlyViolatedRoutes: 1, NewlyViolatedSignals: 1,
		Signals:  []faas.RouteMonitorIncidentEscalationSignal{{RouteIndex: 0, Signal: "latency", Finding: i.OpeningReport.Routes[0]}},
		Evidence: i.Evidence[:1],
	}}
	body, err = json.Marshal(i)
	if err != nil {
		t.Fatal(err)
	}
	c := faas.RouteMonitorConfig{AppID: i.AppID, Enabled: true, Revision: 1, UpdatedAt: i.OpeningReport.ObservationAnchor, Routes: []faas.RouteMonitorRoute{i.OpeningReport.Routes[0].Route}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/apps/demo/route-monitor":
			_ = json.NewEncoder(w).Encode(c)
		case "PUT /v1/apps/demo/route-monitor":
			var req faas.SetRouteMonitorRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.ExpectedRevision == nil || *req.ExpectedRevision != 1 || !req.Enabled || !reflect.DeepEqual(req.Routes, c.Routes) {
				t.Error("intent binding")
			}
			_ = json.NewEncoder(w).Encode(c)
		case "POST /v1/apps/demo/route-monitor/preview":
			var req faas.PreviewRouteMonitorRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || !reflect.DeepEqual(req.Routes, c.Routes) {
				t.Error("preview proposal binding")
			}
			_ = json.NewEncoder(w).Encode(faas.RouteMonitorPreview{CurrentRevision: i.OpeningReport.Revision, PreviewOnly: true, ConfigChangeResetsObservationAnchor: true, Report: i.OpeningReport})
		case "GET /v1/apps/demo/route-monitor/report":
			_ = json.NewEncoder(w).Encode(i.OpeningReport)
		case "GET /v1/apps/demo/route-monitor/incidents":
			if r.URL.Query().Get("limit") != "5" || r.URL.Query().Get("before") != i.ID {
				t.Error("pagination binding")
			}
			_ = json.NewEncoder(w).Encode(faas.RouteMonitorIncidentPage{AppID: i.AppID, Incidents: []faas.RouteMonitorIncident{i}})
		case "GET /v1/apps/demo/route-monitor/incidents/" + i.ID:
			_, _ = w.Write(body)
		default:
			t.Error("unexpected request", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "test")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if got, err := client.GetRouteMonitor(ctx, "demo"); err != nil || !sameProductionRouteJSON(got, c) {
		t.Fatal("get", err)
	}
	if _, err := client.SetRouteMonitor(ctx, "demo", faas.SetRouteMonitorRequest{Enabled: true, ExpectedRevision: &c.Revision, Routes: c.Routes}); err != nil {
		t.Fatal(err)
	}
	if preview, err := client.PreviewRouteMonitor(ctx, "demo", faas.PreviewRouteMonitorRequest{Routes: c.Routes}); err != nil || preview.CurrentRevision != i.OpeningReport.Revision || !preview.PreviewOnly {
		t.Fatal("preview", err)
	}
	if r, err := client.GetRouteMonitorReport(ctx, "demo"); err != nil || r.Status != "violated" {
		t.Fatal("report", err)
	}
	if p, err := client.ListRouteMonitorIncidents(ctx, "demo", 5, i.ID); err != nil || len(p.Incidents) != 1 {
		t.Fatal("list", err)
	}
	if got, err := client.GetRouteMonitorIncident(ctx, "demo", i.ID); err != nil || !sameProductionRouteJSON(got, i) {
		t.Fatal("saved evidence", err)
	}
}

func TestRouteMonitorEscalationWebhookPayloadRoundTrip(t *testing.T) {
	body := []byte(`{"version":1,"app_id":"11111111-1111-4111-8111-111111111111","deployment_id":"22222222-2222-4222-8222-222222222222","incident_id":"33333333-3333-4333-8333-333333333333","transition_id":"44444444-4444-4444-8444-444444444444","revision":7,"status":"open","checked_at":"2026-10-06T09:01:00Z","incident_path":"/v1/apps/demo/route-monitor/incidents/33333333-3333-4333-8333-333333333333","escalation":{"previous_checked_at":"2026-10-06T09:00:00Z","newly_violated_routes":1,"newly_violated_signals":2},"customer_impact":{"group_by":"tenant","coverage":"observed_only","observed_customers":5,"violated_customers":2,"unknown_customers":1}}`)
	var payload faas.RouteMonitorWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.TransitionID != "44444444-4444-4444-8444-444444444444" || payload.Escalation == nil || payload.Escalation.NewlyViolatedRoutes != 1 || payload.Escalation.NewlyViolatedSignals != 2 || payload.CustomerImpact == nil || payload.CustomerImpact.UnknownCustomers != 1 {
		t.Fatalf("escalation event did not round-trip: %+v", payload)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var roundTripped faas.RouteMonitorWebhookPayload
	if err := json.Unmarshal(encoded, &roundTripped); err != nil || roundTripped.Escalation == nil || roundTripped.Escalation.PreviousCheckedAt.Format(time.RFC3339) != "2026-10-06T09:00:00Z" {
		t.Fatalf("escalation event failed JSON round-trip: %s %v", encoded, err)
	}
}

func sameProductionRouteJSON(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}
