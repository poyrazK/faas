package main

// adr: 457

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRouteHealthNotificationAPISubscriptionAndSavedEvidence(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "health-notifications")
	app, _ := e.store.AppBySlug(t.Context(), slug)
	stable, err := e.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:stable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(t.Context(), stable.ID); err != nil {
		t.Fatal(err)
	}
	d, err := e.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate", CanaryPreset: "balanced", CanaryTotalSteps: 4, RolloutState: "pending", TrafficPercent: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(t.Context(), d.ID); err != nil {
		t.Fatal(err)
	}
	req := webhookReq()
	req.EventFilter = []string{"routes.health.blocked", "routes.health.resumed"}
	hook := mustCreateWebhook(t, e, slug, req)
	base := "/v1/apps/" + slug + "/route-health"
	gate := api.SetRouteHealthGateRequest{Mode: "enforce", ExpectedRevision: new(int64), Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout", MaxP95MS: 300}}}
	if rec := e.do(t, "PUT", base+"/gate", gate, nil); rec.Code != 200 {
		t.Fatalf("gate %d %s", rec.Code, rec.Body)
	}
	for range 2 {
		if rec := e.do(t, "POST", "/v1/deployments/"+d.ID+"/canary/advance", api.AdvanceCanaryRequest{ExpectedStep: 0}, nil); rec.Code != 409 {
			t.Fatalf("hold %d %s", rec.Code, rec.Body)
		}
	}
	if n, err := e.store.DrainAppWebhookEventOutbox(t.Context(), 32); err != nil || n != 1 {
		t.Fatal("hold not durable/deduplicated", n, err)
	}
	rows, _, err := e.store.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 10, "")
	if err != nil || len(rows) != 1 || rows[0].Event != state.AppWebhookEventRouteHealthBlocked {
		t.Fatal("hold delivery", err)
	}
	var payload api.RouteHealthTransitionWebhookPayload
	if json.Unmarshal(rows[0].Payload, &payload) != nil || payload.HealthStatus != "unknown" || payload.Status != "blocked" || strings.Contains(string(rows[0].Payload), "/checkout") {
		t.Fatal("hold metadata", string(rows[0].Payload))
	}
	var entry api.RouteHealthHistoryEntry
	rec := e.do(t, "GET", payload.HistoryPath, nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &entry) != nil || entry.ID != payload.DecisionID || entry.Decision.Status != "blocked" || entry.Report.Routes[0].Path != "/checkout" {
		t.Fatal("notification does not resolve to saved evidence", rec.Code)
	}
	// Switching to report mode allows an advance without claiming observed recovery.
	revision := int64(1)
	gate.Mode, gate.ExpectedRevision = "report", &revision
	if rec := e.do(t, "PUT", base+"/gate", gate, nil); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if rec := e.do(t, "POST", "/v1/deployments/"+d.ID+"/canary/advance", api.AdvanceCanaryRequest{ExpectedStep: 0}, nil); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	if health, err := e.store.AppWebhookEventOutboxHealth(t.Context()); err != nil || health.PendingCount != 0 {
		t.Fatal("report mode claimed recovery", health, err)
	}
}
