package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAlertObserveOnlyRoute(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := t.Context()
	acct, store, srv := *e.Acct(), *e.Store(), *e.S()
	if _, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "orders", Type: state.AppTypeApp}); err != nil {
		t.Fatal(err)
	}
	srv.AnomalyAlertsAllowedFor(platformflags.Subject{AccountID: acct.ID}, true)
	const collection = "/v1/apps/orders/alerts"
	req := api.CreateAlertRuleRequest{
		Name: "Observe orders", Metric: "request_count", Comparison: "above_baseline",
		Threshold: 3, WindowSpec: "15m", ObserveOnly: true,
	}
	rec := e.Do(t, http.MethodPost, collection, req, nil)
	var response api.AlertRuleResponse
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &response) != nil || !response.ObserveOnly || !response.Enabled {
		t.Fatalf("create observe-only rule: status=%d body=%s", rec.Code, rec.Body.String())
	}
	rule, err := store.AlertRuleByID(ctx, response.ID)
	if err != nil {
		t.Fatal(err)
	}
	patchPath := collection + "/" + rule.ID
	at := rule.UpdatedAt.Add(time.Second)
	if ok, err := store.RecordAlertObservation(ctx, rule, at, api.AlertAnomalyEvaluation{Reason: api.AlertAnomalyObservedBreach}); err != nil || !ok {
		t.Fatalf("record observed breach: %t %v", ok, err)
	}
	rec = e.Do(t, http.MethodGet, patchPath, nil, nil)
	response = api.AlertRuleResponse{}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil || len(response.ObservedIncidents) != 1 ||
		response.ObservedIncidents[0].EligibleNotifications != 1 || response.AnomalyEvaluation == nil || response.AnomalyEvaluation.Reason != api.AlertAnomalyObservedBreach {
		t.Fatalf("get observation history: status=%d body=%s", rec.Code, rec.Body.String())
	}
	patch := api.UpdateAlertRuleRequest{ObserveOnly: new(false)}
	if rec = e.Do(t, http.MethodPatch, patchPath, patch, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("promote without a destination: status=%d body=%s", rec.Code, rec.Body.String())
	}
	channel, err := store.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: acct.ID, Name: "Operations", Kind: "slack"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	patch.ChannelIDs = &[]string{channel.ID}
	rec = e.Do(t, http.MethodPatch, patchPath, patch, nil)
	response = api.AlertRuleResponse{}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil || response.ObserveOnly ||
		!response.Enabled || len(response.ChannelIDs) != 1 || response.ChannelIDs[0] != channel.ID {
		t.Fatalf("promote with a channel: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
