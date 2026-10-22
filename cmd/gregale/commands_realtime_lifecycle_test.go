package main_test

import (
	"bytes"
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"net/http"
	"strings"
	"testing"
)

func TestCmdRealtimeCreateReadsCallbackSecretFromStdin(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"id":"endpoint-1","app_id":"app-1","auth_mode":"static_bearer","auth_token_masked":"***","callback_auth_token_masked":"***","enabled":true}`, http.StatusCreated)
	oldIn, oldOut := (*main.OsStdin), (*main.OsStdout)
	var out bytes.Buffer
	(*main.OsStdin) = strings.NewReader("callback-secret\n")
	(*main.OsStdout) = &out
	t.Cleanup(func() {
		(*main.OsStdin) = oldIn
		(*main.OsStdout) = oldOut
	})

	if code := main.CmdRealtimeCreate([]string{
		"demo", "--callback-url", "https://app.example.test/events",
		"--callback-auth-token-stdin", "--auth-mode", "static_bearer",
		"--auth-token", "client-secret", "--allowed-origin", "https://app.example.test",
		"--max-connections", "50",
	}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if (*f.SawMethod()) != http.MethodPost || (*f.SawPath()) != "/v1/apps/demo/realtime/endpoints" {
		t.Fatalf("route = %s %s", (*f.SawMethod()), (*f.SawPath()))
	}
	var request map[string]any
	if err := json.Unmarshal((*f.SawBody()), &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if request["callback_auth_token"] != "callback-secret" || request["auth_token"] != "client-secret" || request["auth_mode"] != "static_bearer" {
		t.Fatalf("request = %s", (*f.SawBody()))
	}
	if strings.Contains(out.String(), "callback-secret") || strings.Contains(out.String(), "client-secret") {
		t.Fatalf("secret leaked into output: %s", out.String())
	}
}

func TestCmdRealtimeUpdateBuildsOIDCPolicy(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"id":"endpoint-1","app_id":"app-1","auth_mode":"oidc_jwt","enabled":true}`, http.StatusOK)
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = &bytes.Buffer{}
	t.Cleanup(func() { (*main.OsStdout) = oldOut })

	if code := main.CmdRealtimeUpdate([]string{
		"demo", "endpoint-1", "--auth-mode", "oidc_jwt",
		"--auth-issuer", "https://issuer.example.test",
		"--auth-jwks-url", "https://issuer.example.test/.well-known/jwks.json",
		"--auth-audience", "realtime", "--auth-algorithm", "RS256",
		"--auth-claim", "tenant=demo", "--allowed-origin", "https://app.example.test",
	}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if (*f.SawMethod()) != http.MethodPatch || (*f.SawPath()) != "/v1/apps/demo/realtime/endpoints/endpoint-1" {
		t.Fatalf("route = %s %s", (*f.SawMethod()), (*f.SawPath()))
	}
	var request struct {
		AuthMode          string            `json:"auth_mode"`
		AuthAudience      []string          `json:"auth_audience"`
		AuthAlgorithms    []string          `json:"auth_algorithms"`
		AuthRequiredClaim map[string]string `json:"auth_required_claims"`
		AllowedOrigins    []string          `json:"allowed_origins"`
	}
	if err := json.Unmarshal((*f.SawBody()), &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if request.AuthMode != "oidc_jwt" || len(request.AuthAudience) != 1 || request.AuthAudience[0] != "realtime" || len(request.AuthAlgorithms) != 1 || request.AuthAlgorithms[0] != "RS256" || request.AuthRequiredClaim["tenant"] != "demo" || len(request.AllowedOrigins) != 1 {
		t.Fatalf("request = %s", (*f.SawBody()))
	}
}

func TestCmdRealtimeDeleteRequiresConfirmation(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, "", http.StatusNoContent)
	if code := main.CmdRealtimeDelete([]string{"demo", "endpoint-1"}); code == 0 {
		t.Fatal("delete without --yes unexpectedly succeeded")
	}
	if (*f.SawMethod()) != "" || (*f.SawPath()) != "" {
		t.Fatalf("request was sent without confirmation: %s %s", (*f.SawMethod()), (*f.SawPath()))
	}
}

func TestCmdRealtimeDeleteUsesDeleteRoute(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, "", http.StatusNoContent)
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = &bytes.Buffer{}
	t.Cleanup(func() { (*main.OsStdout) = oldOut })

	if code := main.CmdRealtimeDelete([]string{"demo", "endpoint-1", "--yes"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if (*f.SawMethod()) != http.MethodDelete || (*f.SawPath()) != "/v1/apps/demo/realtime/endpoints/endpoint-1" {
		t.Fatalf("route = %s %s", (*f.SawMethod()), (*f.SawPath()))
	}
}
