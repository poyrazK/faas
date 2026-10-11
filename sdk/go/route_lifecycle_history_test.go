package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestRouteLifecycleHistoryClient(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Second)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer token" || r.URL.Path != "/v1/apps/demo/route-lifecycle/history" || r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("before") != "cursor&value" {
			t.Error("request", r.URL.String())
		}
		entry := faas.RouteLifecycleHistoryEntry{
			ID: "9", ReviewedAt: at, Outcome: "applied", EvidenceAvailable: true,
			GraphIDs:  []string{"graph"},
			Captures:  []faas.RouteLifecycleHistoryCapture{{DeploymentID: "candidate", SHA256: "capture"}},
			Approvals: []faas.RouteLifecycleHistoryApproval{{ID: "approval", Used: true, Status: "invalidated", StatusReason: "review_inputs_changed", InvalidatedAt: &at, GraphIDs: []string{"old-graph"}}},
		}
		json.NewEncoder(w).Encode(faas.RouteLifecycleHistoryPage{AppID: "app", NextCursor: "8", Entries: []faas.RouteLifecycleHistoryEntry{entry}})
	}))
	defer ts.Close()
	client, err := faas.NewClient(ts.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.ListRouteLifecycleHistory(context.Background(), "demo", 2, "cursor&value")
	if err != nil || page.NextCursor != "8" || len(page.Entries) != 1 || !page.Entries[0].ReviewedAt.Equal(at) || page.Entries[0].Approvals[0].Status != "invalidated" || page.Entries[0].Approvals[0].GraphIDs[0] != "old-graph" || page.Entries[0].Captures[0].SHA256 != "capture" {
		t.Fatal("history binding", page, err)
	}
}
