package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestConsumerControlPlaneLifecycle(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "consumer-app")

	created := e.do(t, http.MethodPost, "/v1/apps/consumer-app/consumers", api.CreateAPIConsumerRequest{
		ExternalRef: "customer-42",
		Name:        "Customer 42",
	}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create consumer: code=%d body=%s", created.Code, created.Body.String())
	}
	var consumer api.APIConsumerResponse
	if err := json.Unmarshal(created.Body.Bytes(), &consumer); err != nil {
		t.Fatalf("decode consumer: %v", err)
	}
	if consumer.ID == "" || consumer.Status != "active" || consumer.ExternalRef != "customer-42" {
		t.Fatalf("unexpected consumer response: %+v", consumer)
	}

	listed := e.do(t, http.MethodGet, "/v1/apps/consumer-app/consumers", nil, nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list consumers: code=%d body=%s", listed.Code, listed.Body.String())
	}
	var consumerList api.APIConsumerListResponse
	if err := json.Unmarshal(listed.Body.Bytes(), &consumerList); err != nil {
		t.Fatalf("decode consumer list: %v", err)
	}
	if len(consumerList.Consumers) != 1 || consumerList.Consumers[0].ID != consumer.ID {
		t.Fatalf("consumer list = %+v", consumerList)
	}

	keyCreated := e.do(t, http.MethodPost, "/v1/apps/consumer-app/consumers/"+consumer.ID+"/keys", api.CreateConsumerKeyRequest{
		Name:   "primary",
		Scopes: []string{"read", "write"},
	}, nil)
	if keyCreated.Code != http.StatusCreated {
		t.Fatalf("create consumer key: code=%d body=%s", keyCreated.Code, keyCreated.Body.String())
	}
	var key api.ConsumerKeyResponse
	if err := json.Unmarshal(keyCreated.Body.Bytes(), &key); err != nil {
		t.Fatalf("decode consumer key: %v", err)
	}
	if key.ID == "" || key.Key == "" || key.ConsumerID != consumer.ID {
		t.Fatalf("unexpected consumer key response: %+v", key)
	}

	keys := e.do(t, http.MethodGet, "/v1/apps/consumer-app/consumers/"+consumer.ID+"/keys", nil, nil)
	if keys.Code != http.StatusOK {
		t.Fatalf("list consumer keys: code=%d body=%s", keys.Code, keys.Body.String())
	}
	var keyList api.ConsumerKeyListResponse
	if err := json.Unmarshal(keys.Body.Bytes(), &keyList); err != nil {
		t.Fatalf("decode consumer key list: %v", err)
	}
	if len(keyList.Keys) != 1 || keyList.Keys[0].ID != key.ID || keyList.Keys[0].Key != "" {
		t.Fatalf("consumer key list = %+v; plaintext must be create-only", keyList)
	}

	revokedKey := e.do(t, http.MethodDelete, "/v1/apps/consumer-app/consumers/"+consumer.ID+"/keys/"+key.ID, nil, nil)
	if revokedKey.Code != http.StatusOK {
		t.Fatalf("revoke consumer key: code=%d body=%s", revokedKey.Code, revokedKey.Body.String())
	}
	var revokedKeyResponse api.ConsumerKeyResponse
	if err := json.Unmarshal(revokedKey.Body.Bytes(), &revokedKeyResponse); err != nil {
		t.Fatalf("decode revoked key: %v", err)
	}
	if revokedKeyResponse.RevokedAt == nil {
		t.Fatalf("revoked key response missing revoked_at: %+v", revokedKeyResponse)
	}

	revokedConsumer := e.do(t, http.MethodDelete, "/v1/apps/consumer-app/consumers/"+consumer.ID, nil, nil)
	if revokedConsumer.Code != http.StatusOK {
		t.Fatalf("revoke consumer: code=%d body=%s", revokedConsumer.Code, revokedConsumer.Body.String())
	}
	var revokedConsumerResponse api.APIConsumerResponse
	if err := json.Unmarshal(revokedConsumer.Body.Bytes(), &revokedConsumerResponse); err != nil {
		t.Fatalf("decode revoked consumer: %v", err)
	}
	if revokedConsumerResponse.Status != "revoked" || revokedConsumerResponse.RevokedAt == nil {
		t.Fatalf("unexpected revoked consumer response: %+v", revokedConsumerResponse)
	}
}

