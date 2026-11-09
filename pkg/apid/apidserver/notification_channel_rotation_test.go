package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261109120000 — a PagerDuty routing key is not replaced while a rule
// using the channel is firing: the resolve would go to the new key's service
// and the incident the old key opened would stay open.
func TestPagerDutyKeyIsNotReplacedUnderAnOpenIncident(t *testing.T) {
	e := apidserver.SetupAlerts(t, api.PlanPro)
	apidserver.CreateApp(t, e, "shop")
	rec := e.Do(t, http.MethodPost, "/v1/notification-channels", api.CreateNotificationChannelRequest{
		Name: "oncall", Kind: "pagerduty", PagerDutyRoutingKey: strings.Repeat("a", 32),
	}, nil)
	var ch api.NotificationChannelResponse
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &ch) != nil {
		t.Fatalf("create channel = %d %s", rec.Code, rec.Body)
	}
	req := apidserver.AlertRuleReq()
	req.WebhookURL, req.WebhookSecret, req.ChannelIDs = "", "", []string{ch.ID}
	rule := apidserver.MustCreateAlertRule(t, e, "shop", req)
	store := *e.Store()
	if _, err := store.SetAlertRuleState(context.Background(), rule.ID, state.AlertStateFiring, time.Now()); err != nil {
		t.Fatal(err)
	}

	rotate := api.UpdateNotificationChannelRequest{PagerDutyRoutingKey: strings.Repeat("b", 32)}
	rec = e.Do(t, http.MethodPatch, "/v1/notification-channels/"+ch.ID, rotate, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), rule.ID) {
		t.Fatalf("rotate under a firing rule = %d %s; want 409 naming the rule", rec.Code, rec.Body)
	}
	name := "oncall-primary"
	if rec := e.Do(t, http.MethodPatch, "/v1/notification-channels/"+ch.ID, api.UpdateNotificationChannelRequest{Name: &name}, nil); rec.Code != http.StatusOK {
		t.Fatalf("rename under a firing rule = %d %s; a rename keeps the key", rec.Code, rec.Body)
	}

	if _, err := store.SetAlertRuleState(context.Background(), rule.ID, state.AlertStateOk, time.Now()); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, http.MethodPatch, "/v1/notification-channels/"+ch.ID, rotate, nil); rec.Code != http.StatusOK {
		t.Fatalf("rotate after recovery = %d %s", rec.Code, rec.Body)
	}
}
