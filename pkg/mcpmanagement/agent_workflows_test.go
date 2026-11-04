package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestTemporaryPlansAndCompletedReceiptsRecover(t *testing.T) {
	for _, kind := range []string{"upload_new", "upload_serving", "provision"} {
		t.Run(kind, func(t *testing.T) {
			store, path, key := testStateStore(t)
			current := ""
			if kind == "upload_serving" {
				current = "release-base"
			}
			f := newLocalFixture(t, kind != "upload_new", current)
			backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
			defer backend.Close()
			uploads, err := newUploadStore(filepath.Join(t.TempDir(), "uploads"), "https://api.gregale.test/mcp")
			if err != nil {
				t.Fatal(err)
			}
			cli := newProvisionCLI()
			opts := Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, LocalCLI: f.cli, uploads: uploads, Environments: &EnvironmentConsent{DefaultWrite: true, Named: map[string]EnvironmentGrant{}}, planStore: store, planNamespace: "recovery", grantID: "recovery"}
			if kind == "provision" {
				opts.LocalCLI = cli.cli
			}
			_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			var result any
			if kind == "provision" {
				// Model a legacy temporary plan issued before native durable
				// provisioning. Its completed receipt must remain recoverable.
				var p *releasePlan
				p, err = m.provisionBase(t.Context(), "alpha", "legacy recovery", "cron")
				if err == nil {
					p.Provision.cron = cronSpec{Schedule: "*/15 * * * *", Path: "/tick"}
					p.Provision.existing = map[string]bool{}
					p.Provision.Command = []string{"crons", "add", "--app", "alpha", "--schedule", "*/15 * * * *", "--path", "/tick"}
					result, err = m.savePlan(p)
				}
			} else {
				result, err = m.planUploadDeploy(t.Context(), planUploadDeployArgs{App: "alpha", Reason: "test recovery", Files: []inlineFile{{Path: "server.js", Content: "require('http')"}}})
			}
			if err != nil {
				t.Fatal(err)
			}
			id := result.(releasePlan).ID
			if m.idle(time.Now()) {
				t.Fatal("idle cleanup would lose a pending temporary plan")
			}
			var records map[string]storedPlan
			if err := store.read("plans/recovery", &records); err != nil || len(records) != 0 {
				t.Fatalf("temporary inputs journaled: %v %v", records, err)
			}
			if _, _, err := newManagementServer(api.NewClient(backend.URL, testToken), opts); err != nil {
				t.Fatalf("pending plan poisoned recovery: %v", err)
			}
			if kind == "provision" {
				_, err = m.applyProvision(t.Context(), applyArgs{PlanID: id})
			} else {
				_, err = m.applyLocalDeploy(t.Context(), applyArgs{PlanID: id})
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			next, err := OpenStateStore(path, key)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			opts.planStore = next
			_, restored, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
			if err != nil {
				t.Fatalf("completed receipt did not restore: %v", err)
			}
			if kind == "provision" {
				_, err = restored.applyProvision(t.Context(), applyArgs{PlanID: id})
				if cli.count("crons add") != 1 {
					t.Fatal("recovery duplicated cron")
				}
			} else {
				_, err = restored.applyLocalDeploy(t.Context(), applyArgs{PlanID: id})
				if len(f.deployCalls()) != 1 {
					t.Fatal("recovery duplicated deploy")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind == "upload_serving" {
				if _, err := restored.promoteDeployment(t.Context(), applyArgs{PlanID: id}); err != nil {
					t.Fatalf("restored staged upload cannot promote: %v", err)
				}
			}
		})
	}
}

func TestOldTemporaryJournalDoesNotPoisonConnection(t *testing.T) {
	store, _, _ := testStateStore(t)
	old := map[string]storedPlan{"old": {Plan: releasePlan{Action: "provision_cron", ExpiresAt: time.Now().Add(time.Hour), Provision: &provisionPlan{Kind: "cron"}}}}
	if err := store.update("plans/old", old); err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(http.NotFoundHandler())
	defer backend.Close()
	if _, _, err := newManagementServer(api.NewClient(backend.URL, testToken), Options{APIBase: backend.URL, planStore: store, planNamespace: "old"}); err != nil {
		t.Fatal(err)
	}
}

func TestUnjournaledEnvironmentPlanPreventsIdleEviction(t *testing.T) {
	m := &management{environmentPlans: map[string]*environmentPlan{"pending": {ExpiresAt: time.Now().Add(time.Hour)}}}
	if m.idle(time.Now()) {
		t.Fatal("idle cleanup would lose an unjournaled environment plan")
	}
}

func TestHeadlessReceiptsSurviveOAuthCleanup(t *testing.T) {
	store, _, _ := testStateStore(t)
	name := "plans/agent-key/account/hash"
	if err := store.update(name, map[string]storedPlan{"plan": {Plan: releasePlan{ExpiresAt: time.Now().Add(time.Hour)}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{}}); err != nil {
		t.Fatal(err)
	}
	var result map[string]storedPlan
	if err := store.read(name, &result); err != nil || len(result) != 1 {
		t.Fatalf("headless receipt removed by OAuth cleanup: %v %v", result, err)
	}
	if err := store.update(name, map[string]storedPlan{"plan": {Plan: releasePlan{ExpiresAt: time.Now().Add(-time.Hour)}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{}}); err != nil {
		t.Fatal(err)
	}
	result = nil
	if err := store.read(name, &result); err != nil || len(result) != 0 {
		t.Fatalf("expired headless receipt retained: %v %v", result, err)
	}
}

type operationFixture struct {
	base                    *localFixture
	mu                      sync.Mutex
	revision                int64
	values                  map[string]string
	puts, restarts, patches int
	keys                    []string
	batchReceipts           map[string]api.EnvBatchResponse
	batchBodies             []string
	failBatchAfterCommit    bool
	denyRestart             bool
}

func (f *operationFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/v1/apps/alpha/env/batch" && r.Method == http.MethodPost:
		var req api.EnvBatchRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			w.WriteHeader(400)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		raw, _ := json.Marshal(req)
		f.batchBodies = append(f.batchBodies, string(raw))
		if previous, ok := f.batchReceipts[req.RequestID]; ok {
			_ = json.NewEncoder(w).Encode(previous)
			return
		}
		if req.ExpectedRevision == nil || *req.ExpectedRevision != f.revision {
			api.WriteProblem(w, api.NewProblem(409, api.CodeEnvBatchRevisionConflict, "Stale", "configuration changed"))
			return
		}
		for key, value := range req.Values {
			f.values[key] = value
			f.puts++
		}
		f.revision++
		receipt := api.EnvBatchResponse{RequestID: req.RequestID, AppID: f.base.app.ID, Scope: normalizedScope(r.URL.Query().Get("scope")), UpdatedKeys: sortedEnvBatchKeysForTest(req.Values), DeletedKeys: []string{}, ConfigChange: api.ConfigChangeReceipt{Revision: f.revision}}
		if f.batchReceipts == nil {
			f.batchReceipts = map[string]api.EnvBatchResponse{}
		}
		f.batchReceipts[req.RequestID] = receipt
		if f.failBatchAfterCommit {
			f.failBatchAfterCommit = false
			api.WriteProblem(w, api.NewProblem(502, "transport_error", "Response lost", "retry the same request"))
			return
		}
		_ = json.NewEncoder(w).Encode(receipt)
	case r.URL.Path == "/v1/apps/alpha/config/revisions":
		f.mu.Lock()
		defer f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(api.ListConfigRevisionsResponse{Revisions: []api.ConfigRevision{{Revision: f.revision, Scope: normalizedScope(r.URL.Query().Get("scope"))}}})
	case r.URL.Path == "/v1/apps/alpha/env":
		f.mu.Lock()
		defer f.mu.Unlock()
		vars := []api.AppEnvResponse{}
		for k := range f.values {
			vars = append(vars, api.AppEnvResponse{Key: k})
		}
		_ = json.NewEncoder(w).Encode(api.AppEnvListResponse{Env: vars})
	case strings.HasPrefix(r.URL.Path, "/v1/apps/alpha/env/") && r.Method == http.MethodPut:
		var v api.PutAppEnvRequest
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			w.WriteHeader(400)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.values[strings.TrimPrefix(r.URL.Path, "/v1/apps/alpha/env/")] = v.Value
		f.revision++
		f.puts++
		w.WriteHeader(200)
	case r.URL.Path == "/v1/apps/alpha/restart":
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.denyRestart {
			api.WriteProblem(w, api.NewProblem(403, "forbidden", "Scope required", "restart requires deployment write access"))
			return
		}
		f.restarts++
		f.keys = append(f.keys, r.Header.Get("Idempotency-Key"))
		_ = json.NewEncoder(w).Encode(api.AppRestartResponse{WakeID: uuid.NewString()})
	case r.URL.Path == "/v1/apps/alpha" && r.Method == http.MethodPatch:
		var req api.UpdateAppRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.patches++
		f.mu.Unlock()
		f.base.mu.Lock()
		defer f.base.mu.Unlock()
		if req.RAMMB != nil {
			f.base.app.RAMMB = *req.RAMMB
		}
		_ = json.NewEncoder(w).Encode(f.base.app)
	default:
		f.base.serveHTTP(w, r)
	}
}

func sortedEnvBatchKeysForTest(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func TestAtomicVariableBatchRecoversLostResponseAfterRestart(t *testing.T) {
	m, f := newOperationFixture(t)
	store, path, key := testStateStore(t)
	m.opts.planStore, m.opts.planNamespace = store, "atomic-recovery"
	plan, err := m.planNativeEnvChange(t.Context(), planEnvChangeArgs{App: "alpha", Reason: "atomic retry", Values: map[string]string{"FIRST": "one", "SECOND": "two"}})
	if err != nil {
		t.Fatal(err)
	}
	id := plan.(releasePlan).ID
	f.mu.Lock()
	f.failBatchAfterCommit = true
	f.mu.Unlock()
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: id}); err == nil {
		t.Fatal("lost response was hidden")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	opts := m.opts
	opts.planStore = next
	_, restored, err := newManagementServer(api.NewClient(opts.APIBase, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restored.applyOperation(t.Context(), applyArgs{PlanID: id}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.puts != 2 || len(f.batchBodies) != 2 || f.batchBodies[0] != f.batchBodies[1] {
		t.Fatalf("retry repeated writes or changed its request: puts=%d bodies=%v", f.puts, f.batchBodies)
	}
}

func newOperationFixture(t *testing.T) (*management, *operationFixture) {
	t.Helper()
	f := &operationFixture{base: newLocalFixture(t, true, "release-base"), revision: 1, values: map[string]string{}}
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(backend.Close)
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, agentKey: true, Capabilities: []string{api.ScopeEnvWrite}})
	if err != nil {
		t.Fatal(err)
	}
	return m, f
}

func TestNativeVariablePlanScansAndChecksRevision(t *testing.T) {
	m, f := newOperationFixture(t)
	plan, err := m.planNativeEnvChange(t.Context(), planEnvChangeArgs{App: "alpha", Reason: "change log level", Values: map[string]string{"LOG_LEVEL": "debug"}})
	if err != nil {
		t.Fatal(err)
	}
	p := plan.(releasePlan)
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "debug") {
		t.Fatal("plan leaked private configuration inputs")
	}
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: p.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: p.ID}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	puts := f.puts
	value := f.values["LOG_LEVEL"]
	f.mu.Unlock()
	if puts != 1 || value != "debug" {
		t.Fatalf("native variable writes=%d value=%s", puts, value)
	}
	plan, err = m.planNativeEnvChange(t.Context(), planEnvChangeArgs{App: "alpha", Reason: "next level", Values: map[string]string{"LOG_LEVEL": "info"}})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.revision++
	f.mu.Unlock()
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: plan.(releasePlan).ID}); err == nil {
		t.Fatal("stale configuration plan applied")
	}
	if _, err := m.planNativeEnvChange(t.Context(), planEnvChangeArgs{App: "alpha", Reason: "should refuse", Values: map[string]string{"TOKEN": "sk_" + "live_" + strings.Repeat("x", 38)}}); err == nil {
		t.Fatal("credential-shaped variable accepted")
	}
	metadata, err := m.getConfig(t.Context(), appArgs{App: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(metadata)
	if strings.Contains(string(raw), "debug") {
		t.Fatal("configuration read exposed variable values")
	}
}

func TestNativeConfigJournalRestoresPrivateInputs(t *testing.T) {
	m, f := newOperationFixture(t)
	store, path, key := testStateStore(t)
	m.opts.planStore, m.opts.planNamespace = store, "native-config"
	p, err := m.planNativeEnvChange(t.Context(), planEnvChangeArgs{App: "alpha", Reason: "restore private input", Values: map[string]string{"LOG_LEVEL": "debug"}})
	if err != nil {
		t.Fatal(err)
	}
	id := p.(releasePlan).ID
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	opts := m.opts
	opts.planStore = next
	_, restored, err := newManagementServer(api.NewClient(opts.APIBase, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := restored.getPlan(t.Context(), planLookupArgs{PlanID: id})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(receipt)
	if strings.Contains(string(raw), "debug") {
		t.Fatal("receipt exposed journaled configuration input")
	}
	for range 2 {
		if _, err := restored.applyOperation(t.Context(), applyArgs{PlanID: id}); err != nil {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.puts != 1 || f.values["LOG_LEVEL"] != "debug" {
		t.Fatal("restored plan lost its input or repeated a native write")
	}
}

func TestRestartAndScalingReceiptsDoNotRepeatWrites(t *testing.T) {
	m, f := newOperationFixture(t)
	plan, err := m.planRestart(t.Context(), operationArgs{App: "alpha", Reason: "apply new variables"})
	if err != nil {
		t.Fatal(err)
	}
	id := plan.(releasePlan).ID
	for range 2 {
		if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: id}); err != nil {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	if f.restarts != 1 || len(f.keys) != 1 || f.keys[0] != "mcp-operation-"+id {
		t.Fatalf("restart writes/keys=%d %v", f.restarts, f.keys)
	}
	f.mu.Unlock()
	ram := 512
	plan, err = m.planScale(t.Context(), scalePlanArgs{App: "alpha", Reason: "increase memory", RAMMB: &ram})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: plan.(releasePlan).ID}); err != nil {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.patches != 1 {
		t.Fatalf("scaling repeated %d times", f.patches)
	}
}

func TestOperationsRespectEnvironmentWriteConsent(t *testing.T) {
	m, _ := newOperationFixture(t)
	m.opts.Environments = &EnvironmentConsent{DefaultWrite: false, Named: map[string]EnvironmentGrant{}}
	plan, err := m.planRestart(t.Context(), operationArgs{App: "alpha", Reason: "read-only review"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: plan.(releasePlan).ID}); err == nil {
		t.Fatal("default write consent bypassed")
	}
}

func TestCatalogOutputContractsAndRetryHints(t *testing.T) {
	for _, tool := range toolsetCatalog(t, nil) {
		if tool.OutputSchema == nil {
			t.Errorf("%s lacks an output schema", tool.Name)
		}
		if strings.HasPrefix(tool.Name, "gregale_plan_") || tool.Name == "gregale_request_secret" {
			if tool.Annotations.IdempotentHint {
				t.Errorf("%s incorrectly promises idempotency", tool.Name)
			}
		}
	}
	if err := validateToolEnvelope("gregale_get_deployment", json.RawMessage(`{"traffic_percent":"wrong"}`)); err == nil {
		t.Fatal("output schema accepts the wrong numeric type")
	}
	if err := validateToolEnvelope("gregale_get_deployment", json.RawMessage(`{"id":"deployment-one","app_id":"app-one","status":"live","traffic_percent":9007199254740993}`)); err != nil {
		t.Fatalf("exact integer rejected: %v", err)
	}
}

func TestAsyncProvisionReturnsBeforeCLICompletes(t *testing.T) {
	f := newLocalFixture(t, true, "release-base")
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	defer backend.Close()
	cli := newProvisionCLI()
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	runner := func(ctx context.Context, args []string, in []byte) (LocalCLIResult, error) {
		if len(args) > 1 && args[0] == "crons" && args[1] == "add" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return LocalCLIResult{}, ctx.Err()
			}
		}
		return cli.cli(ctx, args, in)
	}
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), Options{APIBase: backend.URL, AllowWrites: true, LocalCLI: runner})
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.planCron(t.Context(), planCronArgs{App: "alpha", Reason: "async test", Path: "/tick", Schedule: "*/15 * * * *"})
	if err != nil {
		t.Fatal(err)
	}
	id := p.(releasePlan).ID
	if _, err := m.applyProvisionRequest(t.Context(), provisionApplyArgs{PlanID: id, Async: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not start")
	}
	if _, err := m.getProvision(t.Context(), planLookupArgs{PlanID: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.cancelProvision(t.Context(), planLookupArgs{PlanID: id}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := m.lifecycle.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}
