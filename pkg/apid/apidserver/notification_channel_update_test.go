package apidserver_test

// adr: 20261104142830 — PATCH /v1/notification-channels/{id} renames a
// channel or replaces its sealed destination in place: the id stays, the new
// destination is what a test send reaches, and fields of another kind, an
// email destination or a taken name are refused.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestNotificationChannelUpdateRotatesInPlace(t *testing.T) {
	e := apidserver.SetupAlerts(t, api.PlanPro)
	create := func(req api.CreateNotificationChannelRequest) api.NotificationChannelResponse {
		t.Helper()
		rec := e.Do(t, http.MethodPost, "/v1/notification-channels", req, nil)
		var ch api.NotificationChannelResponse
		if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &ch) != nil {
			t.Fatalf("create = %d %s", rec.Code, rec.Body)
		}
		return ch
	}
	slack := create(api.CreateNotificationChannelRequest{Name: "ops-slack", Kind: "slack", SlackWebhookURL: testSlackURL})
	create(api.CreateNotificationChannelRequest{Name: "taken", Kind: "slack", SlackWebhookURL: testSlackURL})

	const rotated = "https://hooks.slack.com/services/T0009/B0008/newsecretnewsecret"
	name := "ops-alerts"
	rec := e.Do(t, http.MethodPatch, "/v1/notification-channels/"+slack.ID, api.UpdateNotificationChannelRequest{Name: &name, SlackWebhookURL: rotated}, nil)
	var got api.NotificationChannelResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("update = %d %s", rec.Code, rec.Body)
	}
	if got.ID != slack.ID || got.Name != name || got.Target != "hooks.slack.com/T0009/B0008/…" || strings.Contains(rec.Body.String(), "newsecret") {
		t.Fatalf("updated = %+v; want the same id, the new name and the new destination's hint only", got)
	}
	sender := &recordingChannelSender{}
	withChannelSender(t, sender)
	if rec := e.Do(t, http.MethodPost, "/v1/notification-channels/"+slack.ID+"/test", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("test send = %d %s", rec.Code, rec.Body)
	}
	if len(sender.targets) != 1 || sender.targets[0].SlackURL != rotated {
		t.Fatalf("test send reached %+v; want the rotated destination", sender.targets)
	}

	taken := "taken"
	for label, tc := range map[string]struct {
		req  api.UpdateNotificationChannelRequest
		want int
	}{
		"another kind's field": {api.UpdateNotificationChannelRequest{PagerDutyRoutingKey: strings.Repeat("a", 32)}, http.StatusBadRequest},
		"nothing to change":    {api.UpdateNotificationChannelRequest{}, http.StatusBadRequest},
		"not a slack url":      {api.UpdateNotificationChannelRequest{SlackWebhookURL: "https://example.com/hook"}, http.StatusBadRequest},
		"a taken name":         {api.UpdateNotificationChannelRequest{Name: &taken}, http.StatusConflict},
	} {
		if rec := e.Do(t, http.MethodPatch, "/v1/notification-channels/"+slack.ID, tc.req, nil); rec.Code != tc.want {
			t.Errorf("%s = %d %s; want %d", label, rec.Code, rec.Body, tc.want)
		}
	}
	if rec := e.Do(t, http.MethodPatch, "/v1/notification-channels/6f0c1d4e-7a52-4c37-9a63-0f6a7b6c1a10", api.UpdateNotificationChannelRequest{Name: &name}, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("update of a missing channel = %d", rec.Code)
	}
}
