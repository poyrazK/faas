package main

// ADR-212 acceptance tests for signature-verified, durable-before-202 inbound
// webhook ingress and deterministic provider-retry deduplication.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	stripex "github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/onebox-faas/faas/pkg/state"
)

const inboundWebhookTestSecret = "whsec_inbound_test"

func mustCreateInboundWebhook(t *testing.T, e testEnv, slug string) api.InboundWebhookEndpointResponse {
	t.Helper()
	rec := e.do(t, http.MethodPost, "/v1/apps/"+slug+"/inbound-webhooks", api.CreateInboundWebhookEndpointRequest{
		Name:          "stripe-primary",
		Provider:      "stripe",
		SigningSecret: inboundWebhookTestSecret,
		DeliveryPath:  "/internal/stripe",
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create inbound webhook status %d: %s", rec.Code, rec.Body.String())
	}
	var endpoint api.InboundWebhookEndpointResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &endpoint); err != nil {
		t.Fatalf("decode inbound webhook endpoint: %v", err)
	}
	if endpoint.EndpointURL == "" {
		t.Fatal("create response omitted one-time endpoint_url")
	}
	return endpoint
}

func postStripeInboundWebhook(t *testing.T, e testEnv, endpointURL string, body []byte, secret string) *httptest.ResponseRecorder {
	t.Helper()
	u, err := url.Parse(endpointURL)
	if err != nil {
		t.Fatalf("parse endpoint URL: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, u.Path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Stripe-Signature", stripex.SignForTest(body, secret, time.Now()))
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func TestInboundWebhookAcceptsDurablyAndDeduplicatesProviderRetries(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	appID := mustSeedApp(t, e, "inbound-stripe")
	retryPolicy := []byte(`{"max_attempts":20,"base_seconds":2,"max_seconds":30}`)
	if _, err := e.store.UpdateApp(t.Context(), appID, state.UpdateAppParams{RetryPolicyJSON: &retryPolicy, SetRetryPolicy: true}); err != nil {
		t.Fatalf("set app retry policy: %v", err)
	}
	endpoint := mustCreateInboundWebhook(t, e, "inbound-stripe")
	body := []byte(`{"id":"evt_sleeping_app","object":"event","type":"checkout.session.completed"}`)

	first := postStripeInboundWebhook(t, e, endpoint.EndpointURL, body, inboundWebhookTestSecret)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first ingress status %d: %s", first.Code, first.Body.String())
	}
	var receipt api.InboundWebhookReceiptResponse
	if err := json.Unmarshal(first.Body.Bytes(), &receipt); err != nil {
		t.Fatalf("decode first receipt: %v", err)
	}
	if receipt.Duplicate {
		t.Fatal("first delivery marked duplicate")
	}
	wantReceiptID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:inbound-webhook:"+endpoint.ID+"\x00evt_sleeping_app")).String()
	if receipt.ReceiptID != wantReceiptID {
		t.Fatalf("receipt_id = %q, want %q", receipt.ReceiptID, wantReceiptID)
	}

	// A 202 is emitted only after EnqueueInvocation has returned its committed
	// durable row. This lookup therefore pins the acknowledgement boundary.
	invocation, err := e.store.InvocationByID(t.Context(), receipt.ReceiptID)
	if err != nil {
		t.Fatalf("202 returned without durable invocation: %v", err)
	}
	if invocation.AppID != appID || invocation.State != state.InvocationPending || invocation.Source != state.InvocationInboundWebhook {
		t.Fatalf("unexpected durable invocation: %#v", invocation)
	}
	if invocation.Method != http.MethodPost || invocation.Path != "/internal/stripe" || !bytes.Equal(invocation.Payload, body) {
		t.Fatalf("delivery envelope mismatch: method=%q path=%q payload=%s", invocation.Method, invocation.Path, invocation.Payload)
	}
	var persistedRetryPolicy api.RetryPolicyDTO
	if err := json.Unmarshal(invocation.RetryPolicyJSON, &persistedRetryPolicy); err != nil {
		t.Fatalf("decode persisted retry policy: %v", err)
	}
	wantMaxAttempts := api.MustLimitsFor(api.PlanPro).MaxQueueAttempts
	if persistedRetryPolicy.MaxAttempts != wantMaxAttempts || persistedRetryPolicy.BaseSeconds != 2 || persistedRetryPolicy.MaxSeconds != 30 {
		t.Fatalf("retry policy = %+v, want app default capped to plan max attempts %d", persistedRetryPolicy, wantMaxAttempts)
	}
	var headers map[string]string
	if err := json.Unmarshal(invocation.Headers, &headers); err != nil {
		t.Fatalf("decode stored headers: %v", err)
	}
	if headers["x-gregale-webhook-event-id"] != "evt_sleeping_app" || headers["x-gregale-webhook-provider"] != "stripe" {
		t.Fatalf("missing verification attestation headers: %#v", headers)
	}

	second := postStripeInboundWebhook(t, e, endpoint.EndpointURL, body, inboundWebhookTestSecret)
	if second.Code != http.StatusAccepted {
		t.Fatalf("retry ingress status %d: %s", second.Code, second.Body.String())
	}
	var duplicate api.InboundWebhookReceiptResponse
	if err := json.Unmarshal(second.Body.Bytes(), &duplicate); err != nil {
		t.Fatalf("decode retry receipt: %v", err)
	}
	if !duplicate.Duplicate || duplicate.ReceiptID != receipt.ReceiptID {
		t.Fatalf("retry receipt = %#v, want same id marked duplicate", duplicate)
	}
	due, err := e.store.ListDueInvocations(t.Context(), time.Now().Add(time.Second), 10)
	if err != nil {
		t.Fatalf("list due invocations: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("provider retry created %d durable deliveries, want 1", len(due))
	}
}

func TestInboundWebhookRejectsBadSignatureWithoutDurableReceipt(t *testing.T) {
	e := setupWebhookTest(t, api.PlanHobby)
	mustSeedApp(t, e, "inbound-bad-sig")
	endpoint := mustCreateInboundWebhook(t, e, "inbound-bad-sig")
	body := []byte(`{"id":"evt_untrusted","object":"event"}`)

	rec := postStripeInboundWebhook(t, e, endpoint.EndpointURL, body, "wrong-secret")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad signature status %d: %s", rec.Code, rec.Body.String())
	}
	receiptID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:inbound-webhook:"+endpoint.ID+"\x00evt_untrusted")).String()
	if _, err := e.store.InvocationByID(t.Context(), receiptID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("bad signature created durable receipt: %v", err)
	}
}

func TestInboundWebhookManagementRotatesSecretAndPreservesOneTimeURL(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	mustSeedApp(t, e, "inbound-manage")
	endpoint := mustCreateInboundWebhook(t, e, "inbound-manage")

	list := e.do(t, http.MethodGet, "/v1/apps/inbound-manage/inbound-webhooks", nil, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", list.Code, list.Body.String())
	}
	var endpoints []api.InboundWebhookEndpointResponse
	if err := json.Unmarshal(list.Body.Bytes(), &endpoints); err != nil || len(endpoints) != 1 {
		t.Fatalf("list response = %#v, %v", endpoints, err)
	}
	if endpoints[0].EndpointURL != "" || endpoints[0].SigningSecretMasked != api.AppWebhookSecretMasked {
		t.Fatalf("list leaked write-only fields: %#v", endpoints[0])
	}

	newSecret := "whsec_rotated"
	newPath := "/hooks/stripe-v2"
	patch := e.do(t, http.MethodPatch, "/v1/apps/inbound-manage/inbound-webhooks/"+endpoint.ID, api.UpdateInboundWebhookEndpointRequest{
		SigningSecret: &newSecret,
		DeliveryPath:  &newPath,
	}, nil)
	if patch.Code != http.StatusOK {
		t.Fatalf("patch status %d: %s", patch.Code, patch.Body.String())
	}

	body := []byte(`{"id":"evt_rotated","object":"event"}`)
	oldSignature := postStripeInboundWebhook(t, e, endpoint.EndpointURL, body, inboundWebhookTestSecret)
	if oldSignature.Code != http.StatusBadRequest {
		t.Fatalf("old signing secret status %d: %s", oldSignature.Code, oldSignature.Body.String())
	}
	accepted := postStripeInboundWebhook(t, e, endpoint.EndpointURL, body, newSecret)
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("rotated signing secret status %d: %s", accepted.Code, accepted.Body.String())
	}
	var receipt api.InboundWebhookReceiptResponse
	if err := json.Unmarshal(accepted.Body.Bytes(), &receipt); err != nil {
		t.Fatalf("decode rotated receipt: %v", err)
	}
	invocation, err := e.store.InvocationByID(t.Context(), receipt.ReceiptID)
	if err != nil || invocation.Path != newPath {
		t.Fatalf("rotated delivery path = %q, %v", invocation.Path, err)
	}

	deleted := e.do(t, http.MethodDelete, "/v1/apps/inbound-manage/inbound-webhooks/"+endpoint.ID, nil, nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status %d: %s", deleted.Code, deleted.Body.String())
	}
	missing := postStripeInboundWebhook(t, e, endpoint.EndpointURL, []byte(`{"id":"evt_after_delete"}`), newSecret)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("deleted endpoint ingress status %d: %s", missing.Code, missing.Body.String())
	}
}

func TestInboundWebhookCreateFreePlanGate(t *testing.T) {
	e := setupWebhookTest(t, api.PlanFree)
	mustSeedApp(t, e, "inbound-free")
	rec := e.do(t, http.MethodPost, "/v1/apps/inbound-free/inbound-webhooks", api.CreateInboundWebhookEndpointRequest{
		Name: "stripe", Provider: "stripe", SigningSecret: inboundWebhookTestSecret,
	}, nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("free plan status %d: %s", rec.Code, rec.Body.String())
	}
}
