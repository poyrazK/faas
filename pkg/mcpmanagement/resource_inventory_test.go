// adr: 20261103155008
package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestResourcePaginationExcludesUnconsentedScopes(t *testing.T) {
	f := newLocalFixture(t, true, "release-base")
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/alpha/resource-inventory" {
			f.serveHTTP(w, r)
			return
		}
		if r.URL.Query().Get("limit") != "1" || r.URL.Query().Get("offset") != "0" || r.URL.Query().Get("environment") != "" {
			t.Error("pagination was not delegated to the backend")
		}
		pending := false
		item := api.AppResourceInventoryItem{ID: "binding-one", AppID: f.app.ID, Scope: "default", BucketID: "bucket-own", Bucket: "assets", RotationPending: &pending}
		kind := r.URL.Query().Get("kind")
		if kind == "databases" {
			item = api.AppResourceInventoryItem{ID: "db-own", AppID: f.app.ID, Scope: "default", Name: "app-db", Region: "test", State: "ready", PostgresMajor: 17, Binding: &api.AppResourceInventoryBinding{ID: "binding-one", DatabaseID: "db-own", AppID: f.app.ID, Scope: "default", Access: "read_only", State: "ready", EnvironmentKey: "DATABASE_URL"}}
		}
		_ = json.NewEncoder(w).Encode(api.AppResourceInventoryResponse{SchemaVersion: 1, AppID: f.app.ID, Scope: "default", Kind: kind, Limit: 1, Total: 1, Items: []api.AppResourceInventoryItem{item}, GeneratedAt: time.Now().UTC()})
	}))
	defer b.Close()
	_, m, err := newManagementServer(api.NewClient(b.URL, testToken), Options{APIBase: b.URL})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"storage", "databases"} {
		t.Run(kind, func(t *testing.T) {
			a := resourcePageArgs{App: "alpha", Limit: 1}
			var out any
			var err error
			if kind == "storage" {
				out, err = m.getStorageBindings(t.Context(), a)
			} else {
				out, err = m.listDatabases(t.Context(), a)
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "next_offset") || !strings.Contains(string(raw), "binding-one") {
				t.Fatalf("inventory crossed consent: %s", raw)
			}
		})
	}
}

func TestResourceChangesRecheckReviewedConfiguration(t *testing.T) {
	f := newLocalFixture(t, true, "release-base")
	var mu sync.Mutex
	cron := api.CronResponse{ID: "cron-one", AppID: f.app.ID, Schedule: "*/15 * * * *", Path: "/tick", Enabled: true, Timezone: "UTC"}
	domain := api.CustomDomainResponse{Domain: "api.example.com", AppID: f.app.ID}
	writes := 0
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/crons/cron-one" && r.URL.Path != "/v1/domains/api.example.com" {
			f.serveHTTP(w, r)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.Method != http.MethodGet {
			writes++
		}
		if strings.HasPrefix(r.URL.Path, "/v1/crons/") {
			_ = json.NewEncoder(w).Encode(cron)
		} else {
			_ = json.NewEncoder(w).Encode(domain)
		}
	}))
	defer b.Close()
	_, m, err := newManagementServer(api.NewClient(b.URL, testToken), Options{APIBase: b.URL, AllowWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	cronPlan, err := m.planCronChange(t.Context(), cronChangeArgs{App: "alpha", CronID: cron.ID, Delete: true, Reason: "retire cron"})
	if err != nil {
		t.Fatal(err)
	}
	domainPlan, err := m.planDomainRemoval(t.Context(), domainRemovalArgs{App: "alpha", Domain: domain.Domain, Reason: "retire domain"})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	cron.Schedule, domain.AppID = "0 0 * * *", "another-app"
	mu.Unlock()
	for _, p := range []any{cronPlan, domainPlan} {
		if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: p.(releasePlan).ID}); err == nil {
			t.Fatal("resource changed since review but mutation was accepted")
		}
	}
	mu.Lock()
	if writes != 0 {
		t.Errorf("stale plans caused %d writes", writes)
	}
	mu.Unlock()
	// A scheduled firing alone does not invalidate the reviewed configuration.
	p, err := m.planCronChange(t.Context(), cronChangeArgs{App: "alpha", CronID: cron.ID, Delete: true, Reason: "retire updated cron"})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	cron.LastFiredAt = "2026-11-03T16:00:00Z"
	mu.Unlock()
	for range 2 {
		if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: p.(releasePlan).ID}); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if writes != 1 {
		t.Fatalf("completed resource receipt repeated a deletion: writes=%d", writes)
	}
}