func TestConsumerControlPlaneFreePlanGate(t *testing.T) {
	e := setup(t, api.PlanFree)
	rec := e.do(t, http.MethodGet, "/v1/apps/missing/consumers", nil, nil)
	assertProblem(t, rec, http.StatusPaymentRequired, api.CodeConsumerKeysNotAllowed)
}

func TestAPIConsumerUsageReadSurface(t *testing.T) {
	// ADR-120 monetization target: usage is read from the durable ledger,
	// not from sampled request_telemetry rows.
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "consumer-usage-app")
	created := e.do(t, http.MethodPost, "/v1/apps/consumer-usage-app/consumers", api.CreateAPIConsumerRequest{
		ExternalRef: "customer-usage", Name: "Customer Usage",
	}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create consumer: %d %s", created.Code, created.Body)
	}
	var consumer api.APIConsumerResponse
	if err := json.Unmarshal(created.Body.Bytes(), &consumer); err != nil {
		t.Fatalf("decode consumer: %v", err)
	}
	window := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Minute)
	if _, err := e.store.RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: consumer.AppID,
		ConsumerKey: consumer.ID, WindowStart: window,
		RequestCount: 7, ErrorCount: 2, BillableUnits: 7,
	}); err != nil {
		t.Fatalf("record usage: %v", err)
	}
	read := e.do(t, http.MethodGet, "/v1/apps/consumer-usage-app/consumers/"+consumer.ID+"/usage", nil, nil)
	if read.Code != http.StatusOK {
		t.Fatalf("read usage: %d %s", read.Code, read.Body)
	}
	var usage api.APIConsumerUsageResponse
	if err := json.Unmarshal(read.Body.Bytes(), &usage); err != nil {
		t.Fatalf("decode usage: %v", err)
	}
	if usage.ConsumerID != consumer.ID || usage.RequestCount != 7 || usage.ErrorCount != 2 || usage.BillableUnits != 7 || len(usage.Buckets) != 1 {
		t.Fatalf("usage response = %+v", usage)
	}
}

func TestUpdateAppConsumerAuthMode(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "consumer-mode")
	mode := api.ConsumerAuthModeRequired
	rec := e.do(t, http.MethodPatch, "/v1/apps/consumer-mode", api.UpdateAppRequest{ConsumerAuthMode: &mode}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("update consumer_auth_mode: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var app api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &app); err != nil {
		t.Fatalf("decode app response: %v", err)
	}
	if app.ConsumerAuthMode != api.ConsumerAuthModeRequired {
		t.Fatalf("consumer_auth_mode = %q, want required", app.ConsumerAuthMode)
	}

	invalid := "bogus"
	rec = e.do(t, http.MethodPatch, "/v1/apps/consumer-mode", api.UpdateAppRequest{ConsumerAuthMode: &invalid}, nil)
	assertProblem(t, rec, http.StatusUnprocessableEntity, api.CodeConsumerAuthModeInvalid)
}

func TestUpdateAppConsumerAuthModeFreePlanGate(t *testing.T) {
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "consumer-mode-free")
	mode := api.ConsumerAuthModeRequired
	rec := e.do(t, http.MethodPatch, "/v1/apps/consumer-mode-free", api.UpdateAppRequest{ConsumerAuthMode: &mode}, nil)
	assertProblem(t, rec, http.StatusPaymentRequired, api.CodeConsumerKeysNotAllowed)
}
