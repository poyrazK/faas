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

func TestDurableEntitySocketRegistryAuthorizationMigrationAndListing(t *testing.T) {
	e, app, _, q := entityQuotaFixture(t)
	e.s.Durable().QuotaMigrationEnabled = true
	e.s.Durable().QuotaPolicyEnabled = true
	e.s.Durable().RegistryEnabled = true
	cache, cacheErr := entitysocket.NewRegistryCache(q.Store, 8<<20, 256, 16)
	if cacheErr != nil {
		t.Fatal(cacheErr)
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
		m, err = q.StepMigration(t.Context(), id, m.ID, m.Revision, e.s.Durable().Engine)
		if err != nil {
			t.Fatal(err)
		}
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "registry-read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + key}
	path := "/v1/apps/" + app.Slug + "/entities/registry"
	selectors := "?namespace=counters&key=customer:456"
	r := e.do(t, "GET", path+selectors, nil, headers)
	var page entitysocket.RegistryPage
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &page) != nil || page.Enabled || len(page.Entries) != 1 || r.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(r.Code, r.Body.String())
	}
	request := map[string]any{"operation_id": uuid.NewString(), "expected_revision": page.Revision}
	if r = e.do(t, "POST", path+"/enable"+selectors, request, headers); r.Code != 403 {
		t.Fatal("read-only migration", r.Code)
	}
	if r = e.do(t, "POST", path+"/enable"+selectors, request, nil); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = e.do(t, "POST", path+"/enable"+selectors, request, nil); r.Code != 200 {
		t.Fatal("replay", r.Code, r.Body.String())
	}
	if r = e.do(t, "GET", path+selectors, nil, headers); r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &page) != nil || !page.Enabled || page.ActiveCapacity != 1024 {
		t.Fatal(r.Code, r.Body.String())
	}
	changed := map[string]any{"operation_id": uuid.NewString(), "expected_revision": page.Revision}
	if r = e.do(t, "POST", path+"/enable"+selectors, changed, nil); r.Code != 409 {
		t.Fatal("changed migration identity", r.Code, r.Body.String())
	}
	if r = e.do(t, "GET", path+selectors+"&cursor=invalid", nil, headers); r.Code != 409 {
		t.Fatal("invalid cursor", r.Code, r.Body.String())
	}
	e.s.Durable().RegistryEnabled = false
	if r = e.do(t, "GET", path+selectors, nil, nil); r.Code != 503 {
		t.Fatal("disabled", r.Code)
	}
}
func TestDurableEntitySocketRegistryRequiresManagedPolicy(t *testing.T) {
	e, _, _, _ := entityQuotaFixture(t)
	if err := e.s.ConfigureDurableEntities(t.Context(), func(name string) string {
		if name == "FAAS_DURABLE_ENTITY_REGISTRY_ENABLED" {
			return "1"
		}
		return ""
	}); err == nil {
		t.Fatal("registry enabled without managed policy")
	}
}
