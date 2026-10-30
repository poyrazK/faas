package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func createTestChannel(t *testing.T, e apidserver.TestEnv, name string) string {
	t.Helper()
	rec := e.Do(t, http.MethodPost, "/v1/notification-channels", api.CreateNotificationChannelRequest{Name: name, Kind: "slack", SlackWebhookURL: testSlackURL}, nil)
	var ch api.NotificationChannelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ch); err != nil || rec.Code != http.StatusCreated {
		t.Fatalf("create channel: %d %s", rec.Code, rec.Body)
	}
	return ch.ID
}

func TestAlertRuleChannels(t *testing.T) {
	e := apidserver.SetupAlerts(t, api.PlanPro)
	apidserver.CreateApp(t, e, "shop")
	ch := createTestChannel(t, e, "ops")

	// A channel-only rule needs no webhook.
	req := apidserver.AlertRuleReq()
	req.WebhookURL, req.WebhookSecret, req.ChannelIDs = "", "", []string{ch}
	created := apidserver.MustCreateAlertRule(t, e, "shop", req)
	if len(created.ChannelIDs) != 1 || created.ChannelIDs[0] != ch || created.WebhookURL != "" {
		t.Fatalf("created = %+v", created)
	}
	rec := e.Do(t, http.MethodGet, "/v1/apps/shop/alerts/"+created.ID, nil, nil)
	var got api.AlertRuleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.ChannelIDs) != 1 {
		t.Fatalf("get = %d %s", rec.Code, rec.Body)
	}

	// It cannot drop its last channel without gaining a webhook.
	empty := []string{}
	if rec := e.Do(t, http.MethodPatch, "/v1/apps/shop/alerts/"+created.ID, api.UpdateAlertRuleRequest{ChannelIDs: &empty}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("clearing the last destination = %d, want 400: %s", rec.Code, rec.Body)
	}
	// A rename keeps its channels.
	name := "renamed"
	rec = e.Do(t, http.MethodPatch, "/v1/apps/shop/alerts/"+created.ID, api.UpdateAlertRuleRequest{Name: &name}, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK || len(got.ChannelIDs) != 1 {
		t.Fatalf("rename = %d %s", rec.Code, rec.Body)
	}

	// A webhook rule can add channels and clear them again.
	hook := apidserver.MustCreateAlertRule(t, e, "shop", func() api.CreateAlertRuleRequest { r := apidserver.AlertRuleReq(); r.Name = "hooked"; return r }())
	ids := []string{ch}
	rec = e.Do(t, http.MethodPatch, "/v1/apps/shop/alerts/"+hook.ID, api.UpdateAlertRuleRequest{ChannelIDs: &ids}, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.ChannelIDs) != 1 {
		t.Fatalf("add channel = %d %s", rec.Code, rec.Body)
	}
	rec = e.Do(t, http.MethodPatch, "/v1/apps/shop/alerts/"+hook.ID, api.UpdateAlertRuleRequest{ChannelIDs: &empty}, nil)
	got = api.AlertRuleResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK || len(got.ChannelIDs) != 0 {
		t.Fatalf("clear channels on a webhook rule = %d %s", rec.Code, rec.Body)
	}
}

func TestAlertRuleChannelValidation(t *testing.T) {
	e := apidserver.SetupAlerts(t, api.PlanPro)
	apidserver.CreateApp(t, e, "shop")
	ch := createTestChannel(t, e, "ops")
	other := apidserver.SetupAlerts(t, api.PlanPro)
	foreign := createTestChannel(t, other, "theirs")
	tests := []struct {
		name string
		ids  []string
	}{
		{"no destination at all", nil},
		{"another account's channel", []string{foreign}},
		{"unknown channel", []string{"00000000-0000-0000-0000-000000000000"}},
		{"duplicate", []string{ch, ch}},
		{"too many", []string{ch, ch, ch, ch, ch, ch}},
	}
	for _, tt := range tests {
		req := apidserver.AlertRuleReq()
		req.Name, req.WebhookURL, req.WebhookSecret, req.ChannelIDs = "bad "+tt.name, "", "", tt.ids
		if rec := e.Do(t, http.MethodPost, "/v1/apps/shop/alerts", req, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", tt.name, rec.Code, rec.Body)
		}
	}
}

// adr: 20261029203000 — a channel that is a rule's only destination cannot be
// deleted: the rule would fire into nothing. A second channel or a webhook
// frees it.
func TestNotificationChannelDeleteKeepsRulesReachable(t *testing.T) {
	e := apidserver.SetupAlerts(t, api.PlanPro)
	apidserver.CreateApp(t, e, "shop")
	ops := createTestChannel(t, e, "ops")
	backup := createTestChannel(t, e, "backup")
	req := apidserver.AlertRuleReq()
	req.Name, req.WebhookURL, req.WebhookSecret, req.ChannelIDs = "only-ops", "", "", []string{ops}
	rule := apidserver.MustCreateAlertRule(t, e, "shop", req)

	rec := e.Do(t, http.MethodDelete, "/v1/notification-channels/"+ops, nil, nil)
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || rec.Code != http.StatusConflict ||
		problem.Code != api.CodeNotificationChannelInUse || !strings.Contains(problem.Detail, "only-ops ("+rule.ID+")") {
		t.Fatalf("deleting the rule's only channel = %d %s, want 409 naming the rule", rec.Code, rec.Body)
	}
	if rec := e.Do(t, http.MethodGet, "/v1/notification-channels/"+ops, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("a refused delete must keep the channel: %d", rec.Code)
	}

	both := []string{ops, backup}
	if rec := e.Do(t, http.MethodPatch, "/v1/apps/shop/alerts/"+rule.ID, api.UpdateAlertRuleRequest{ChannelIDs: &both}, nil); rec.Code != http.StatusOK {
		t.Fatalf("add a second channel = %d %s", rec.Code, rec.Body)
	}
	if rec := e.Do(t, http.MethodDelete, "/v1/notification-channels/"+ops, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete with another channel left = %d %s, want 204", rec.Code, rec.Body)
	}
}

// adr: 20261029203000 — notify_resolved is the webhook's opt-in to resolve
// events, so a rule without a webhook cannot ask for it.
func TestAlertRuleNotifyResolved(t *testing.T) {
	e := apidserver.SetupAlerts(t, api.PlanPro)
	apidserver.CreateApp(t, e, "shop")
	ch := createTestChannel(t, e, "ops")

	channelOnly := apidserver.AlertRuleReq()
	channelOnly.Name, channelOnly.WebhookURL, channelOnly.WebhookSecret, channelOnly.ChannelIDs, channelOnly.NotifyResolved = "channel-only", "", "", []string{ch}, true
	if rec := e.Do(t, http.MethodPost, "/v1/apps/shop/alerts", channelOnly, nil); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "notify_resolved needs a webhook_url") {
		t.Fatalf("notify_resolved without a webhook = %d %s, want 400", rec.Code, rec.Body)
	}

	hooked := apidserver.AlertRuleReq()
	hooked.Name, hooked.NotifyResolved = "hooked", true
	created := apidserver.MustCreateAlertRule(t, e, "shop", hooked)
	if !created.NotifyResolved {
		t.Fatalf("created = %+v, want notify_resolved", created)
	}
	off := false
	rec := e.Do(t, http.MethodPatch, "/v1/apps/shop/alerts/"+created.ID, api.UpdateAlertRuleRequest{NotifyResolved: &off}, nil)
	var got api.AlertRuleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK || got.NotifyResolved {
		t.Fatalf("turn notify_resolved off = %d %s", rec.Code, rec.Body)
	}
}
