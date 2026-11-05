package main_test

// adr: 281

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdRealtimeSendReadsBinarySafeStdin(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, "", http.StatusAccepted)
	oldIn, oldOut := (*main.OsStdin), (*main.OsStdout)
	var out bytes.Buffer
	(*main.OsStdin) = strings.NewReader("hello\x00world")
	(*main.OsStdout) = &out
	t.Cleanup(func() {
		(*main.OsStdin) = oldIn
		(*main.OsStdout) = oldOut
	})

	if code := main.CmdRealtimeSend([]string{"demo", "endpoint-1", "conn-1", "--data-stdin", "--binary"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if (*f.SawMethod()) != http.MethodPost || (*f.SawPath()) != "/v1/apps/demo/realtime/endpoints/endpoint-1/connections/conn-1/send" {
		t.Fatalf("route = %s %s", (*f.SawMethod()), (*f.SawPath()))
	}
	var request map[string]any
	if err := json.Unmarshal((*f.SawBody()), &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if request["data_base64"] != base64.StdEncoding.EncodeToString([]byte("hello\x00world")) || request["binary"] != true {
		t.Fatalf("request = %s", (*f.SawBody()))
	}
	if strings.Contains(out.String(), "hello") {
		t.Fatalf("message data leaked into output: %s", out.String())
	}
}

func TestCmdRealtimePublishUsesChannelRouteAndReportsQueued(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"queued":3}`, http.StatusOK)
	oldOut := (*main.OsStdout)
	var out bytes.Buffer
	(*main.OsStdout) = &out
	t.Cleanup(func() { (*main.OsStdout) = oldOut })

	if code := main.CmdRealtimePublish([]string{"demo", "endpoint-1", "room-a", "--data", "hello"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if (*f.SawMethod()) != http.MethodPost || (*f.SawPath()) != "/v1/apps/demo/realtime/endpoints/endpoint-1/channels/room-a/publish" {
		t.Fatalf("route = %s %s", (*f.SawMethod()), (*f.SawPath()))
	}
	if !strings.Contains(out.String(), "3 connection(s)") {
		t.Fatalf("queued count missing: %s", out.String())
	}
}

func TestCmdRealtimePublishUsesChannelRouteAndReportsQueuedDocumentedStatus(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"queued":3,"subscribers":3}`, http.StatusOK)
	oldOut := (*main.OsStdout)
	var out bytes.Buffer
	(*main.OsStdout) = &out
	t.Cleanup(func() { (*main.OsStdout) = oldOut })

	if code := main.CmdRealtimePublish([]string{"demo", "endpoint-1", "room-a", "--data", "hello"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if (*f.SawMethod()) != http.MethodPost || (*f.SawPath()) != "/v1/apps/demo/realtime/endpoints/endpoint-1/channels/room-a/publish" {
		t.Fatalf("route = %s %s", (*f.SawMethod()), (*f.SawPath()))
	}
	if !strings.Contains(out.String(), "3 of 3 targeted connection(s)") {
		t.Fatalf("queued count missing: %s", out.String())
	}
}

func TestCmdRealtimePublishWarnsOnPartialFleet(t *testing.T) {
	main.ResetJSONOut(t)
	_ = main.AuthedFakeAPI(t, `{"queued":2,"partial":true,"nodes_queried":1,"nodes_unavailable":1}`, http.StatusOK)
	oldOut, oldErr := (*main.OsStdout), (*main.OsStderr)
	var out, errOut bytes.Buffer
	(*main.OsStdout), (*main.OsStderr) = &out, &errOut
	t.Cleanup(func() { (*main.OsStdout), (*main.OsStderr) = oldOut, oldErr })
	if code := main.CmdRealtimePublish([]string{"demo", "endpoint-1", "room-a", "--data", "hello"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "partial") || !strings.Contains(errOut.String(), "duplicates") {
		t.Fatalf("partial warning missing: %s", errOut.String())
	}
}

func TestCmdRealtimePublishWarnsOnPartialFleetDocumentedStatus(t *testing.T) {
	main.ResetJSONOut(t)
	_ = main.AuthedFakeAPI(t, `{"queued":2,"subscribers":3,"partial":true,"nodes_queried":1,"nodes_unavailable":1}`, http.StatusOK)
	oldOut, oldErr := (*main.OsStdout), (*main.OsStderr)
	var out, errOut bytes.Buffer
	(*main.OsStdout), (*main.OsStderr) = &out, &errOut
	t.Cleanup(func() { (*main.OsStdout), (*main.OsStderr) = oldOut, oldErr })
	if code := main.CmdRealtimePublish([]string{"demo", "endpoint-1", "room-a", "--data", "hello"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "partial") || !strings.Contains(errOut.String(), "duplicates") {
		t.Fatalf("partial warning missing: %s", errOut.String())
	}
}

func TestCmdRealtimeCloseAndSubscriptionRoutes(t *testing.T) {
	main.ResetJSONOut(t)
	tests := []struct {
		name string
		call func() int
		path string
	}{
		{
			name: "close",
			call: func() int {
				return main.CmdRealtimeClose([]string{"demo", "endpoint-1", "conn-1", "--reason", "migrated"})
			},
			path: "/v1/apps/demo/realtime/endpoints/endpoint-1/connections/conn-1/close",
		},
		{
			name: "subscribe",
			call: func() int { return main.CmdRealtimeSubscribe([]string{"demo", "endpoint-1", "conn-1", "room-a"}) },
			path: "/v1/apps/demo/realtime/endpoints/endpoint-1/connections/conn-1/subscriptions/room-a",
		},
		{
			name: "unsubscribe",
			call: func() int { return main.CmdRealtimeUnsubscribe([]string{"demo", "endpoint-1", "conn-1", "room-a"}) },
			path: "/v1/apps/demo/realtime/endpoints/endpoint-1/connections/conn-1/subscriptions/room-a",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := main.AuthedFakeAPI(t, "", http.StatusNoContent)
			oldOut := (*main.OsStdout)
			(*main.OsStdout) = &bytes.Buffer{}
			t.Cleanup(func() { (*main.OsStdout) = oldOut })
			if code := tt.call(); code != 0 {
				t.Fatalf("exit = %d", code)
			}
			if (*f.SawPath()) != tt.path {
				t.Fatalf("path = %s, want %s", (*f.SawPath()), tt.path)
			}
			wantMethod := http.MethodPut
			if tt.name == "close" {
				wantMethod = http.MethodPost
			} else if tt.name == "unsubscribe" {
				wantMethod = http.MethodDelete
			}
			if (*f.SawMethod()) != wantMethod {
				t.Fatalf("method = %s, want %s", (*f.SawMethod()), wantMethod)
			}
		})
	}
}

func TestRealtimeMessageRequestRejectsOversizedPayload(t *testing.T) {
	_, err := main.RealtimeMessageRequest(strings.Repeat("x", api.RealtimeMessageMaxBytes+1), false, false)
	if err == nil {
		t.Fatal("expected oversized message error")
	}
}
