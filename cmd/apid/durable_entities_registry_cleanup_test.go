package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	entitysocket "github.com/onebox-faas/faas/pkg/durableentity/socket"
)

func TestDurableEntitySocketRegistryCleanupAuthorizationHoldAndResume(t *testing.T) {
	e, app, dispatch, q := entityQuotaFixture(t)
	e.s.durableEntityQuotaMigrationEnabled = true
	e.s.durableEntityQuotaPolicyEnabled = true
	e.s.durableEntityRegistryEnabled = true
	e.s.durableEntityRegistryCleanupEnabled = true
	cache, err := entitysocket.NewRegistryCache(q.Store, 8<<20, 256, 16)
	if err != nil {
		t.Fatal(err)
	}
	q.RegistryCache = cache
	if r := e.do(t, "POST", "/v1/apps/"+app.Slug+"/entities/invoke", entityRequest("seed"), nil); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	id := durableentity.ID{AccountID: e.acct.ID, AppID: app.ID, EnvironmentID: app.ID, Namespace: "counters", Key: "customer:456"}
	m, err := q.StartMigration(t.Context(), id, uuid.NewString(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	p := entitysocket.QuotaPolicyRequest{OperationID: uuid.NewString(), ExpectedMigrationID: m.ID, ExpectedMigrationRevision: m.Revision, MigrationID: uuid.NewString(), Limits: q.Limits}
	if _, err = q.ApplyPolicy(t.Context(), id, p, "operator"); err != nil {
		t.Fatal(err)
	}
	m, err = q.MigrationStatus(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	now := m.NotBefore
	q.Now = func() time.Time { return now }
	for m.Phase != "complete" {
		m, err = q.StepMigration(t.Context(), id, m.ID, m.Revision, e.s.durableEntities)
		if err != nil {
			t.Fatal(err)
		}
	}
	status, err := q.RegistryStatus(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.EnableRegistry(t.Context(), id, uuid.NewString(), status.Revision, "operator"); err != nil {
		t.Fatal(err)
	}
	status, err = q.RegistryStatus(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "cleanup-read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + key}
	path := "/v1/apps/" + app.Slug + "/entities/registry/cleanup"
	selectors := "?namespace=counters&key=customer:456"
	if r := e.do(t, "GET", path+selectors, nil, headers); r.Code != 200 || r.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(r.Code, r.Body.String())
	}
	request := map[string]any{"operation_id": uuid.NewString(), "expected_revision": status.Revision}
	if r := e.do(t, "POST", path+"/start"+selectors, request, headers); r.Code != 403 {
		t.Fatal("read key started cleanup", r.Code)
	}
	r := e.do(t, "POST", path+"/start"+selectors, request, nil)
	var result struct {
		Cleanup entitysocket.RegistryCleanup `json:"cleanup"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil || result.Cleanup.Epoch != 1 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = e.do(t, "POST", path+"/start"+selectors, request, nil); r.Code != 200 {
		t.Fatal("start replay", r.Code, r.Body.String())
	}
	if r = e.do(t, "POST", "/v1/apps/"+app.Slug+"/entities/invoke", entityRequest("held"), nil); r.Code != 503 || dispatch.calls.Load() != 1 {
		t.Fatal("held work dispatched", r.Code, r.Body.String())
	}
	step := map[string]any{"operation_id": result.Cleanup.OperationID, "expected_revision": result.Cleanup.Revision}
	if r = e.do(t, "POST", path+"/step"+selectors, step, nil); r.Code != 409 {
		t.Fatal("grace skipped", r.Code, r.Body.String())
	}
	now = result.Cleanup.NotBefore
	for i := 0; result.Cleanup.Phase != "complete"; i++ {
		if i > 100 {
			t.Fatal("cleanup stuck")
		}
		step["expected_revision"] = result.Cleanup.Revision
		r = e.do(t, "POST", path+"/step"+selectors, step, nil)
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	if result.Cleanup.DeletedObjects < 16 {
		t.Fatal(result)
	}
	if r = e.do(t, "POST", "/v1/apps/"+app.Slug+"/entities/invoke", entityRequest("after-cleanup"), nil); r.Code != 200 || dispatch.calls.Load() != 2 {
		t.Fatal("admission did not resume", r.Code, r.Body.String())
	}
	e.s.durableEntityRegistryCleanupEnabled = false
	if r = e.do(t, "GET", path+selectors, nil, nil); r.Code != 503 {
		t.Fatal("disabled route", r.Code)
	}
}
func TestDurableEntitySocketRegistryCleanupRequiresRegistryManagement(t *testing.T) {
	e, _, _, _ := entityQuotaFixture(t)
	if err := e.s.configureDurableEntities(t.Context(), func(name string) string {
		if name == "FAAS_DURABLE_ENTITY_REGISTRY_CLEANUP_ENABLED" {
			return "1"
		}
		return ""
	}); err == nil {
		t.Fatal("cleanup enabled without registry management")
	}
}
