package state_test

// adr: 20261028110044

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// checkRouteHealthHoldAbort holds a canary, has the hold deadline end it and
// checks that the hold reads as aborted and that app webhooks receive
// routes.health.aborted naming the hold's first blocking decision, once.
func checkRouteHealthHoldAbort(t *testing.T, s state.Store, a state.Account, app state.App, d state.Deployment) {
	t.Helper()
	ctx := t.Context()
	holdStore, aborter := s.(state.RouteAlertStore), s.(state.RouteHealthHoldAborter)
	hook := healthNotificationHook(t, s, a.ID, app.ID, "aborted", []string{"routes.health.aborted"}, true)
	var held api.RouteHealthDecision
	if _, _, err := s.(state.CanaryAdvancer).AdvanceCanary(ctx, d.ID, state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteHealthDecision: &held}); err == nil || held.HistoryID == "" {
		t.Fatalf("advance on sparse evidence: %v (decision %+v), want a recorded hold", err, held)
	}
	at := time.Now().UTC()
	for range 2 {
		if err := aborter.AbortRouteHealthHold(ctx, a.ID, app.ID, d.ID, at); err != nil {
			t.Fatal(err)
		}
	}
	holds, total, err := holdStore.RouteHealthHoldsSince(ctx, app.ID, at.Add(-time.Minute), 5)
	if err != nil || total != 1 || holds[0].Status != "aborted" {
		t.Fatalf("holds after the deadline abort = %+v total %d err %v; want the aborted hold counted", holds, total, err)
	}
	if n, err := s.(state.AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(ctx, 32); err != nil || n != 1 {
		t.Fatalf("aborted webhooks relayed %d err %v, want exactly 1", n, err)
	}
	rows, _, err := s.ListAppWebhookDeliveries(ctx, app.ID, hook.ID, 10, "")
	if err != nil || len(rows) != 1 || rows[0].Event != state.AppWebhookEventRouteHealthAborted {
		t.Fatalf("deliveries = %+v err %v", rows, err)
	}
	var payload api.RouteHealthTransitionWebhookPayload
	if err := json.Unmarshal(rows[0].Payload, &payload); err != nil || payload.Status != "aborted" || payload.Reason != api.RouteHealthHoldDeadlineReason ||
		payload.DecisionID != held.HistoryID || payload.BlockedDecisionID != held.HistoryID || payload.DeploymentID != d.ID || payload.RequestedTrafficPercent != 0 ||
		payload.HistoryPath != "/v1/apps/"+app.Slug+"/route-health/deployments/"+d.ID+"/history/"+held.HistoryID {
		t.Fatalf("aborted payload = %s (%v)", rows[0].Payload, err)
	}
}

func TestRouteHealthHoldAbortMem(t *testing.T) {
	s := state.NewMemStore()
	a, app, _, d := healthFixture(t, s)
	zero := int64(0)
	if _, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "enforce", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout", MaxP95MS: 300}}}); err != nil {
		t.Fatal(err)
	}
	checkRouteHealthHoldAbort(t, s, a, app, d)
}

func TestRouteHealthHoldAbortPostgres(t *testing.T) {
	_, s, a, app, d, seed := healthNotificationPG(t)
	seed(1, 100)
	checkRouteHealthHoldAbort(t, s, a, app, d)
}
