package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestCmdRealtimeCreateReadsCallbackSecretFromStdin(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"id":"endpoint-1","app_id":"app-1","auth_mode":"static_bearer","auth_token_masked":"***","callback_auth_token_masked":"***","enabled":true}`, http.StatusCreated)
	oldIn, oldOut := osStdin, osStdout
	var out bytes.Buffer
	osStdin = strings.NewReader("callback-secret\n")
	osStdout = &out
	t.Cleanup(func() {
		osStdin = oldIn
		osStdout = oldOut
	})

	if code := cmdRealtimeCreate([]string{
		"demo", "--callback-url", "https://app.example.test/events",
		"--callback-auth-token-stdin", "--auth-mode", "static_bearer",
		"--auth-token", "client-secret", "--allowed-origin", "https://app.example.test",
		"--max-connections", "50",
	}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if f.sawMethod != http.MethodPost || f.sawPath != "/v1/apps/demo/realtime/endpoints" {
		t.Fatalf("route = %s %s", f.sawMethod, f.sawPath)
	}
	var request map[string]any
	if err := json.Unmarshal(f.sawBody, &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if request["callback_auth_token"] != "callback-secret" || request["auth_token"] != "client-secret" || request["auth_mode"] != "static_bearer" {
		t.Fatalf("request = %s", f.sawBody)
	}
	if strings.Contains(out.String(), "callback-secret") || strings.Contains(out.String(), "client-secret") {
		t.Fatalf("secret leaked into output: %s", out.String())
	}
}

func TestCmdRealtimeUpdateBuildsOIDCPolicy(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"id":"endpoint-1","app_id":"app-1","auth_mode":"oidc_jwt","enabled":true}`, http.StatusOK)
	oldOut := osStdout
	osStdout = &bytes.Buffer{}
	t.Cleanup(func() { osStdout = oldOut })

	if code := cmdRealtimeUpdate([]string{
		"demo", "endpoint-1", "--auth-mode", "oidc_jwt",
		"--auth-issuer", "https://issuer.example.test",
		"--auth-jwks-url", "https://issuer.example.test/.well-known/jwks.json",
		"--auth-audience", "realtime", "--auth-algorithm", "RS256",
		"--auth-claim", "tenant=demo", "--allowed-origin", "https://app.example.test",
	}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if f.sawMethod != http.MethodPatch || f.sawPath != "/v1/apps/demo/realtime/endpoints/endpoint-1" {
		t.Fatalf("route = %s %s", f.sawMethod, f.sawPath)
	}
	var request struct {
		AuthMode          string            `json:"auth_mode"`
		AuthAudience      []string          `json:"auth_audience"`
		AuthAlgorithms    []string          `json:"auth_algorithms"`
		AuthRequiredClaim map[string]string `json:"auth_required_claims"`
		AllowedOrigins    []string          `json:"allowed_origins"`
	}
	if err := json.Unmarshal(f.sawBody, &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if request.AuthMode != "oidc_jwt" || len(request.AuthAudience) != 1 || request.AuthAudience[0] != "realtime" || len(request.AuthAlgorithms) != 1 || request.AuthAlgorithms[0] != "RS256" || request.AuthRequiredClaim["tenant"] != "demo" || len(request.AllowedOrigins) != 1 {
		t.Fatalf("request = %s", f.sawBody)
	}
}

func TestCmdRealtimeDeleteRequiresConfirmation(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, "", http.StatusNoContent)
	if code := cmdRealtimeDelete([]string{"demo", "endpoint-1"}); code == 0 {
		t.Fatal("delete without --yes unexpectedly succeeded")
	}
	if f.sawMethod != "" || f.sawPath != "" {
		t.Fatalf("request was sent without confirmation: %s %s", f.sawMethod, f.sawPath)
	}
}

func TestCmdRealtimeDeleteUsesDeleteRoute(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, "", http.StatusNoContent)
	oldOut := osStdout
	osStdout = &bytes.Buffer{}
	t.Cleanup(func() { osStdout = oldOut })

	if code := cmdRealtimeDelete([]string{"demo", "endpoint-1", "--yes"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if f.sawMethod != http.MethodDelete || f.sawPath != "/v1/apps/demo/realtime/endpoints/endpoint-1" {
		t.Fatalf("route = %s %s", f.sawMethod, f.sawPath)
	}
}
