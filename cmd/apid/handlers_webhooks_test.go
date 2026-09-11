package main

// Smoke tests for the webhook handlers (issue #476 / ADR-076).
//
// Layering:
//   - handlers_webhooks_test.go: in-process end-to-end through the
//     HTTP mux. Uses setup(t, plan) (cmd/apid/server_test.go:31) for
//     store + key + server wiring; mirrors the alert-rule tests
//     at handlers_alerts_test.go.
//
// The deeper per-endpoint matrix (rotation, retry-from-dead, SSRF
// rejection, quota gate) lives in a follow-up when the e2e harness
// ships (issue #476 PR cluster commit 7). This file pins:
//   - Happy path: create → list → delete round-trip
//   - Plan-tier gate: Free plan → 402 plan_webhooks_not_allowed
//   - Closed-set drift: retry_policy / event_filter typo → 400
//   - Masked secret: webhook_secret_sealed_masked == "***" on response
//   - Plaintext never lands in the sealed ciphertext row

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func webhookReq() api.CreateAppWebhookRequest {
	return api.CreateAppWebhookRequest{
		TargetURL:     "https://example.com/hook",
		WebhookSecret: "shh-test",
		EventFilter:   []string{"cron.fired"},
		RetryPolicy:   "default",
	}
}

func mustCreateWebhook(t *testing.T, e testEnv, slug string, req api.CreateAppWebhookRequest) api.AppWebhookResponse {
	t.Helper()
	rec := e.do(t, "POST", "/v1/apps/"+slug+"/webhooks", req, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create webhook status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppWebhookResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func setupWebhookTest(t *testing.T, plan api.Plan) testEnv {
	t.Helper()
	teardown := withTestRecipient(t)
	t.Cleanup(teardown)
	return setup(t, plan)
}

// TestCreateAppWebhook_HappyPath pins the basic round-trip:
//   - 201 on create
//   - webhook_secret_sealed_masked == "***"
//   - retry_policy / event_filter preserved verbatim
//   - store row carries the sealed ciphertext (plaintext is gone)
func TestCreateAppWebhook_HappyPath(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	mustSeedApp(t, e, "wh-hp")
	out := mustCreateWebhook(t, e, "wh-hp", webhookReq())
	if out.ID == "" {
		t.Errorf("ID is empty")
	}
	if out.WebhookSecretSealedMasked != api.AppWebhookSecretMasked {
		t.Errorf("WebhookSecretSealedMasked = %q, want %q", out.WebhookSecretSealedMasked, api.AppWebhookSecretMasked)
	}
	if !strings.Contains(out.WebhookSecretSealedMasked, "*") {
		t.Errorf("masked constant is not masked: %q", out.WebhookSecretSealedMasked)
	}
	if out.RetryPolicy != "default" {
		t.Errorf("retry_policy: got %q, want %q", out.RetryPolicy, "default")
	}
	if len(out.EventFilter) != 1 || out.EventFilter[0] != "cron.fired" {
		t.Errorf("event_filter: got %v, want [cron.fired]", out.EventFilter)
	}
	row, err := e.store.AppWebhookByID(context.Background(), out.ID)
	if err != nil {
		t.Fatalf("AppWebhookByID: %v", err)
	}
	if len(row.SecretSealed) == 0 {
		t.Errorf("SecretSealed is empty — seal did not happen")
	}
	// Plaintext must NEVER land on disk.
	if strings.Contains(string(row.SecretSealed), "shh-test") {
		t.Errorf("plaintext leaked into sealed ciphertext (regression)")
	}
}

// TestListAppWebhooks_HappyPath exercises GET /v1/apps/{slug}/webhooks
// and pins the empty-slice shape when no webhooks are configured.
func TestListAppWebhooks_HappyPath(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	mustSeedApp(t, e, "wh-list")
	mustCreateWebhook(t, e, "wh-list", webhookReq())
	rec := e.do(t, "GET", "/v1/apps/wh-list/webhooks", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out []api.AppWebhookResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("len = %d, want 1", len(out))
	}
}

// TestCreateAppWebhook_FreeReturns402 pins the plan-tier gate: Free
// customers see 402 plan_webhooks_not_allowed. Mirrors the alert-rule
// precedent.
func TestCreateAppWebhook_FreeReturns402(t *testing.T) {
	e := setupWebhookTest(t, api.PlanFree)
	mustSeedApp(t, e, "wh-free")
	rec := e.do(t, "POST", "/v1/apps/wh-free/webhooks", webhookReq(), nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Errorf("status: got %d, want 402 (Free plan)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "plan_webhooks_not_allowed") {
		t.Errorf("body missing plan_webhooks_not_allowed code: %s", rec.Body)
	}
}

// TestCreateAppWebhook_RetryPolicyOutOfVocabulary pins the closed-set
// drift rejection: a typo (e.g. "defaultt") surfaces as 400
// app_webhook_invalid.
func TestCreateAppWebhook_RetryPolicyOutOfVocabulary(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	mustSeedApp(t, e, "wh-bad-policy")
	req := webhookReq()
	req.RetryPolicy = "defaultt"
	rec := e.do(t, "POST", "/v1/apps/wh-bad-policy/webhooks", req, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "app_webhook_invalid") {
		t.Errorf("body missing app_webhook_invalid code: %s", rec.Body)
	}
}

// TestCreateAppWebhook_EventOutOfVocabulary pins the event closed-set
// drift rejection.
func TestCreateAppWebhook_EventOutOfVocabulary(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	mustSeedApp(t, e, "wh-bad-event")
	req := webhookReq()
	req.EventFilter = []string{"not.a.real.event"}
	rec := e.do(t, "POST", "/v1/apps/wh-bad-event/webhooks", req, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "app_webhook_invalid") {
		t.Errorf("body missing app_webhook_invalid code: %s", rec.Body)
	}
}

// TestDeleteAppWebhook_HappyPath pins the DELETE → 204 round-trip.
func TestDeleteAppWebhook_HappyPath(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	mustSeedApp(t, e, "wh-del")
	created := mustCreateWebhook(t, e, "wh-del", webhookReq())
	rec := e.do(t, "DELETE", "/v1/apps/wh-del/webhooks/"+created.ID, nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want 204", rec.Code)
	}
	// Subsequent get → 404.
	rec = e.do(t, "GET", "/v1/apps/wh-del/webhooks/"+created.ID, nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("post-delete get: got %d, want 404", rec.Code)
	}
}

// TestRotateAppWebhookSecret_HappyPath pins the rotate-secret
// endpoint: the customer supplies a known replacement, while the response
// remains masked.
func TestRotateAppWebhookSecret_HappyPath(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	mustSeedApp(t, e, "wh-rotate")
	created := mustCreateWebhook(t, e, "wh-rotate", webhookReq())
	before, err := e.store.AppWebhookByID(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, "POST", "/v1/apps/wh-rotate/webhooks/"+created.ID+"/rotate-secret", api.RotateAppWebhookSecretRequest{WebhookSecret: "known-replacement"}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.RotateAppWebhookSecretResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.WebhookSecretSealedMasked != api.AppWebhookSecretMasked {
		t.Errorf("masked: got %q, want %q", out.WebhookSecretSealedMasked, api.AppWebhookSecretMasked)
	}
	if out.RotatedAt == "" {
		t.Errorf("rotated_at missing from response")
	}
	after, err := e.store.AppWebhookByID(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(after.SecretSealed) == string(before.SecretSealed) {
		t.Error("sealed secret did not change")
	}
	if strings.Contains(rec.Body.String(), "known-replacement") || strings.Contains(string(after.SecretSealed), "known-replacement") {
		t.Error("replacement plaintext leaked into response or persisted ciphertext")
	}
	namespace, plaintext, err := secretbox.OpenBytes(mfaIdentities()[0], after.SecretSealed)
	if err != nil {
		t.Fatal(err)
	}
	if namespace != appWebhookSecretSealLabel || string(plaintext) != "known-replacement" {
		t.Fatalf("unsealed rotation = namespace %q plaintext %q", namespace, plaintext)
	}
}

func TestRotateAppWebhookSecret_RequiresReplacement(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	mustSeedApp(t, e, "wh-rotate-empty")
	created := mustCreateWebhook(t, e, "wh-rotate-empty", webhookReq())
	rec := e.do(t, "POST", "/v1/apps/wh-rotate-empty/webhooks/"+created.ID+"/rotate-secret", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}
