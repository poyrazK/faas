package main_test

import (
	"bytes"
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"strings"
	"testing"
)

func TestCmdRealtimeAuthRotateReadsTokenFromStdin(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"endpoint_id":"endpoint-1","auth_mode":"static_bearer","previous_token_expires_at":"2026-09-17T12:15:00Z"}`, 200)

	oldIn, oldOut := (*main.OsStdin), (*main.OsStdout)
	var out bytes.Buffer
	(*main.OsStdin) = strings.NewReader("replacement-token\n")
	(*main.OsStdout) = &out
	t.Cleanup(func() {
		(*main.OsStdin) = oldIn
		(*main.OsStdout) = oldOut
	})

	if code := main.CmdRealtimeAuthRotate([]string{"demo", "endpoint-1", "--token-stdin", "--grace-period", "900"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if (*f.SawMethod()) != "POST" || (*f.SawPath()) != "/v1/apps/demo/realtime/endpoints/endpoint-1/auth/rotate" {
		t.Fatalf("route = %s %s", (*f.SawMethod()), (*f.SawPath()))
	}
	var request map[string]any
	if err := json.Unmarshal((*f.SawBody()), &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if request["new_auth_token"] != "replacement-token" || request["grace_period_seconds"] != float64(900) {
		t.Fatalf("request = %s", (*f.SawBody()))
	}
	if strings.Contains(out.String(), "replacement-token") {
		t.Fatalf("output exposed replacement token: %s", out.String())
	}
}

func TestCmdRealtimeAuthFinalizeUsesFinalizeRoute(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"endpoint_id":"endpoint-1","auth_mode":"static_bearer","previous_token_expires_at":null}`, 200)

	oldOut := (*main.OsStdout)
	var out bytes.Buffer
	(*main.OsStdout) = &out
	t.Cleanup(func() { (*main.OsStdout) = oldOut })

	if code := main.CmdRealtimeAuthFinalize([]string{"demo", "endpoint-1"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if (*f.SawMethod()) != "POST" || (*f.SawPath()) != "/v1/apps/demo/realtime/endpoints/endpoint-1/auth/rotate/finalize" {
		t.Fatalf("route = %s %s", (*f.SawMethod()), (*f.SawPath()))
	}
	if strings.Contains(out.String(), "token") && !strings.Contains(out.String(), "previous_token_expires_at") {
		t.Fatalf("unexpected credential output: %s", out.String())
	}
}

func TestCmdRealtimeGetRendersRotationExpiryWithoutCredential(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"id":"endpoint-1","app_id":"app-1","account_id":"account-1","callback_url":"https://example.test/callback","connect_path":"/realtime/connect","message_path":"/realtime/message","disconnect_path":"/realtime/disconnect","callback_auth_token_masked":"***","auth_token_masked":"***","auth_token_previous_expires_at":"2026-09-17T12:15:00Z","auth_mode":"static_bearer","enabled":true,"allowed_origins":[],"max_connections":10,"max_message_bytes":1024,"max_connection_age_seconds":60,"created_at":"2026-09-17T11:00:00Z","updated_at":"2026-09-17T11:00:00Z"}`, 200)

	oldOut := (*main.OsStdout)
	var out bytes.Buffer
	(*main.OsStdout) = &out
	t.Cleanup(func() { (*main.OsStdout) = oldOut })

	if code := main.CmdRealtimeGet([]string{"demo", "endpoint-1"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if (*f.SawMethod()) != "GET" || (*f.SawPath()) != "/v1/apps/demo/realtime/endpoints/endpoint-1" {
		t.Fatalf("route = %s %s", (*f.SawMethod()), (*f.SawPath()))
	}
	if !strings.Contains(out.String(), "previous_token_expires_at:    2026-09-17T12:15:00Z") {
		t.Fatalf("expiry missing from output: %s", out.String())
	}
	if strings.Contains(out.String(), "replacement-token") {
		t.Fatalf("output exposed credential material: %s", out.String())
	}
}
