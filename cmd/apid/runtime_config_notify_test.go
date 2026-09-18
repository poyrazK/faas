// adr: 045
// issue: 1278
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeConfigNotifyStub struct {
	channel string
	payload string
}

func (n *runtimeConfigNotifyStub) Notify(_ context.Context, channel, payload string) error {
	n.channel = channel
	n.payload = payload
	return nil
}

func (n *runtimeConfigNotifyStub) Subscribe(_ context.Context, _ []string) (<-chan db.Notification, func(), error) {
	ch := make(chan db.Notification)
	close(ch)
	return ch, func() {}, nil
}

func (n *runtimeConfigNotifyStub) WaitFor(_ context.Context, _ string, _ func(string) bool, _ time.Duration) (string, error) {
	return "", db.ErrWaitTimeout
}

func TestNotifyRuntimeConfigChangeCarriesIdentityOnly(t *testing.T) {
	notifier := &runtimeConfigNotifyStub{}
	srv := &server{notif: notifier, log: slog.Default()}
	srv.notifyRuntimeConfigChange(context.Background(), db.NotifyAppEnvChanged,
		state.Account{ID: "acct-1"}, state.App{ID: "app-1"}, "set", "default", "FEATURE_X")

	if notifier.channel != db.NotifyAppEnvChanged {
		t.Fatalf("channel = %q, want %q", notifier.channel, db.NotifyAppEnvChanged)
	}
	var payload db.RuntimeConfigChangedPayload
	if err := json.Unmarshal([]byte(notifier.payload), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.AppID != "app-1" || payload.AccountID != "acct-1" || payload.Key != "FEATURE_X" {
		t.Fatalf("payload = %+v", payload)
	}
	if strings.Contains(notifier.payload, "secret-value") {
		t.Fatal("runtime config notification leaked a value")
	}
}
