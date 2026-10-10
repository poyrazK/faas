package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/api"
)

type recordingChannelSender struct {
	targets []alertchannels.Target
	fail    error
}

func (r *recordingChannelSender) Send(_ context.Context, t alertchannels.Target, _ alertchannels.Message) error {
	r.targets = append(r.targets, t)
	return r.fail
}

func withChannelSender(t *testing.T, r *recordingChannelSender) {
	t.Helper()
	prev := channelSender
	channelSender = func(alertchannels.Mailer) channelDeliverer { return r }
	t.Cleanup(func() { channelSender = prev })
}

const testSlackURL = "https://hooks.slack.com/services/T0001/B0002/secretsecretsecret"

func TestNotificationChannelLifecycle(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	rec := e.do(t, http.MethodPost, "/v1/notification-channels", api.CreateNotificationChannelRequest{Name: "ops-slack", Kind: "slack", SlackWebhookURL: testSlackURL}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "secretsecret") {
		t.Fatal("the Slack webhook secret must never be returned")
	}
	var slack api.NotificationChannelResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &slack)
	if slack.Target != "hooks.slack.com/T0001/B0002/…" {
		t.Fatalf("target hint = %q", slack.Target)
	}

	sender := &recordingChannelSender{}
	withChannelSender(t, sender)
	rec = e.do(t, http.MethodPost, "/v1/notification-channels/"+slack.ID+"/test", nil, nil)
	var result api.TestNotificationChannelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || !result.Delivered {
		t.Fatalf("test send = %d %s", rec.Code, rec.Body)
	}
	if len(sender.targets) != 1 || sender.targets[0].SlackURL != testSlackURL {
		t.Fatalf("sealed destination did not round-trip: %+v", sender.targets)
	}
	sender.fail = errors.New("destination rejected the notification (404)")
	rec = e.do(t, http.MethodPost, "/v1/notification-channels/"+slack.ID+"/test", nil, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &result)
	rec = e.do(t, http.MethodGet, "/v1/notification-channels/"+slack.ID, nil, nil)
	var got api.NotificationChannelResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if result.Delivered || got.LastError == "" || got.LastDeliveredAt == "" {
		t.Fatalf("failed test must be recorded on the channel: result %+v channel %+v", result, got)
	}

	if rec := e.do(t, http.MethodDelete, "/v1/notification-channels/"+slack.ID, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}
}

func TestNotificationChannelValidation(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	key := strings.Repeat("k", 32)
	tests := []struct {
		name string
		req  api.CreateNotificationChannelRequest
		want int
	}{
		{"pagerduty", api.CreateNotificationChannelRequest{Name: "pd", Kind: "pagerduty", PagerDutyRoutingKey: key}, http.StatusCreated},
		{"own email", api.CreateNotificationChannelRequest{Name: "me", Kind: "email", Email: strings.ToUpper(e.acct.Email)}, http.StatusCreated},
		{"someone else's email", api.CreateNotificationChannelRequest{Name: "them", Kind: "email", Email: "victim@example.org"}, http.StatusBadRequest},
		{"non-slack host", api.CreateNotificationChannelRequest{Name: "evil", Kind: "slack", SlackWebhookURL: "https://evil.example/services/T/B/X"}, http.StatusBadRequest},
		{"metadata address", api.CreateNotificationChannelRequest{Name: "meta", Kind: "slack", SlackWebhookURL: "http://169.254.169.254/services/T/B/X"}, http.StatusBadRequest},
		{"short routing key", api.CreateNotificationChannelRequest{Name: "pd2", Kind: "pagerduty", PagerDutyRoutingKey: "abc"}, http.StatusBadRequest},
		{"mixed fields", api.CreateNotificationChannelRequest{Name: "mix", Kind: "slack", SlackWebhookURL: testSlackURL, Email: "x@example.com"}, http.StatusBadRequest},
		{"unknown kind", api.CreateNotificationChannelRequest{Name: "sms", Kind: "sms"}, http.StatusBadRequest},
	}
	for _, tt := range tests {
		if rec := e.do(t, http.MethodPost, "/v1/notification-channels", tt.req, nil); rec.Code != tt.want {
			t.Errorf("%s: status %d, want %d: %s", tt.name, rec.Code, tt.want, rec.Body)
		}
	}
	rec := e.do(t, http.MethodGet, "/v1/notification-channels", nil, nil)
	var list []api.NotificationChannelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 2 {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), key) {
		t.Fatal("the routing key must never be returned")
	}
}
