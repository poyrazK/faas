package apidserver_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	entitysocket "github.com/onebox-faas/faas/pkg/durableentity/socket"
)

func TestDurableEntitySocketRegistryCleanupAuthorizationConcurrentAdmission(t *testing.T) {
	e, app, dispatch, q := entityQuotaFixture(t)
	q.Limits.InvocationsPerMinute = 10
	e.s.Durable().QuotaMigrationEnabled = true
	e.s.Durable().QuotaPolicyEnabled = true
	e.s.Durable().RegistryEnabled = true
	e.s.Durable().RegistryCleanupEnabled = true
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
		m, err = q.StepMigration(t.Context(), id, m.ID, m.Revision, e.s.Durable().Engine)
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
	} else {
		var health struct {
			Discovery struct {
				Scope string `json:"scope"`
				State string `json:"state"`
			} `json:"discovery_health"`
			Progress struct {
				State string `json:"state"`
			} `json:"progress_health"`
		}
		if json.Unmarshal(r.Body.Bytes(), &health) != nil || health.Discovery.Scope != "serving_node" || health.Discovery.State != "disabled" || health.Progress.State != "idle" {
			t.Fatal("health missing from authorized response", r.Body.String())
		}
	}
	request := map[string]any{"operation_id": uuid.NewString(), "expected_revision": status.Revision}
	if r := e.do(t, "POST", path+"/start"+selectors, request, headers); r.Code != 403 {
		t.Fatal("read key started cleanup", r.Code)
	}
	r := e.do(t, "POST", path+"/start"+selectors, request, nil)
	var result struct {
		Cleanup       entitysocket.RegistryCleanup              `json:"cleanup"`
		Worker        *entitysocket.RegistryCleanupWorkerStatus `json:"worker"`
		WorkerEnabled bool                                      `json:"worker_enabled"`
		Seconds       *int64                                    `json:"seconds_since_last_success"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil || result.Cleanup.Epoch != 1 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = e.do(t, "POST", path+"/start"+selectors, request, nil); r.Code != 200 {
		t.Fatal("start replay", r.Code, r.Body.String())
	}
	if r = e.do(t, "POST", "/v1/apps/"+app.Slug+"/entities/invoke", entityRequest("during-cleanup"), nil); r.Code != 200 || dispatch.calls.Load() != 2 {
		t.Fatal("cleanup blocked dispatch", r.Code, r.Body.String())
	}
	step := map[string]any{"operation_id": result.Cleanup.OperationID, "expected_revision": result.Cleanup.Revision}
	if r = e.do(t, "POST", path+"/step"+selectors, step, nil); r.Code != 409 {
		t.Fatal("grace skipped", r.Code, r.Body.String())
	}
	now = result.Cleanup.NotBefore
	dispatchedDuringCollection := false
	e.s.Durable().RegistryCleanupWorkerEnabled = true
	for i := 0; result.Cleanup.Phase != "complete"; i++ {
		if i > 100 {
			t.Fatal("cleanup stuck")
		}
		if result.Cleanup.Phase == "epoch_cleanup" && !dispatchedDuringCollection {
			if r = e.do(t, "POST", "/v1/apps/"+app.Slug+"/entities/invoke", entityRequest("during-collection"), nil); r.Code != 200 || dispatch.calls.Load() != 3 {
				t.Fatal("collection blocked dispatch", r.Code, r.Body.String())
			}
			dispatchedDuringCollection = true
		}
		page, err := q.SweepIndexedRegistryCleanups(t.Context(), "", func(durableentity.ID) bool { return true })
		if err != nil || page.Attempted != 1 || page.Failed != 0 {
			t.Fatal(page, err)
		}
		r = e.do(t, "GET", path+selectors, nil, headers)
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil {
			t.Fatal(r.Code, r.Body.String())
		}
		if !result.WorkerEnabled || result.Worker == nil || result.Worker.ObservedRevision != result.Cleanup.Revision || result.Worker.LastSuccessAt.IsZero() || result.Seconds == nil {
			t.Fatal("worker observation missing", r.Body.String())
		}
		now = now.Add(entitysocket.RegistryCleanupWorkerPoll)
	}
	if result.Cleanup.DeletedObjects < 16 {
		t.Fatal(result)
	}
	if !dispatchedDuringCollection {
		t.Fatal("collection phase not exercised")
	}
	if r = e.do(t, "POST", "/v1/apps/"+app.Slug+"/entities/invoke", entityRequest("after-cleanup"), nil); r.Code != 200 || dispatch.calls.Load() != 4 {
		t.Fatal("admission failed after cleanup", r.Code, r.Body.String())
	}
	e.s.Durable().RegistryCleanupEnabled = false
	if r = e.do(t, "GET", path+selectors, nil, nil); r.Code != 503 {
		t.Fatal("disabled route", r.Code)
	}
}
func TestDurableEntitySocketRegistryCleanupRequiresRegistryManagement(t *testing.T) {
	e, _, _, _ := entityQuotaFixture(t)
	if err := e.s.ConfigureDurableEntities(t.Context(), func(name string) string {
		if name == "FAAS_DURABLE_ENTITY_REGISTRY_CLEANUP_ENABLED" {
			return "1"
		}
		return ""
	}); err == nil {
		t.Fatal("cleanup enabled without registry management")
	}
}

func TestDurableEntitySocketRegistryCleanupWorkerRequiresManagementAndCancels(t *testing.T) {
	e, _, _, _ := entityQuotaFixture(t)
	if err := e.s.ConfigureDurableEntities(t.Context(), func(name string) string {
		if name == "FAAS_DURABLE_ENTITY_REGISTRY_CLEANUP_WORKER_ENABLED" {
			return "1"
		}
		return ""
	}); err == nil {
		t.Fatal("worker enabled without cleanup management")
	}
	e, _, _, _ = entityQuotaFixture(t)
	e.s.Durable().RunDurableEntityRegistryCleanup(t.Context()) // Disabled worker returns.
	e.s.Durable().RegistryCleanupEnabled = true
	e.s.Durable().RegistryCleanupWorkerEnabled = true
	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan struct{})
	go func() { e.s.Durable().RunDurableEntityRegistryCleanup(ctx); close(finished) }()
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("cleanup worker ignored runtime cancellation")
	}
}
