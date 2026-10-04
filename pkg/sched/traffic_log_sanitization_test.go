// adr: 570
package sched

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

type failingTrafficNotifier struct {
	channel string
	payload string
}

func (n *failingTrafficNotifier) Notify(_ context.Context, channel, payload string) error {
	n.channel, n.payload = channel, payload
	return errors.New("notification unavailable")
}

func TestEmitInstanceChangedSanitizesWakeLogAndPreservesNotification(t *testing.T) {
	wakeID := "wake\r\nforged\v\x7f"
	var output bytes.Buffer
	n := &failingTrafficNotifier{}
	e := &Engine{notif: n, log: slog.New(slog.NewJSONHandler(&output, nil))}
	e.emitInstanceChanged(t.Context(), "instance-1", "app-1", state.StateRunning, wakeID, "node-1")
	if n.channel != db.NotifyInstanceChanged {
		t.Fatalf("notification channel = %q", n.channel)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(n.payload), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["wake_id"] != wakeID || payload["node_id"] != "node-1" || payload["state"] != string(state.StateRunning) {
		t.Fatalf("notification identity changed: %+v", payload)
	}
	if strings.Count(output.String(), "\n") != 1 {
		t.Fatalf("notification failure produced multiple log records: %q", output.String())
	}
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if got := record["wake_id"]; got != "wake··forged··" {
		t.Fatalf("unsafe wake log field = %q", got)
	}
}
