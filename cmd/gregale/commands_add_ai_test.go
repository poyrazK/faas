package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// fakeAddAIAPI is an in-memory apid for the routes `gregale add ai` uses
// (ADR 20261027132936). It records every mutating call with its body.
type fakeAddAIAPI struct {
	mu     sync.Mutex
	offers []api.OutboundIntegrationOffer
	calls  []string
	bodies []map[string]any
}

func (f *fakeAddAIAPI) serve(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if r.Method != http.MethodGet {
			f.calls = append(f.calls, r.Method+" "+r.URL.Path)
			f.bodies = append(f.bodies, body)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/outbound/integrations":
			_ = json.NewEncoder(w).Encode(api.OutboundIntegrationOfferList{Items: f.offers})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/outbound/integrations":
			_ = json.NewEncoder(w).Encode(api.OutboundIntegrationOffer{ID: "int-1", Name: "openai", AIProvider: "openai"})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/apps/shop/outbound-bindings/"):
			_ = json.NewEncoder(w).Encode(api.OutboundAppBinding{})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
}

func TestCmdAddAICreatesBindsAndPointsTheSDK(t *testing.T) {
	f := &fakeAddAIAPI{}
	f.serve(t)
	t.Setenv("OPENAI_API_KEY", "sk-test")
	_ = captureAddDatadogStdout(t)

	if code := main.CmdAdd([]string{"ai", "--app", "shop", "--provider", "openai"}); code != 0 {
		t.Fatalf("add ai = %d, want 0", code)
	}
	want := "POST /v1/outbound/integrations,PUT /v1/outbound/integrations/int-1/credential,PUT /v1/apps/shop/outbound-bindings/int-1," +
		"PUT /v1/apps/shop/secrets/GREGALE_AI_BASE_URL,PUT /v1/apps/shop/secrets/OPENAI_API_KEY,PUT /v1/apps/shop/secrets/OPENAI_BASE_URL"
	if got := strings.Join(f.calls, ","); got != want {
		t.Fatalf("calls = %s\nwant    %s", got, want)
	}
	create, credential, baseURL := f.bodies[0], f.bodies[1], f.bodies[5]
	if create["ai_provider"] != "openai" || create["origin"] != "https://api.openai.com" || create["name"] != "openai" {
		t.Fatalf("create body = %v", create)
	}
	if credential["authorization"] != "Bearer sk-test" {
		t.Fatalf("credential body = %v; want the key as a bearer value", credential)
	}
	if baseURL["value"] != api.OutboundRelayURL+"/i/int-1/v1" {
		t.Fatalf("OPENAI_BASE_URL = %v", baseURL["value"])
	}
}

func TestCmdAddAIReusesTheIntegrationOfTheSameName(t *testing.T) {
	f := &fakeAddAIAPI{offers: []api.OutboundIntegrationOffer{{ID: "int-9", Name: "anthropic", AIProvider: "anthropic"}}}
	f.serve(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	_ = captureAddDatadogStdout(t)

	if code := main.CmdAdd([]string{"ai", "--app", "shop", "--provider", "anthropic", "--no-env"}); code != 0 {
		t.Fatalf("add ai = %d, want 0", code)
	}
	if got := strings.Join(f.calls, ","); got != "PUT /v1/outbound/integrations/int-9/credential,PUT /v1/apps/shop/outbound-bindings/int-9" {
		t.Fatalf("calls = %s; want the existing integration reused and no settings set", got)
	}
	if f.bodies[0]["authorization"] != "sk-ant-test" {
		t.Fatalf("credential body = %v; want the raw Anthropic key", f.bodies[0])
	}
}

func TestCmdAddAIProviderInputs(t *testing.T) {
	f := &fakeAddAIAPI{}
	f.serve(t)
	_ = captureAddDatadogStdout(t)
	if code := main.CmdAdd([]string{"ai", "--app", "shop", "--provider", "bedrock"}); code == 0 || len(f.calls) != 0 {
		t.Fatalf("bedrock without a region = %d, calls %v; want a refusal before any call", code, f.calls)
	}
	keyFile := filepath.Join(t.TempDir(), "key.json")
	if err := os.WriteFile(keyFile, []byte("{\n  \"type\": \"service_account\",\n  \"client_email\": \"a@b\"\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := main.CmdAdd([]string{"ai", "--app", "shop", "--provider", "vertex", "--region", "us-central1", "--key-file", keyFile, "--no-env"}); code != 0 {
		t.Fatalf("add ai vertex = %d", code)
	}
	if f.bodies[0]["origin"] != "https://us-central1-aiplatform.googleapis.com" ||
		f.bodies[1]["authorization"] != `{"type":"service_account","client_email":"a@b"}` {
		t.Fatalf("vertex create %v, credential %v; want the regional origin and the key on one line", f.bodies[0], f.bodies[1])
	}
}
