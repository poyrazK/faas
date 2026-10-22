package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	entitysocket "github.com/onebox-faas/faas/pkg/durableentity/socket"
)

func TestDurableEntitySocketQuotaMigrationBackfillsHistoricalState(t *testing.T) {
	e, app, dispatch, q := entityQuotaFixture(t)
	e.s.Durable().QuotaMigrationEnabled = true
	now := time.Now()
	q.Now = func() time.Time { return now }
	id := durableentity.ID{AccountID: e.acct.ID, AppID: app.ID, EnvironmentID: app.ID, Namespace: "counters", Key: "customer:456"}
	// Simulate committed data created before shared tenant admission existed.
	_, err := e.s.Durable().Engine.Invoke(t.Context(), id, "legacy-owner", durableentity.Request{ID: "historical", Payload: json.RawMessage(`{}`)}, func(context.Context, durableentity.View) (durableentity.Transition, error) {
		return durableentity.Transition{Data: json.RawMessage(`{"count":1}`), Result: json.RawMessage(`{}`)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/apps/" + app.Slug + "/entities/quota-migration"
	selectors := "?namespace=counters&key=customer:456"
	response := e.do(t, "GET", base+"/plan"+selectors, nil, nil)
	var plan entitysocket.QuotaMigrationPlan
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &plan) != nil || !plan.PageFits || plan.MissingReservations != 1 || plan.AdditionalBytes != 1<<20 {
		t.Fatal(response.Code, response.Body.String())
	}
	usage, err := q.Inspect(t.Context(), id)
	if err != nil || usage.EntityReservations != 0 || usage.Invocations != 0 {
		t.Fatal("plan changed admission", usage, err)
	}
	response = e.do(t, "POST", base+"/start"+selectors, map[string]any{"migration_id": uuid.NewString()}, nil)
	var progress entitysocket.QuotaMigration
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &progress) != nil {
		t.Fatal(response.Code, response.Body.String())
	}
	invoke := e.do(t, "POST", "/v1/apps/"+app.Slug+"/entities/invoke", entityRequest("held"), nil)
	if invoke.Code != 503 || dispatch.calls.Load() != 0 {
		t.Fatal("migration failed to hold dispatch", invoke.Code, dispatch.calls.Load())
	}
	request := map[string]any{"migration_id": progress.ID, "expected_revision": progress.Revision}
	response = e.do(t, "POST", base+"/step"+selectors, request, nil)
	if response.Code != 503 {
		t.Fatal("drain wait bypassed", response.Code)
	}
	now = progress.NotBefore
	response = e.do(t, "POST", base+"/step"+selectors, request, nil)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &progress) != nil || progress.Phase != "verify" {
		t.Fatal(response.Code, response.Body.String())
	}
	stale := e.do(t, "POST", base+"/step"+selectors, request, nil)
	if stale.Code != 409 {
		t.Fatal("stale worker accepted", stale.Code, stale.Body.String())
	}
	request["expected_revision"] = progress.Revision
	response = e.do(t, "POST", base+"/step"+selectors, request, nil)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &progress) != nil || progress.Phase != "complete" || progress.Observed != 1 {
		t.Fatal(response.Code, response.Body.String())
	}
	usage, err = q.Inspect(t.Context(), id)
	if err != nil || usage.EntityReservations != 1 || usage.ReservedBytes != 1<<20 || usage.Invocations != 0 {
		t.Fatal(usage, err)
	}
	status := e.do(t, "GET", base+selectors, nil, nil)
	if status.Code != 200 || status.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(status.Code, status.Body.String())
	}
	if response = e.do(t, "POST", "/v1/apps/"+app.Slug+"/entities/invoke", entityRequest("resumed"), nil); response.Code != 200 || dispatch.calls.Load() != 1 {
		t.Fatal(response.Code, response.Body.String())
	}
	anonymous := httptest.NewRecorder()
	e.h.ServeHTTP(anonymous, httptest.NewRequest("GET", base+selectors, nil))
	if anonymous.Code == 200 {
		t.Fatal("anonymous migration inspection")
	}
	readKey, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "migration-read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	readHeaders := map[string]string{"Authorization": "Bearer " + readKey}
	if response = e.do(t, "GET", base+selectors, nil, readHeaders); response.Code != 200 {
		t.Fatal("read scope cannot inspect", response.Code, response.Body.String())
	}
	if response = e.do(t, "POST", base+"/start"+selectors, map[string]any{"migration_id": uuid.NewString()}, readHeaders); response.Code != 403 {
		t.Fatal("read scope can start migration", response.Code, response.Body.String())
	}
	e.s.Durable().QuotaMigrationEnabled = false
	if response = e.do(t, "GET", base+selectors, nil, nil); response.Code != 503 {
		t.Fatal("disabled migration available", response.Code)
	}
}

func TestDurableEntitySocketQuotaMigrationRequiresQuotas(t *testing.T) {
	e := entityAPISetup(t)
	if err := e.s.ConfigureDurableEntities(t.Context(), func(name string) string {
		if name == "FAAS_DURABLE_ENTITY_QUOTA_MIGRATION_ENABLED" {
			return "1"
		}
		return ""
	}); err == nil {
		t.Fatal("migration configured without tenant quotas")
	}
}
