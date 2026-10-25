package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/poyrazK/faas/sdk/go/preview"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestRouteLifecycleHistoryClient(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Second)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer token" || r.URL.Path != "/v1/apps/demo/route-lifecycle/history" || r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("before") != "cursor&value" {
			t.Error("request", r.URL.String())
		}
		entry := preview.RouteLifecycleHistoryEntry{
			ID: "9", ReviewedAt: at, Outcome: "applied", EvidenceAvailable: true,
			GraphIDs:  []string{"graph"},
			Captures:  []preview.RouteLifecycleHistoryCapture{{DeploymentID: "candidate", SHA256: "capture"}},
			Approvals: []preview.RouteLifecycleHistoryApproval{{ID: "approval", Used: true, Status: "invalidated", StatusReason: "review_inputs_changed", InvalidatedAt: &at, GraphIDs: []string{"old-graph"}}},
		}
		json.NewEncoder(w).Encode(preview.RouteLifecycleHistoryPage{AppID: "app", NextCursor: "8", Entries: []preview.RouteLifecycleHistoryEntry{entry}})
	}))
	defer ts.Close()
	client, err := faas.NewClient(ts.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	page, err := preview.Wrap(client).ListRouteLifecycleHistory(context.Background(), "demo", 2, "cursor&value")
	if err != nil || page.NextCursor != "8" || len(page.Entries) != 1 || !page.Entries[0].ReviewedAt.Equal(at) || page.Entries[0].Approvals[0].Status != "invalidated" || page.Entries[0].Approvals[0].GraphIDs[0] != "old-graph" || page.Entries[0].Captures[0].SHA256 != "capture" {
		t.Fatal("history binding", page, err)
	}
}
