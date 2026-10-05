package faas_test

// adr: 595

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

// ADR-595: receivers can decode a change and retrieve its retained observation.
func TestAppHealthNotificationSavedEvidence(t *testing.T) {
	var payload faas.AppHealthChangedWebhookPayload
	err := json.Unmarshal([]byte(`{"version":1,"app_id":"app","scope":"default","transition_id":"transition","transition_observed_at":"2026-10-05T10:00:00Z","previous_status":"healthy","status":"unhealthy","change":"worsened","phase":"serving","evaluated_at":"2026-10-05T10:00:00Z","queued_at":"2026-10-05T10:00:00Z","coalesced":false,"cooldown_seconds":300,"serving_deployment_ids":[],"history_path":"/v1/apps/demo/health/history"}`), &payload)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != payload.HistoryPath || r.URL.Query().Get("before") != "cursor&value" || r.URL.Query().Get("limit") != "2" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("history request lost notification path, cursor or authentication", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(faas.AppHealthHistoryPage{AppID: payload.AppID, Scope: payload.Scope,
			Entries: []faas.AppHealthHistoryEntry{{ID: payload.TransitionID, Kind: "transition", ObservedAt: payload.TransitionObservedAt, Assessment: faas.AppHealthResponse{Status: payload.Status}}}})
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.ListAppHealthHistory(context.Background(), "demo", 2, "cursor&value")
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ID != payload.TransitionID || page.Entries[0].Assessment.Status != "unhealthy" || payload.Change != "worsened" {
		t.Fatal("notification provenance lost", page, payload, err)
	}
}
