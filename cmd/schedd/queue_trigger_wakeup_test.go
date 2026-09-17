package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
)

func TestTriggerWakeNotification(t *testing.T) {
	tests := []struct {
		name string
		n    db.Notification
		want bool
	}{
		{name: "trigger ready", n: db.Notification{Channel: db.NotifyTriggerReady}, want: true},
		{name: "trigger changed", n: db.Notification{Channel: db.NotifyTriggerChanged}, want: true},
		{name: "queue invocation", n: db.Notification{Channel: db.NotifyInvocationDue, Payload: `{"source":"queue"}`}, want: true},
		{name: "delayed task invocation", n: db.Notification{Channel: db.NotifyInvocationDue, Payload: `{"source":"delayed_task"}`}, want: true},
		{name: "async invocation", n: db.Notification{Channel: db.NotifyInvocationDue, Payload: `{"source":"async_invoke"}`}, want: false},
		{name: "malformed invocation payload", n: db.Notification{Channel: db.NotifyInvocationDue, Payload: "not-json"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := triggerWakeNotification(tt.n); got != tt.want {
				t.Fatalf("triggerWakeNotification() = %v, want %v", got, tt.want)
			}
		})
	}
}
