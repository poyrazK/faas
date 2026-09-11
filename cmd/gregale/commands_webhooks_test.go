// Tests for the `gregale webhooks <list|add|update|rm|deliveries|retry>`
// subcommands. Mirrors commands_crons_update_test.go's httptest + t.Setenv
// shape — the dispatch placement lives in main_test.go.
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// webhookTestID is the 32-hex id every test uses. Matches the
// webhookIDPattern in commands_webhooks.go.
const webhookTestID = "0123456789abcdef0123456789abcdef"

func TestCmdWebhooks_Add_HappyPath(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody api.CreateAppWebhookRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(api.AppWebhookResponse{
			ID:                        webhookTestID,
			AppID:                     "app-1",
			AccountID:                 "acct-1",
			TargetURL:                 "https://example.com/hook",
			WebhookSecretSealedMasked: api.AppWebhookSecretMasked,
			EventFilter:               []string{"cron.fired"},
			RetryPolicy:               "default",
			Enabled:                   true,
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	oldOut, oldJSON := osStdout, jsonOutput
	var stdout bytes.Buffer
	osStdout, jsonOutput = &stdout, false
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })

	if code := cmdWebhooksAdd([]string{
		"--app", "demo",
		"--target-url", "https://example.com/hook",
		"--secret", "shh",
		"--event", "cron.fired",
	}); code != 0 {
		t.Errorf("add = %d, want 0", code)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/apps/demo/webhooks" {
		t.Errorf("path = %q, want /v1/apps/demo/webhooks", gotPath)
	}
	if gotBody.WebhookSecret != "shh" {
		t.Errorf("body.webhook_secret = %q, want %q", gotBody.WebhookSecret, "shh")
	}
	if gotBody.RetryPolicy != "default" {
		t.Errorf("body.retry_policy = %q, want default", gotBody.RetryPolicy)
	}
	if strings.Contains(stdout.String(), "%!") || !strings.Contains(stdout.String(), webhookTestID) {
		t.Errorf("malformed human output: %q", stdout.String())
	}
}

func TestCmdWebhooks_Add_BadRetryPolicyRejected(t *testing.T) {
	// A typo in --retry-policy surfaces locally before the round-trip
	// (mirrors cmdApp's --eviction-priority check, PR #647).
	t.Setenv("FAAS_API", "http://localhost")
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdWebhooksAdd([]string{
		"--app", "demo",
		"--target-url", "https://example.com/hook",
		"--retry-policy", "defaultt",
	}); code == 0 {
		t.Errorf("typo --retry-policy accepted; want non-zero exit")
	}
}

func TestCmdWebhooks_Rm_HappyPath(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdWebhooksRm([]string{"--app", "demo", webhookTestID}); code != 0 {
		t.Errorf("rm = %d, want 0", code)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/v1/apps/demo/webhooks/"+webhookTestID {
		t.Errorf("path = %q, want /v1/apps/demo/webhooks/<id>", gotPath)
	}
}

func TestCmdWebhooks_Retry_HappyPath(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(api.AppWebhookRetryDeliveryResponse{
			Delivery: api.AppWebhookDeliveryResponse{
				ID:            "deadbeef00000000deadbeef00000000",
				WebhookID:     webhookTestID,
				Event:         "cron.fired",
				Attempt:       0,
				Status:        "pending",
				NextAttemptAt: "2026-08-06T12:00:00Z",
			},
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdWebhookRetry([]string{
		"--app", "demo",
		webhookTestID, "deadbeef00000000deadbeef00000000",
	}); code != 0 {
		t.Errorf("retry = %d, want 0", code)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/deliveries/deadbeef00000000deadbeef00000000/retry") {
		t.Errorf("path = %q, want suffix /deliveries/<did>/retry", gotPath)
	}
}

func TestCmdWebhookDeliveries_JSONPreservesCursor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.AppWebhookDeliveryListResponse{
			Deliveries: []api.AppWebhookDeliveryResponse{{ID: "delivery-1", Event: "app.woken", Status: "pending"}},
			NextToken:  "cursor-next",
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	oldOut, oldJSON := osStdout, jsonOutput
	var stdout bytes.Buffer
	osStdout, jsonOutput = &stdout, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })

	if code := cmdWebhookDeliveries([]string{"--app", "demo", webhookTestID}); code != 0 {
		t.Fatalf("deliveries = %d, want 0", code)
	}
	var got api.AppWebhookDeliveryListResponse
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("JSON output: %v\n%s", err, stdout.String())
	}
	if got.NextToken != "cursor-next" || len(got.Deliveries) != 1 {
		t.Fatalf("output = %+v, want delivery envelope with cursor", got)
	}
}

func TestCmdWebhookMutations_HonorJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch:
			_ = json.NewEncoder(w).Encode(api.AppWebhookResponse{ID: webhookTestID, TargetURL: "https://example.com/hook"})
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/retry"):
			_ = json.NewEncoder(w).Encode(api.AppWebhookRetryDeliveryResponse{Delivery: api.AppWebhookDeliveryResponse{ID: "delivery-1", Status: "pending"}})
		case strings.HasSuffix(r.URL.Path, "/rotate-secret"):
			_ = json.NewEncoder(w).Encode(api.RotateAppWebhookSecretResponse{RotatedAt: "2026-09-10T12:00:00Z", WebhookSecretSealedMasked: "***"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	oldOut, oldJSON := osStdout, jsonOutput
	var stdout bytes.Buffer
	osStdout, jsonOutput = &stdout, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })

	calls := []struct {
		name string
		call func() int
	}{
		{"update", func() int { return cmdWebhooksUpdate([]string{"--app", "demo", "--enable", webhookTestID}) }},
		{"remove", func() int { return cmdWebhooksRm([]string{"--app", "demo", webhookTestID}) }},
		{"retry", func() int { return cmdWebhookRetry([]string{"--app", "demo", webhookTestID, "delivery-1"}) }},
		{"rotate", func() int {
			return cmdWebhookRotateSecret([]string{"--app", "demo", "--secret", "replacement", webhookTestID})
		}},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			stdout.Reset()
			if code := tc.call(); code != 0 {
				t.Fatalf("exit = %d, want 0", code)
			}
			var got map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, stdout.String())
			}
		})
	}
}
