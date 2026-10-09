package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// fakeDatadogAPI is an in-memory apid for the drain and webhook routes the
// command uses. It records every mutating call with its decoded body.
type fakeDatadogAPI struct {
	mu     sync.Mutex
	drains []api.AppLogDrainResponse
	hooks  []api.AppWebhookResponse
	calls  []string
	bodies []map[string]any
}

func (f *fakeDatadogAPI) serve(t *testing.T) {
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
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/shop/log-drains":
			_ = json.NewEncoder(w).Encode(f.drains)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/shop/webhooks":
			_ = json.NewEncoder(w).Encode(f.hooks)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/shop/log-drains":
			_ = json.NewEncoder(w).Encode(api.AppLogDrainResponse{ID: "drain-new"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/shop/webhooks":
			_ = json.NewEncoder(w).Encode(api.AppWebhookResponse{ID: "hook-new"})
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{})
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
}

// TestCmdAddDatadog_ConfiguresDrainAndWebhook is the capability acceptance
// test (pkg/productcap/catalog.json, ADR-742).
func TestCmdAddDatadog_ConfiguresDrainAndWebhook(t *testing.T) {
	f := &fakeDatadogAPI{}
	f.serve(t)
	t.Setenv("DD_API_KEY", "dd-secret-key")
	stdout := captureAddDatadogStdout(t)

	if code := cmdAdd([]string{"datadog", "--app", "shop", "--site", "eu1"}); code != 0 {
		t.Fatalf("add datadog = %d, want 0", code)
	}
	if got := strings.Join(f.calls, ","); got != "POST /v1/apps/shop/log-drains,POST /v1/apps/shop/webhooks" {
		t.Fatalf("calls = %s", got)
	}
	drain, hook := f.bodies[0], f.bodies[1]
	if drain["kind"] != "datadog" || drain["target_url"] != "https://http-intake.logs.datadoghq.eu/api/v2/logs" || drain["auth_header"] != "DD-API-KEY: dd-secret-key" {
		t.Fatalf("drain body = %v", drain)
	}
	if hook["delivery_format"] != "datadog" || hook["target_url"] != "https://api.datadoghq.eu/api/v1/events" || hook["webhook_secret"] != "dd-secret-key" {
		t.Fatalf("webhook body = %v", hook)
	}
	if events, _ := hook["event_filter"].([]any); len(events) != len(api.DatadogWebhookEvents) {
		t.Fatalf("event_filter = %v, want the deployment and rollout events", hook["event_filter"])
	}
	out := stdout.String()
	if strings.Contains(out, "dd-secret-key") {
		t.Fatalf("output leaked the API key:\n%s", out)
	}
	if !strings.Contains(out, "Logs:   created") || !strings.Contains(out, "Events: created") {
		t.Fatalf("output = %s", out)
	}
}

func TestCmdAddDatadog_UpdatesExistingInPlace(t *testing.T) {
	f := &fakeDatadogAPI{
		drains: []api.AppLogDrainResponse{{ID: "drain-1", Kind: "http_json"}, {ID: "drain-dd", Kind: "datadog"}},
		hooks:  []api.AppWebhookResponse{{ID: "hook-dd", DeliveryFormat: "datadog"}},
	}
	f.serve(t)
	t.Setenv("DD_API_KEY", "rotated-key")
	captureAddDatadogStdout(t)

	if code := cmdAdd([]string{"datadog", "--app", "shop"}); code != 0 {
		t.Fatalf("add datadog = %d, want 0", code)
	}
	if got := strings.Join(f.calls, ","); got != "PATCH /v1/apps/shop/log-drains/drain-dd,PATCH /v1/apps/shop/webhooks/hook-dd" {
		t.Fatalf("calls = %s, want in-place updates of only the datadog drain and webhook", got)
	}
	if f.bodies[0]["auth_header"] != "DD-API-KEY: rotated-key" || f.bodies[1]["webhook_secret"] != "rotated-key" {
		t.Fatalf("rotation bodies = %v", f.bodies)
	}
}

func TestCmdAddDatadog_DryRunAndRemove(t *testing.T) {
	f := &fakeDatadogAPI{
		drains: []api.AppLogDrainResponse{{ID: "drain-dd", Kind: "datadog", TargetURL: "https://http-intake.logs.datadoghq.com/api/v2/logs"}},
		hooks:  []api.AppWebhookResponse{{ID: "hook-dd", DeliveryFormat: "datadog"}},
	}
	f.serve(t)
	captureAddDatadogStdout(t)

	if code := cmdAdd([]string{"datadog", "--app", "shop", "--dry-run"}); code != 0 {
		t.Fatalf("dry run = %d, want 0 (no key needed)", code)
	}
	if code := cmdAdd([]string{"datadog", "--app", "shop", "--remove", "--dry-run"}); code != 0 {
		t.Fatalf("remove dry run = %d", code)
	}
	if len(f.calls) != 0 {
		t.Fatalf("dry runs changed state: %v", f.calls)
	}
	if code := cmdAdd([]string{"datadog", "--app", "shop", "--remove"}); code != 0 {
		t.Fatalf("remove = %d", code)
	}
	if got := strings.Join(f.calls, ","); got != "DELETE /v1/apps/shop/log-drains/drain-dd,DELETE /v1/apps/shop/webhooks/hook-dd" {
		t.Fatalf("calls = %s", got)
	}
}

func TestCmdAddDatadog_KeySourcesAndValidation(t *testing.T) {
	f := &fakeDatadogAPI{}
	f.serve(t)
	t.Setenv("DD_API_KEY", "")
	_, _, restore := swapIO(t)
	defer restore()

	if code := cmdAdd([]string{"datadog", "--app", "shop"}); code == 0 {
		t.Fatal("a missing key must fail")
	}
	if code := cmdAdd([]string{"datadog", "--app", "shop", "--site", "mars1"}); code == 0 {
		t.Fatal("an unknown site must fail")
	}
	if len(f.calls) != 0 {
		t.Fatalf("validation failures must not call the API: %v", f.calls)
	}

	old := osStdin
	osStdin = strings.NewReader("piped-key\n")
	t.Cleanup(func() { osStdin = old })
	if code := cmdAdd([]string{"datadog", "--app", "shop", "--api-key-stdin"}); code != 0 {
		t.Fatalf("stdin key = %d, want 0", code)
	}
	if f.bodies[0]["auth_header"] != "DD-API-KEY: piped-key" {
		t.Fatalf("stdin key not used: %v", f.bodies[0])
	}
}

func captureAddDatadogStdout(t *testing.T) *strings.Builder {
	t.Helper()
	var b strings.Builder
	old := osStdout
	osStdout = &b
	t.Cleanup(func() { osStdout = old })
	return &b
}
