package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestConsumerControlPlaneLifecycle(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	apidserver.MustSeedApp(t, e, "consumer-app")

	created := e.Do(t, http.MethodPost, "/v1/apps/consumer-app/consumers", api.CreateAPIConsumerRequest{
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

	listed := e.Do(t, http.MethodGet, "/v1/apps/consumer-app/consumers", nil, nil)
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

	keyCreated := e.Do(t, http.MethodPost, "/v1/apps/consumer-app/consumers/"+consumer.ID+"/keys", api.CreateConsumerKeyRequest{
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

	keys := e.Do(t, http.MethodGet, "/v1/apps/consumer-app/consumers/"+consumer.ID+"/keys", nil, nil)
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

	revokedKey := e.Do(t, http.MethodDelete, "/v1/apps/consumer-app/consumers/"+consumer.ID+"/keys/"+key.ID, nil, nil)
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

	revokedConsumer := e.Do(t, http.MethodDelete, "/v1/apps/consumer-app/consumers/"+consumer.ID, nil, nil)
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
	e := apidserver.Setup(t, api.PlanFree)
	// App access resolves the addressed app before any handler runs (#4882),
	// so the gate is observed on an app the caller owns.
	apidserver.MustSeedApp(t, e, "consumer-gate")
	rec := e.Do(t, http.MethodGet, "/v1/apps/consumer-gate/consumers", nil, nil)
	apidserver.AssertProblem(t, rec, http.StatusPaymentRequired, api.CodeConsumerKeysNotAllowed)
}

func TestAPIConsumerUsageReadSurface(t *testing.T) {
	// ADR-120 monetization target: usage is read from the durable ledger,
	// not from sampled request_telemetry rows.
	e := apidserver.Setup(t, api.PlanHobby)
	apidserver.MustSeedApp(t, e, "consumer-usage-app")
	created := e.Do(t, http.MethodPost, "/v1/apps/consumer-usage-app/consumers", api.CreateAPIConsumerRequest{
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
	if _, err := (*e.Store()).RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: (*e.Acct()).ID, AppID: consumer.AppID,
		ConsumerKey: consumer.ID, WindowStart: window,
		RequestCount: 7, ErrorCount: 2, BillableUnits: 7,
	}); err != nil {
		t.Fatalf("record usage: %v", err)
	}
	read := e.Do(t, http.MethodGet, "/v1/apps/consumer-usage-app/consumers/"+consumer.ID+"/usage", nil, nil)
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

// Prod hunt #9: `gregale consumers usage --since <today>` returned 500
// "could not load API consumer usage". The window snaps to UTC midnight and
// is half-open, so since=today is empty; the ledger answers ErrNotFound.
func TestAPIConsumerUsageEmptyWindowIsEmptyNot500(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	apidserver.MustSeedApp(t, e, "consumer-usage-empty")
	created := e.Do(t, http.MethodPost, "/v1/apps/consumer-usage-empty/consumers", api.CreateAPIConsumerRequest{
		ExternalRef: "customer-empty", Name: "Customer Empty",
	}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create consumer: %d %s", created.Code, created.Body)
	}
	var consumer api.APIConsumerResponse
	if err := json.Unmarshal(created.Body.Bytes(), &consumer); err != nil {
		t.Fatalf("decode consumer: %v", err)
	}
	today := time.Now().UTC().Format("2006-01-02") + "T11:00:00Z"
	read := e.Do(t, http.MethodGet, "/v1/apps/consumer-usage-empty/consumers/"+consumer.ID+"/usage?since="+today, nil, nil)
	if read.Code != http.StatusOK {
		t.Fatalf("empty window: %d %s, want 200", read.Code, read.Body)
	}
	var usage api.APIConsumerUsageResponse
	if err := json.Unmarshal(read.Body.Bytes(), &usage); err != nil {
		t.Fatalf("decode usage: %v", err)
	}
	if usage.RequestCount != 0 || len(usage.Buckets) != 0 || !usage.PeriodStart.Equal(usage.PeriodEnd) {
		t.Fatalf("empty window usage = %+v", usage)
	}
}

func TestUpdateAppConsumerAuthMode(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	apidserver.MustSeedApp(t, e, "consumer-mode")
	mode := api.ConsumerAuthModeRequired
	rec := e.Do(t, http.MethodPatch, "/v1/apps/consumer-mode", api.UpdateAppRequest{ConsumerAuthMode: &mode}, nil)
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
	rec = e.Do(t, http.MethodPatch, "/v1/apps/consumer-mode", api.UpdateAppRequest{ConsumerAuthMode: &invalid}, nil)
	apidserver.AssertProblem(t, rec, http.StatusUnprocessableEntity, api.CodeConsumerAuthModeInvalid)
}

func TestUpdateAppConsumerAuthModeFreePlanGate(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	apidserver.MustSeedApp(t, e, "consumer-mode-free")
	mode := api.ConsumerAuthModeRequired
	rec := e.Do(t, http.MethodPatch, "/v1/apps/consumer-mode-free", api.UpdateAppRequest{ConsumerAuthMode: &mode}, nil)
	apidserver.AssertProblem(t, rec, http.StatusPaymentRequired, api.CodeConsumerKeysNotAllowed)
}

func TestUpdateAppPlatformTenantRequired(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	apidserver.MustSeedApp(t, e, "platform-app")
	for _, required := range []bool{true, false} {
		rec := e.Do(t, http.MethodPatch, "/v1/apps/platform-app", api.UpdateAppRequest{PlatformTenantRequired: &required}, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("patch platform_tenant_required=%t: code=%d body=%s", required, rec.Code, rec.Body.String())
		}
		var app api.AppResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &app); err != nil {
			t.Fatal(err)
		}
		if app.PlatformTenantRequired != required {
			t.Fatalf("response platform_tenant_required=%t, want %t", app.PlatformTenantRequired, required)
		}
		read := e.Do(t, http.MethodGet, "/v1/apps/platform-app", nil, nil)
		if err := json.Unmarshal(read.Body.Bytes(), &app); err != nil {
			t.Fatal(err)
		}
		if app.PlatformTenantRequired != required {
			t.Fatalf("read platform_tenant_required=%t, want %t", app.PlatformTenantRequired, required)
		}
	}
}

func TestUpdateAppPlatformTenantRequiredFreePlanGate(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	apidserver.MustSeedApp(t, e, "platform-app-free")
	required := true
	rec := e.Do(t, http.MethodPatch, "/v1/apps/platform-app-free", api.UpdateAppRequest{PlatformTenantRequired: &required}, nil)
	apidserver.AssertProblem(t, rec, http.StatusPaymentRequired, api.CodePlanPlatformTenantRequiredNotAllowed)
}

func TestCreateAppPlatformTenantRequired(t *testing.T) {
	required := true
	e := apidserver.Setup(t, api.PlanHobby)
	rec := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{
		Slug: "tenant-from-start", PlatformTenantRequired: &required,
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create app: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var app api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &app); err != nil {
		t.Fatal(err)
	}
	if !app.PlatformTenantRequired {
		t.Fatalf("create response omitted tenant policy: %+v", app)
	}
	read := e.Do(t, http.MethodGet, "/v1/apps/tenant-from-start", nil, nil)
	if err := json.Unmarshal(read.Body.Bytes(), &app); err != nil {
		t.Fatal(err)
	}
	if !app.PlatformTenantRequired {
		t.Fatal("tenant policy was not persisted")
	}

	free := apidserver.Setup(t, api.PlanFree)
	denied := free.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{
		Slug: "tenant-free", PlatformTenantRequired: &required,
	}, nil)
	apidserver.AssertProblem(t, denied, http.StatusPaymentRequired, api.CodePlanPlatformTenantRequiredNotAllowed)
}
