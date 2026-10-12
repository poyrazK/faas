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

func TestDurableEntitySocketRetirementVerifiedReclamationAndPermanentFence(t *testing.T) {
	e, app, dispatch, q := entityQuotaFixture(t)
	e.s.Durable().QuotaMigrationEnabled, e.s.Durable().QuotaPolicyEnabled, e.s.Durable().RetirementEnabled = true, true, true
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
	engine, err := durableentity.Open(t.Context(), q.Store, durableentity.Options{RetainedBytesLimit: q.Limits.EntityBytes, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	e.s.Durable().Engine = engine
	for m.Phase != "complete" {
		m, err = q.StepMigration(t.Context(), id, m.ID, m.Revision, engine)
		if err != nil {
			t.Fatal(err)
		}
	}
	path := "/v1/apps/" + app.Slug + "/entities/retirement"
	selectors := "?namespace=counters&key=customer:456"
	readKey, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "retirement-read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + readKey}
	r := e.do(t, "GET", path+selectors, nil, headers)
	var preview struct {
		Entity durableentity.RetirementPreview `json:"entity"`
		Ready  bool                            `json:"ready_to_retire"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &preview) != nil || !preview.Ready || r.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(r.Code, r.Body.String())
	}
	request := durableentity.RetirementRequest{OperationID: uuid.NewString(), Version: preview.Entity.Version, Revision: preview.Entity.RecoveryRevision}
	if r = e.do(t, "POST", path+"/start"+selectors, request, headers); r.Code != 403 {
		t.Fatal("read key retired entity", r.Code)
	}
	r = e.do(t, "POST", path+"/start"+selectors, request, nil)
	var result struct {
		Retirement durableentity.RetirementStatus `json:"retirement"`
		Quota      entitysocket.QuotaRetirement   `json:"quota"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil || result.Quota.Phase != "held" {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = e.do(t, "POST", path+"/start"+selectors, request, nil); r.Code != 200 {
		t.Fatal("start replay failed", r.Code, r.Body.String())
	}
	step := map[string]any{"operation_id": request.OperationID, "expected_revision": result.Retirement.Revision}
	if r = e.do(t, "POST", path+"/step"+selectors, step, nil); r.Code != 409 {
		t.Fatal("drain boundary bypassed", r.Code)
	}
	if r = e.do(t, "POST", "/v1/apps/"+app.Slug+"/entities/invoke", entityRequest("blocked"), nil); r.Code != 410 || dispatch.calls.Load() != 1 {
		t.Fatal("retirement dispatched", r.Code, dispatch.calls.Load())
	}
	now = result.Retirement.NotBefore
	for steps := 0; result.Retirement.Phase != "complete"; steps++ {
		if steps > 100 {
			t.Fatal("retirement did not finish")
		}
		step["expected_revision"] = result.Retirement.Revision
		r = e.do(t, "POST", path+"/step"+selectors, step, nil)
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	if result.Quota.Phase != "complete" || result.Quota.ReleasedBytes <= 0 || result.Quota.RetainedBytes <= 0 {
		t.Fatal(result)
	}
	usage, err := q.Inspect(t.Context(), id)
	if err != nil || usage.EntityReservations != 0 || usage.RecoveryBytes != result.Quota.RetainedBytes {
		t.Fatal(usage, err)
	}
	step["expected_revision"] = result.Retirement.Revision
	if r = e.do(t, "POST", path+"/step"+selectors, step, nil); r.Code != 200 {
		t.Fatal("completion replay failed", r.Code, r.Body.String())
	}
	if r = e.do(t, "POST", "/v1/apps/"+app.Slug+"/entities/invoke", entityRequest("retry-old"), nil); r.Code != 410 || dispatch.calls.Load() != 1 {
		t.Fatal(r.Code, dispatch.calls.Load())
	}
	fresh := entityRequest("fresh")
	fresh.Key = "customer:789"
	if r = e.do(t, "POST", "/v1/apps/"+app.Slug+"/entities/invoke", fresh, nil); r.Code != 200 || dispatch.calls.Load() != 2 {
		t.Fatal(r.Code, r.Body.String(), dispatch.calls.Load())
	}
	e.s.Durable().RetirementEnabled = false
	if r = e.do(t, "GET", path+selectors, nil, nil); r.Code != 503 {
		t.Fatal("disabled endpoint available", r.Code)
	}
}
func TestDurableEntitySocketRetirementRequiresPolicyManagement(t *testing.T) {
	e := entityAPISetup(t)
	if err := e.s.ConfigureDurableEntities(t.Context(), func(name string) string {
		if name == "FAAS_DURABLE_ENTITY_RETIREMENT_ENABLED" {
			return "1"
		}
		return ""
	}); err == nil {
		t.Fatal("retirement configured without policy management")
	}
}
