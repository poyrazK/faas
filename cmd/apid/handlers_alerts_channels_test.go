package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func createTestChannel(t *testing.T, e testEnv, name string) string {
	t.Helper()
	rec := e.do(t, http.MethodPost, "/v1/notification-channels", api.CreateNotificationChannelRequest{Name: name, Kind: "slack", SlackWebhookURL: testSlackURL}, nil)
	var ch api.NotificationChannelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ch); err != nil || rec.Code != http.StatusCreated {
		t.Fatalf("create channel: %d %s", rec.Code, rec.Body)
	}
	return ch.ID
}

func TestAlertRuleChannels(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	createApp(t, e, "shop")
	ch := createTestChannel(t, e, "ops")

	// A channel-only rule needs no webhook.
	req := alertRuleReq()
	req.WebhookURL, req.WebhookSecret, req.ChannelIDs = "", "", []string{ch}
	created := mustCreateAlertRule(t, e, "shop", req)
	if len(created.ChannelIDs) != 1 || created.ChannelIDs[0] != ch || created.WebhookURL != "" {
		t.Fatalf("created = %+v", created)
	}
	rec := e.do(t, http.MethodGet, "/v1/apps/shop/alerts/"+created.ID, nil, nil)
	var got api.AlertRuleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.ChannelIDs) != 1 {
		t.Fatalf("get = %d %s", rec.Code, rec.Body)
	}

	// It cannot drop its last channel without gaining a webhook.
	empty := []string{}
	if rec := e.do(t, http.MethodPatch, "/v1/apps/shop/alerts/"+created.ID, api.UpdateAlertRuleRequest{ChannelIDs: &empty}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("clearing the last destination = %d, want 400: %s", rec.Code, rec.Body)
	}
	// A rename keeps its channels.
	name := "renamed"
	rec = e.do(t, http.MethodPatch, "/v1/apps/shop/alerts/"+created.ID, api.UpdateAlertRuleRequest{Name: &name}, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK || len(got.ChannelIDs) != 1 {
		t.Fatalf("rename = %d %s", rec.Code, rec.Body)
	}

	// A webhook rule can add channels and clear them again.
	hook := mustCreateAlertRule(t, e, "shop", func() api.CreateAlertRuleRequest { r := alertRuleReq(); r.Name = "hooked"; return r }())
	ids := []string{ch}
	rec = e.do(t, http.MethodPatch, "/v1/apps/shop/alerts/"+hook.ID, api.UpdateAlertRuleRequest{ChannelIDs: &ids}, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.ChannelIDs) != 1 {
		t.Fatalf("add channel = %d %s", rec.Code, rec.Body)
	}
	rec = e.do(t, http.MethodPatch, "/v1/apps/shop/alerts/"+hook.ID, api.UpdateAlertRuleRequest{ChannelIDs: &empty}, nil)
	got = api.AlertRuleResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK || len(got.ChannelIDs) != 0 {
		t.Fatalf("clear channels on a webhook rule = %d %s", rec.Code, rec.Body)
	}
}

func TestAlertRuleChannelValidation(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	createApp(t, e, "shop")
	ch := createTestChannel(t, e, "ops")
	other := setupAlerts(t, api.PlanPro)
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
		req := alertRuleReq()
		req.Name, req.WebhookURL, req.WebhookSecret, req.ChannelIDs = "bad "+tt.name, "", "", tt.ids
		if rec := e.do(t, http.MethodPost, "/v1/apps/shop/alerts", req, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", tt.name, rec.Code, rec.Body)
		}
	}
}
