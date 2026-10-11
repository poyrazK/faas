package main

// adr: 735

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-735: opt-in notifications link to the owner's retained health evidence.
func TestAppHealthNotificationsAPISubscriptionAndProvenance(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "observed-health-notifications")
	app, err := e.store.AppBySlug(t.Context(), slug)
	if err != nil {
		t.Fatal(err)
	}
	req := webhookReq()
	req.EventFilter = []string{"app.health.changed"}
	hook := mustCreateWebhook(t, e, slug, req)
	now := time.Now().UTC().Truncate(time.Second)
	for i, status := range []string{"healthy", "unhealthy"} {
		at := now.Add(time.Duration(i) * 31 * time.Second)
		claim, err := e.store.ClaimAppHealth(t.Context(), uuid.NewString(), at)
		if err != nil {
			t.Fatal(err)
		}
		a := api.AppHealthResponse{AppID: app.ID, Scope: "default", Status: status, Phase: "serving", EvaluatedAt: at.Format(time.RFC3339Nano), ValidForSeconds: 120, ServingDeploymentIDs: []string{}, Checks: []api.AppHealthCheck{}}
		if err := e.store.FinishAppHealth(t.Context(), claim, a, at); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := e.store.DrainAppWebhookEventOutbox(t.Context(), 32); err != nil || n != 1 {
		t.Fatal("durable app health event", n, err)
	}
	rows, _, err := e.store.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 10, "")
	if err != nil || len(rows) != 1 {
		t.Fatal("delivery", err)
	}
	var payload api.AppHealthChangedWebhookPayload
	if err := json.Unmarshal(rows[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	var page api.AppHealthHistoryPage
	rec := e.do(t, "GET", payload.HistoryPath, nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &page) != nil || len(page.Entries) != 2 || page.Entries[0].ID != payload.TransitionID {
		t.Fatal("notification cannot resolve saved observation", rec.Code, rec.Body)
	}
}
