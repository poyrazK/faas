package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdRealtimeSendReadsBinarySafeStdin(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, "", http.StatusAccepted)
	oldIn, oldOut := osStdin, osStdout
	var out bytes.Buffer
	osStdin = strings.NewReader("hello\x00world")
	osStdout = &out
	t.Cleanup(func() {
		osStdin = oldIn
		osStdout = oldOut
	})

	if code := cmdRealtimeSend([]string{"demo", "endpoint-1", "conn-1", "--data-stdin", "--binary"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if f.sawMethod != http.MethodPost || f.sawPath != "/v1/apps/demo/realtime/endpoints/endpoint-1/connections/conn-1/send" {
		t.Fatalf("route = %s %s", f.sawMethod, f.sawPath)
	}
	var request map[string]any
	if err := json.Unmarshal(f.sawBody, &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if request["data_base64"] != base64.StdEncoding.EncodeToString([]byte("hello\x00world")) || request["binary"] != true {
		t.Fatalf("request = %s", f.sawBody)
	}
	if strings.Contains(out.String(), "hello") {
		t.Fatalf("message data leaked into output: %s", out.String())
	}
}

func TestCmdRealtimePublishUsesChannelRouteAndReportsQueued(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"queued":3}`, http.StatusOK)
	oldOut := osStdout
	var out bytes.Buffer
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })

	if code := cmdRealtimePublish([]string{"demo", "endpoint-1", "room-a", "--data", "hello"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if f.sawMethod != http.MethodPost || f.sawPath != "/v1/apps/demo/realtime/endpoints/endpoint-1/channels/room-a/publish" {
		t.Fatalf("route = %s %s", f.sawMethod, f.sawPath)
	}
	if !strings.Contains(out.String(), "3 connection(s)") {
		t.Fatalf("queued count missing: %s", out.String())
	}
}

func TestCmdRealtimeCloseAndSubscriptionRoutes(t *testing.T) {
	resetJSONOut(t)
	tests := []struct {
		name string
		call func() int
		path string
	}{
		{
			name: "close",
			call: func() int { return cmdRealtimeClose([]string{"demo", "endpoint-1", "conn-1", "--reason", "migrated"}) },
			path: "/v1/apps/demo/realtime/endpoints/endpoint-1/connections/conn-1/close",
		},
		{
			name: "subscribe",
			call: func() int { return cmdRealtimeSubscribe([]string{"demo", "endpoint-1", "conn-1", "room-a"}) },
			path: "/v1/apps/demo/realtime/endpoints/endpoint-1/connections/conn-1/subscriptions/room-a",
		},
		{
			name: "unsubscribe",
			call: func() int { return cmdRealtimeUnsubscribe([]string{"demo", "endpoint-1", "conn-1", "room-a"}) },
			path: "/v1/apps/demo/realtime/endpoints/endpoint-1/connections/conn-1/subscriptions/room-a",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := authedFakeAPI(t, "", http.StatusNoContent)
			oldOut := osStdout
			osStdout = &bytes.Buffer{}
			t.Cleanup(func() { osStdout = oldOut })
			if code := tt.call(); code != 0 {
				t.Fatalf("exit = %d", code)
			}
			if f.sawPath != tt.path {
				t.Fatalf("path = %s, want %s", f.sawPath, tt.path)
			}
			wantMethod := http.MethodPut
			if tt.name == "close" {
				wantMethod = http.MethodPost
			} else if tt.name == "unsubscribe" {
				wantMethod = http.MethodDelete
			}
			if f.sawMethod != wantMethod {
				t.Fatalf("method = %s, want %s", f.sawMethod, wantMethod)
			}
		})
	}
}

func TestRealtimeMessageRequestRejectsOversizedPayload(t *testing.T) {
	_, err := realtimeMessageRequest(strings.Repeat("x", api.RealtimeMessageMaxBytes+1), false, false)
	if err == nil {
		t.Fatal("expected oversized message error")
	}
}