func TestMissingCronDoesNotProveThisPlanDeletedIt(t *testing.T) {
	f := newLocalFixture(t, true, "release-base")
	cron := api.CronResponse{ID: "cron-one", AppID: f.app.ID, Schedule: "*/15 * * * *", Path: "/tick", Enabled: true, Timezone: "UTC"}
	var missing atomic.Bool
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/crons/cron-one" {
			f.serveHTTP(w, r)
			return
		}
		if missing.Load() {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(cron)
	}))
	defer b.Close()
	_, m, err := newManagementServer(api.NewClient(b.URL, testToken), Options{APIBase: b.URL, AllowWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	value, err := m.planCronChange(t.Context(), cronChangeArgs{App: "alpha", CronID: cron.ID, Delete: true, Reason: "retire cron"})
	if err != nil {
		t.Fatal(err)
	}
	p := m.plans[value.(releasePlan).ID]
	missing.Store(true)
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: p.ID}); err == nil {
		t.Fatal("missing cron was attributed to this plan")
	}
	if p.localReceipt != nil || p.failure == nil || p.failure.Status != "outcome_unknown" {
		t.Fatalf("missing cron produced a completion receipt: %+v", p.failure)
	}
	for _, kind := range []string{"cron_update", "cron_delete"} {
		if safePlanRetry(&releasePlan{Operation: &operationPlan{Kind: kind}}, "gregale_apply_operation") {
			t.Fatalf("%s recommended an unguarded retry", kind)
		}
	}
}

func TestAmbiguousCronWriteCannotBeReplayed(t *testing.T) {
	for _, deletion := range []bool{false, true} {
		t.Run(map[bool]string{false: "update", true: "delete"}[deletion], func(t *testing.T) {
			f := newLocalFixture(t, true, "release-base")
			cron := api.CronResponse{ID: "cron-one", AppID: f.app.ID, Schedule: "*/15 * * * *", Path: "/tick", Enabled: true, Timezone: "UTC"}
			var writes atomic.Int32
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/crons/cron-one" {
					f.serveHTTP(w, r)
					return
				}
				if r.Method == http.MethodGet {
					_ = json.NewEncoder(w).Encode(cron)
					return
				}
				writes.Add(1)
				api.WriteProblem(w, api.NewProblem(502, "transport_error", "response lost", "inspect the original action"))
			}))
			defer b.Close()
			store, statePath, key := testStateStore(t)
			opts := Options{APIBase: b.URL, AllowWrites: true, planStore: store, planNamespace: "cron-recovery"}
			_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			path := "/new"
			args := cronChangeArgs{App: "alpha", CronID: cron.ID, Delete: deletion, Reason: "reviewed change"}
			if !deletion {
				args.Path = &path
			}
			value, err := m.planCronChange(t.Context(), args)
			if err != nil {
				t.Fatal(err)
			}
			id := value.(releasePlan).ID
			for range 2 {
				if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: id}); err == nil {
					t.Fatal("ambiguous attempt reported success")
				}
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenStateStore(statePath, key)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			opts.planStore = reopened
			_, restored, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := restored.applyOperation(t.Context(), applyArgs{PlanID: id}); err == nil {
				t.Fatal("restart allowed an ambiguous operation to repeat")
			}
			if writes.Load() != 1 {
				t.Fatalf("ambiguous operation repeated %d times", writes.Load())
			}
		})
	}
}

func TestWorkflowProfilesIncludeApplyAndReceiptTools(t *testing.T) {
	for _, set := range []string{ToolsetCore, ToolsetRelease, ToolsetProvision, ToolsetOperate} {
		t.Run(set, func(t *testing.T) {
			names := map[string]bool{}
			for _, tool := range toolsetCatalog(t, []string{set}) {
				names[tool.Name] = true
			}
			if !names["gregale_get_plan"] || !names["gregale_list_plans"] {
				t.Fatal("workflow profile cannot recover its plan receipts")
			}
			if set == ToolsetProvision && (!names["gregale_plan_cron_change"] || !names["gregale_apply_operation"]) {
				t.Fatal("provision profile cannot apply its resource-change plans")
			}
		})
	}
}

func TestEnvironmentPlanReceiptsExcludeApprovalCredentials(t *testing.T) {
	_, b := newEnvironmentFixture(t)
	cs, ctx := connectConfiguredTestServer(t, b, environmentTestOptions(b, true), "2026-07-28")
	p := callTool(t, ctx, cs, "gregale_plan_environment_promotion", environmentPlanArgs(), false)
	id := environmentPlanID(t, p)
	callTool(t, ctx, cs, "gregale_apply_environment_promotion", map[string]any{"plan_id": id}, false)
	out := callTool(t, ctx, cs, "gregale_get_plan", map[string]any{"plan_id": id}, false)
	raw, _ := json.Marshal(out)
	if string(out["status"]) != `"accepted"` || strings.Contains(string(raw), "exact-preview-token") || strings.Contains(string(raw), "approval") {
		t.Fatalf("environment receipt missing or exposed approval inputs: %s", raw)
	}
	list := callTool(t, ctx, cs, "gregale_list_plans", map[string]any{}, false)
	if !strings.Contains(string(list["items"]), id) {
		t.Fatal("environment plan omitted from inventory")
	}
}
