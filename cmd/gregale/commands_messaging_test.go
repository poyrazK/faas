package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMessagingCommandsForwardExplicitIdempotencyKey(t *testing.T) {
	tests := []struct {
		name string
		path string
		args []string
		body string
	}{
		{
			name: "send",
			path: "/v1/apps/billing/inbox",
			args: []string{"billing", "--type", "invoice.created", "--data", `{}`, "--idempotency-key", "invoice-123"},
			body: `{"id":"inv-1","event_id":"event-1","target_app":"billing","status":"pending"}`,
		},
		{
			name: "deliver",
			path: "/v1/apps/billing/outbox",
			args: []string{"billing", "hook-1", "--type", "invoice.created", "--data", `{}`, "--idempotency-key", "invoice-123"},
			body: `{"id":"delivery-1","destination":"https://example.test/hook","status":"pending"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.path {
					t.Errorf("path = %q, want %q", r.URL.Path, tt.path)
				}
				if got := r.Header.Get("Idempotency-Key"); got != "invoice-123" {
					t.Errorf("Idempotency-Key = %q, want invoice-123", got)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "test-token")
			var code int
			if tt.name == "send" {
				code = cmdSend(tt.args)
			} else {
				code = cmdDeliver(tt.args)
			}
			if code != 0 {
				t.Fatalf("%s exit = %d", tt.name, code)
			}
		})
	}
}
