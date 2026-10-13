package apidserver_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	entitysocket "github.com/onebox-faas/faas/pkg/durableentity/socket"
)

func TestDurableEntitySocketQuotaPolicyIncreaseCoordinatesMigration(t *testing.T) {
	e, app, dispatch, q := entityQuotaFixture(t)
	e.s.Durable().QuotaMigrationEnabled = true
	e.s.Durable().QuotaPolicyEnabled = true
	if r := e.do(t, "POST", "/v1/apps/"+app.Slug+"/entities/invoke", entityRequest("seed"), nil); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	id := durableentity.ID{AccountID: e.acct.ID, AppID: app.ID, EnvironmentID: app.ID, Namespace: "counters", Key: "customer:456"}
	m, err := q.StartMigration(t.Context(), id, uuid.NewString(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	older := *q
	limits := q.Limits
	limits.EntityBytes *= 2
	limits.RetainedBytes *= 2
	p := entitysocket.QuotaPolicyRequest{OperationID: uuid.NewString(), ExpectedMigrationID: m.ID, ExpectedMigrationRevision: m.Revision, MigrationID: uuid.NewString(), Limits: limits}
	q.Limits = limits
	engine, err := durableentity.Open(t.Context(), q.Store, durableentity.Options{RetainedBytesLimit: limits.EntityBytes})
	if err != nil {
		t.Fatal(err)
	}
	e.s.Durable().Engine = engine
	path := "/v1/apps/" + app.Slug + "/entities/quota-policy"
	selectors := "?namespace=counters&key=customer:456"
	readKey, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "policy-read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + readKey}
	r := e.do(t, "POST", path+"/preview"+selectors, p, headers)
	var preview entitysocket.QuotaPolicyPreview
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &preview) != nil || !preview.ReadyToApply || preview.AdditionalBytes != 1<<20 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = e.do(t, "POST", path+"/apply"+selectors, p, headers); r.Code != 403 {
		t.Fatal("read scope applied policy", r.Code)
	}
	r = e.do(t, "POST", path+"/apply"+selectors, p, nil)
	var change entitysocket.QuotaPolicyChange
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &change) != nil || change.Revision != 1 || change.Actor == "" {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = e.do(t, "POST", path+"/apply"+selectors, p, nil); r.Code != 200 {
		t.Fatal("policy replay failed", r.Code, r.Body.String())
	}
	m, err = q.MigrationStatus(t.Context(), id)
	if err != nil || m.ID != p.MigrationID || m.Phase != "backfill" {
		t.Fatal(m, err)
	}
	now := m.NotBefore
	q.Now = func() time.Time { return now }
	for m.Phase != "complete" {
		m, err = q.StepMigration(t.Context(), id, m.ID, m.Revision, engine)
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err := engine.ScanQuotaInventory(t.Context(), "", id)
	if err != nil || len(page.Entities) != 1 || page.Entities[0].StorageLimitBytes != limits.EntityBytes {
		t.Fatal(page, err)
	}
	if r = e.do(t, "GET", path+selectors, nil, headers); r.Code != 200 || r.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(r.Code, r.Body.String())
	}
	if err = older.AdmitInvocation(t.Context(), id); err == nil {
		t.Fatal("old node admitted with stale cap")
	}
	if r = e.do(t, "POST", "/v1/apps/"+app.Slug+"/entities/invoke", entityRequest("after-policy"), nil); r.Code != 200 || dispatch.calls.Load() != 2 {
		t.Fatal(r.Code, r.Body.String(), dispatch.calls.Load())
	}
	stale := p
	stale.OperationID = uuid.NewString()
	stale.MigrationID = uuid.NewString()
	stale.Limits.RetainedBytes++
	if r = e.do(t, "POST", path+"/apply"+selectors, stale, nil); r.Code != 409 {
		t.Fatal("stale policy changed", r.Code, r.Body.String())
	}
	e.s.Durable().QuotaPolicyEnabled = false
	if r = e.do(t, "GET", path+selectors, nil, nil); r.Code != 503 {
		t.Fatal("disabled policy available", r.Code)
	}
}
func TestDurableEntitySocketQuotaPolicyRequiresMigration(t *testing.T) {
	e := entityAPISetup(t)
	if err := e.s.ConfigureDurableEntities(t.Context(), func(name string) string {
		if name == "FAAS_DURABLE_ENTITY_QUOTA_POLICY_ENABLED" {
			return "1"
		}
		return ""
	}); err == nil {
		t.Fatal("policy configured without migration")
	}
}
