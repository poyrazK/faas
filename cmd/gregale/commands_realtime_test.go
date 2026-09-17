package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestCmdRealtimeAuthRotateReadsTokenFromStdin(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"endpoint_id":"endpoint-1","auth_mode":"static_bearer","previous_token_expires_at":"2026-09-17T12:15:00Z"}`, 200)

	oldIn, oldOut := osStdin, osStdout
	var out bytes.Buffer
	osStdin = strings.NewReader("replacement-token\n")
	osStdout = &out
	t.Cleanup(func() {
		osStdin = oldIn
		osStdout = oldOut
	})

	if code := cmdRealtimeAuthRotate([]string{"demo", "endpoint-1", "--token-stdin", "--grace-period", "900"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if f.sawMethod != "POST" || f.sawPath != "/v1/apps/demo/realtime/endpoints/endpoint-1/auth/rotate" {
		t.Fatalf("route = %s %s", f.sawMethod, f.sawPath)
	}
	var request map[string]any
	if err := json.Unmarshal(f.sawBody, &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if request["new_auth_token"] != "replacement-token" || request["grace_period_seconds"] != float64(900) {
		t.Fatalf("request = %s", f.sawBody)
	}
	if strings.Contains(out.String(), "replacement-token") {
		t.Fatalf("output exposed replacement token: %s", out.String())
	}
}

func TestCmdRealtimeAuthFinalizeUsesFinalizeRoute(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"endpoint_id":"endpoint-1","auth_mode":"static_bearer","previous_token_expires_at":null}`, 200)

	oldOut := osStdout
	var out bytes.Buffer
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })

	if code := cmdRealtimeAuthFinalize([]string{"demo", "endpoint-1"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if f.sawMethod != "POST" || f.sawPath != "/v1/apps/demo/realtime/endpoints/endpoint-1/auth/rotate/finalize" {
		t.Fatalf("route = %s %s", f.sawMethod, f.sawPath)
	}
	if strings.Contains(out.String(), "token") && !strings.Contains(out.String(), "previous_token_expires_at") {
		t.Fatalf("unexpected credential output: %s", out.String())
	}
}

func TestCmdRealtimeGetRendersRotationExpiryWithoutCredential(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"id":"endpoint-1","app_id":"app-1","account_id":"account-1","callback_url":"https://example.test/callback","connect_path":"/realtime/connect","message_path":"/realtime/message","disconnect_path":"/realtime/disconnect","callback_auth_token_masked":"***","auth_token_masked":"***","auth_token_previous_expires_at":"2026-09-17T12:15:00Z","auth_mode":"static_bearer","enabled":true,"allowed_origins":[],"max_connections":10,"max_message_bytes":1024,"max_connection_age_seconds":60,"created_at":"2026-09-17T11:00:00Z","updated_at":"2026-09-17T11:00:00Z"}`, 200)

	oldOut := osStdout
	var out bytes.Buffer
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })

	if code := cmdRealtimeGet([]string{"demo", "endpoint-1"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/apps/demo/realtime/endpoints/endpoint-1" {
		t.Fatalf("route = %s %s", f.sawMethod, f.sawPath)
	}
	if !strings.Contains(out.String(), "previous_token_expires_at:    2026-09-17T12:15:00Z") {
		t.Fatalf("expiry missing from output: %s", out.String())
	}
	if strings.Contains(out.String(), "replacement-token") {
		t.Fatalf("output exposed credential material: %s", out.String())
	}
}
