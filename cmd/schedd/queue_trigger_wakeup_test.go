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

func TestQueueInvocationApp(t *testing.T) {
	tests := []struct {
		name string
		n    db.Notification
		want string
	}{
		{name: "queue message", n: db.Notification{Channel: db.NotifyInvocationDue, Payload: `{"app_id":"a1","source":"queue"}`}, want: "a1"},
		{name: "queue message without app", n: db.Notification{Channel: db.NotifyInvocationDue, Payload: `{"source":"queue"}`}},
		{name: "delayed task", n: db.Notification{Channel: db.NotifyInvocationDue, Payload: `{"app_id":"a1","source":"delayed_task"}`}},
		{name: "async invocation", n: db.Notification{Channel: db.NotifyInvocationDue, Payload: `{"app_id":"a1","source":"async_invoke"}`}},
		{name: "malformed payload", n: db.Notification{Channel: db.NotifyInvocationDue, Payload: "not-json"}},
		{name: "trigger ready", n: db.Notification{Channel: db.NotifyTriggerReady, Payload: `{"app_id":"a1","source":"queue"}`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := queueInvocationApp(tt.n)
			if got != tt.want || ok != (tt.want != "") {
				t.Fatalf("queueInvocationApp() = %q, %v, want %q", got, ok, tt.want)
			}
		})
	}
}
