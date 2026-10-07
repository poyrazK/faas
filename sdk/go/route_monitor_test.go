package faas_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

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

func sameProductionRouteJSON(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}
