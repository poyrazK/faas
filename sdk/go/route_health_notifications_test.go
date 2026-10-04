package faas_test

// adr: 457

import (
	"encoding/json"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestRouteHealthNotificationPayload(t *testing.T) {
	body := []byte(`{"version":1,"app_id":"app","deployment_id":"candidate","stable_deployment_id":"stable","decision_id":"healthy","blocked_decision_id":"held","status":"resumed","health_status":"healthy","reason":"selected_routes_healthy","source":"worker","canary_step":0,"revision":1,"observation_anchor":"2026-10-02T18:00:00Z","checked_at":"2026-10-02T19:00:00Z","previous_traffic_percent":1,"requested_traffic_percent":10,"history_path":"/v1/apps/demo/route-health/deployments/candidate/history/healthy"}`)
	var payload faas.RouteHealthTransitionWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.DecisionID != "healthy" || payload.BlockedDecisionID != "held" || payload.Source != "worker" || payload.PreviousTrafficPercent != 1 || payload.RequestedTrafficPercent != 10 || payload.ObservationAnchor == nil {
		t.Fatal("transition provenance lost")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var original, roundtrip map[string]any
	if json.Unmarshal(body, &original) != nil || json.Unmarshal(encoded, &roundtrip) != nil || len(original) != len(roundtrip) {
		t.Fatal("payload shape drift")
	}
	for key, value := range original {
		if roundtrip[key] != value {
			t.Fatal("payload field changed", key)
		}
	}
}
