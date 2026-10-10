package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAlertPresetDeliversToChannels(t *testing.T) {
	e := setupAlerts(t, api.PlanHobby)
	mustSeedApp(t, e, "shop")
	e.store.SeedAlertPresetForTest(state.AlertPreset{Name: "availability", DisplayName: "Availability", Category: "reliability",
		Metric: "error_rate_pct", Comparison: "gt", Threshold: 5, WindowSpec: "5m", DefaultCooldownMinutes: 30, EnabledInCatalog: true, MinimumPlan: "hobby"})
	ch := createTestChannel(t, e, "ops")

	rec := e.do(t, http.MethodPost, "/v1/apps/shop/alert-presets/availability/enable", api.EnableAlertPresetRequest{ChannelIDs: []string{ch}}, nil)
	var rule api.AlertRuleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &rule); err != nil || rec.Code != http.StatusCreated || len(rule.ChannelIDs) != 1 || rule.WebhookURL != "" {
		t.Fatalf("channel-only preset = %d %s", rec.Code, rec.Body)
	}
	// The webhook test endpoint points channel-only rules at the channel test.
	rec = e.do(t, http.MethodPost, "/v1/apps/shop/alert-presets/availability/test", nil, nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "notification-channels") {
		t.Fatalf("preset test on a channel-only rule = %d %s", rec.Code, rec.Body)
	}
	// Neither a webhook nor channels is still refused.
	if rec := e.do(t, http.MethodPost, "/v1/apps/shop/alert-presets/availability/enable", api.EnableAlertPresetRequest{}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("no destination = %d, want 400", rec.Code)
	}
}

func TestAlertChannelItemAndDestinations(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	ok := alertChannelItem(state.NotificationChannel{Name: "ops", Kind: "slack", LastDeliveredAt: now.Add(-3 * time.Minute)}, now)
	failed := alertChannelItem(state.NotificationChannel{Name: "pd", Kind: "pagerduty", LastDeliveredAt: now.Add(-time.Hour), LastErrorAt: now.Add(-time.Minute), LastError: "destination rejected the notification (404)"}, now)
	unused := alertChannelItem(state.NotificationChannel{Name: "me", Kind: "email"}, now)
	if ok.LastClass != "ok" || failed.LastClass != "bad" || !strings.Contains(failed.LastResult, "404") || unused.LastResult != "never used" {
		t.Fatalf("ok %+v failed %+v unused %+v", ok, failed, unused)
	}

	e := setupAlerts(t, api.PlanPro)
	createApp(t, e, "shop")
	chID := createTestChannel(t, e, "ops")
	req := alertRuleReq()
	req.ChannelIDs = []string{chID}
	created := mustCreateAlertRule(t, e, "shop", req)
	rule, err := e.store.AlertRuleByID(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.s.ruleDestinations(t.Context(), rule); got != "webhook, ops (slack)" {
		t.Fatalf("destinations = %q", got)
	}
}
