package apidserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	repairapi "github.com/onebox-faas/faas/pkg/apid/datarepair"
	"github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sched" //nolint:depguard // Repair acceptance drives the scheduler; production apid only records intent.
	"github.com/onebox-faas/faas/pkg/state"
)

type managedRepairRehearsal struct{}

func (managedRepairRehearsal) Rehearse(_ context.Context, p datarepair.Plan) (datarepair.Evidence, error) {
	e := datarepair.Evidence{Passed: true, PlanDigest: p.Digest, SnapshotSHA256: strings.Repeat("a", 64), Runner: "test-only", ChecksSHA256: p.ChecksSHA256}
	if len(p.Checks) != 0 {
		e.CheckResults = fixtureCheckReport(p, false)
	}
	return e, nil
}

func TestManagedDataRepairHTTPJourney(t *testing.T) {
	testManagedDataRepairHTTPJourney(t, false, false, false, false)
}

func TestManagedDataRepairScheduledHTTPJourney(t *testing.T) {
	if os.Getenv("GREGALE_DATA_REPAIR_PG_DUMP") == "" {
		if _, err := exec.LookPath("pg_dump"); err != nil {
			t.Skip("scheduled rehearsal requires the PostgreSQL 16 snapshot exporter")
		}
	}
	testManagedDataRepairHTTPJourney(t, true, false, false, false)
}

func TestManagedDataRepairNamedHTTPJourney(t *testing.T) {
	testManagedDataRepairHTTPJourney(t, false, true, false, false)
}
func TestManagedDataRepairNamedScheduledHTTPJourney(t *testing.T) {
	testManagedDataRepairHTTPJourney(t, true, true, false, false)
}

func TestManagedDataRepairValidationHTTPJourney(t *testing.T) {
	testManagedDataRepairHTTPJourney(t, false, false, true, false)
}

func TestManagedDataRepairReplacementHTTPJourney(t *testing.T) {
	testManagedDataRepairHTTPJourney(t, false, false, false, true)
}

func testManagedDataRepairHTTPJourney(t *testing.T, scheduled, named, validateDrift, replaceDrift bool) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	controlPool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, controlPool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(controlPool)
	acct, err := store.CreateAccount(ctx, "managed-repair@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key := func(scopes ...string) (string, string) {
		t.Helper()
		token, hash, _ := api.GenerateAPIKey()
		record, err := store.CreateAPIKey(ctx, acct.ID, hash, "repair", scopes)
		if err != nil {
			t.Fatal(err)
		}
		return token, record.ID
	}
	admin, adminID := key(api.ScopeAdmin)
	reader, _ := key(api.ScopeManagedPostgresRead)
	manager, managerID := key(api.ScopeManagedPostgresManage)
	deployer, _ := key(api.ScopeDeployWrite)
	appReader, _ := key(api.ScopeAppsRead)
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "managed-repair", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:repair"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, "customer", "Customer", 100)
	if err != nil {
		t.Fatal(err)
	}
	def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: api.OperationDefinitionSpec{Name: "import", Method: "POST", Path: "/import", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"importing"}, InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	source, _, err := store.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: tenant.ID, IdempotencyKey: "source", Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	scope := datarepair.Scope{AccountID: acct.ID, AppID: app.ID, Environment: dep.Scope, PlatformTenantID: tenant.ID}
	pool := pgtest.OpenDatabase(t)
	if _, err = pool.Exec(ctx, datarepair.Schema()); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `CREATE TABLE products(id bigint PRIMARY KEY,category text NOT NULL); SELECT gregale_repair.enroll('products',ARRAY['category']); INSERT INTO products SELECT generate_series(1,500),'original'`); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = datarepair.Capture(ctx, tx, scope, source.ID, "buggy-import-v1", "products", func(tx pgx.Tx) error { _, e := tx.Exec(ctx, `UPDATE products SET category='bad'`); return e }); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE products SET category='human' WHERE id<=40`); err != nil {
		t.Fatal(err)
	}
	engine := &datarepair.Engine{Pool: pool, Rehearser: managedRepairRehearsal{}}
	if named {
		if _, err := pool.Exec(ctx, `CREATE TABLE repair_policy(id int PRIMARY KEY,enabled boolean NOT NULL); INSERT INTO repair_policy VALUES(1,true)`); err != nil {
			t.Fatal(err)
		}
		engine.Checks = []datarepair.Check{
			{Name: "Corrected and human categories", SQL: "SELECT count(*) FILTER(WHERE category='original')=460 AND count(*) FILTER(WHERE category='human')=40 FROM public.products"},
			{Name: "Repair remains enabled", SQL: "SELECT enabled FROM public.repair_policy WHERE id=1 FOR SHARE"},
		}
	}
	cfg := pool.Config().ConnConfig
	sourceURI, err := url.Parse(cfg.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	sourceURI.Path, sourceURI.RawPath = "/"+cfg.Database, ""
	if os.Getenv("GREGALE_DATA_REPAIR_DOCKER_TEST") == "1" {
		// pgtest changes Database after parsing the original URI. ConnString
		// retains that original text; qualify the actual private database.
		cfg := pool.Config().ConnConfig
		sourceURI, e := url.Parse(cfg.ConnString())
		if e != nil {
			t.Fatal(e)
		}
		sourceURI.Path, sourceURI.RawPath = "/"+cfg.Database, ""
		engine.Rehearser = datarepair.DockerRehearser{SourceURL: sourceURI.String(), DumpBinary: os.Getenv("GREGALE_DATA_REPAIR_PG_DUMP"), Checks: engine.Checks}
	}
	executorToken := strings.Repeat("x", 64)
	backend := (&repairapi.Handler{Engine: engine, Exporter: &datarepair.SnapshotExporter{SourceURL: sourceURI.String(), DumpBinary: os.Getenv("GREGALE_DATA_REPAIR_PG_DUMP")}, Release: "repair-executor-v1", Coordinator: true, Authenticate: repairapi.Bearer(executorToken, repairapi.Principal{Scope: scope, Actor: "executor", CanApprove: true, CanApply: true})}).Routes()
	var failBeforeApply atomic.Bool
	failBeforeApply.Store(true)
	var loseApply atomic.Bool
	loseApply.Store(true)
	var applyCalls atomic.Int32
	var loseEvidence atomic.Bool
	loseEvidence.Store(scheduled)
	var evidenceCalls atomic.Int32
	var loseReplacement atomic.Bool
	loseReplacement.Store(replaceDrift)
	var loseResolution atomic.Bool
	loseResolution.Store(replaceDrift)
	var sourceUnavailable atomic.Bool
	executor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sourceUnavailable.Load() {
			w.WriteHeader(503)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/replace") || strings.HasSuffix(r.URL.Path, "/resolve") {
			out := httptest.NewRecorder()
			backend.ServeHTTP(out, r)
			if out.Code == 200 && (strings.HasSuffix(r.URL.Path, "/replace") && loseReplacement.Swap(false) || strings.HasSuffix(r.URL.Path, "/resolve") && loseResolution.Swap(false)) {
				w.WriteHeader(503)
				return
			}
			for name, values := range out.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(out.Code)
			_, _ = w.Write(out.Body.Bytes())
			return
		}
		if strings.HasSuffix(r.URL.Path, "/rehearsal-evidence") {
			evidenceCalls.Add(1)
			out := httptest.NewRecorder()
			backend.ServeHTTP(out, r)
			if out.Code == 200 && loseEvidence.Swap(false) {
				w.WriteHeader(503)
				return
			}
			for name, values := range out.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(out.Code)
			_, _ = w.Write(out.Body.Bytes())
			return
		}
		if strings.HasSuffix(r.URL.Path, "/apply") {
			applyCalls.Add(1)
			if failBeforeApply.Swap(false) {
				w.WriteHeader(503)
				return
			}
			out := httptest.NewRecorder()
			backend.ServeHTTP(out, r)
			if out.Code == 200 && loseApply.Swap(false) {
				w.WriteHeader(503)
				return
			}
			for name, values := range out.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(out.Code)
			_, _ = w.Write(out.Body.Bytes())
			return
		}
		backend.ServeHTTP(w, r)
	}))
	defer executor.Close()
	remote, err := repairapi.NewRemote(executor.URL, executorToken)
	if err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	handler := srv.Handler()
	base := "/v1/apps/" + app.Slug + "/data-repair/operations"
	id := uuid.NewString()
	do := func(method, path, bearer string, body any, headers ...map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		r.Header.Set("Content-Type", "application/json")
		for _, set := range headers {
			for k, v := range set {
				r.Header.Set(k, v)
			}
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	check := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("HTTP %d, want %d: %s", w.Code, status, w.Body.String())
		}
	}
	create := func() *httptest.ResponseRecorder {
		return do("POST", base, manager, map[string]string{"source_operation_id": source.ID}, map[string]string{"Idempotency-Key": id})
	}
	check(do("GET", base+"/"+id, "", nil), 401)
	check(create(), 503)
	binding := repairapi.Binding{Scope: scope, Identity: repairapi.BindingIdentity(scope, uuid.NewString(), executor.URL), Executor: remote, ScheduledRehearsal: scheduled}
	srv.WithDataRepairBindings([]repairapi.Binding{binding})
	doctorURL := strings.TrimSuffix(base, "/operations") + "/doctor?table=public.products"
	check(do("GET", doctorURL, "", nil), 401)
	check(do("GET", doctorURL, appReader, nil), 403)
	readDoctor := func(path string) datarepair.DoctorReport {
		t.Helper()
		response := do("GET", path, reader, nil)
		check(response, 200)
		var report datarepair.DoctorReport
		if json.Unmarshal(response.Body.Bytes(), &report) != nil || !report.Valid() || report.Scope != scope || report.Table != "public.products" ||
			len(report.Findings) != 10 || report.Findings[9].Code != "binding_configured" || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("readiness lost the exact current binding or bounded contract", response.Body.String())
		}
		return report
	}
	readDoctor(doctorURL)
	check(do("GET", doctorURL+"&account_id="+uuid.NewString(), reader, nil), 400)
	check(do("GET", doctorURL+"&environment=other", reader, nil), 404)
	check(do("GET", doctorURL+"&customer="+uuid.NewString(), reader, nil), 404)
	sourceUnavailable.Store(true)
	if report := readDoctor(doctorURL); report.Status != "unknown" || report.Findings[0].Code != "executor_unavailable" {
		t.Fatal("unreachable executor became ready", report)
	}
	sourceUnavailable.Store(false)
	sourcesURL := strings.TrimSuffix(base, "/operations") + "/sources"
	scopesURL := strings.TrimSuffix(base, "/operations") + "/source-scopes"
	check(do("GET", sourcesURL, "", nil), 401)
	check(do("GET", sourcesURL, appReader, nil), 403)
	check(do("GET", scopesURL, appReader, nil), 403)
	wScopes := do("GET", scopesURL, reader, nil)
	check(wScopes, 200)
	var configured repairapi.SourceScopesResponse
	if json.Unmarshal(wScopes.Body.Bytes(), &configured) != nil || len(configured.Scopes) != 1 || configured.Scopes[0] != scope {
		t.Fatal("discovery scope selection lost the exact configured binding")
	}
	discover := func(query string) datarepair.SourcePage {
		t.Helper()
		response := do("GET", sourcesURL+query, reader, nil)
		check(response, 200)
		if response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), `"category"`) {
			t.Fatal("capture discovery cached or exposed row values")
		}
		var page datarepair.SourcePage
		if json.Unmarshal(response.Body.Bytes(), &page) != nil || page.Scope != scope {
			t.Fatal("capture discovery crossed scope")
		}
		return page
	}
	page := discover("?release=buggy-import-v1&table=public.products")
	if len(page.Items) != 1 || page.Items[0].ID != source.ID || page.Items[0].CanPrepare || page.Items[0].SourceState != "unsettled" || page.Items[0].ChangeRows == nil || *page.Items[0].ChangeRows != 500 {
		t.Fatalf("unsettled source inventory: %+v", page)
	}
	check(do("GET", sourcesURL+"?account_id="+uuid.NewString(), reader, nil), 400)
	check(do("GET", sourcesURL+"?environment=other", reader, nil), 404)
	check(do("GET", sourcesURL+"?customer="+uuid.NewString(), reader, nil), 404)
	secondScope := binding
	secondScope.Scope.PlatformTenantID = uuid.NewString()
	srv.WithDataRepairBindings([]repairapi.Binding{binding, secondScope})
	check(do("GET", doctorURL, reader, nil), 400)
	readDoctor(doctorURL + "&environment=" + url.QueryEscape(scope.Environment) + "&customer=" + scope.PlatformTenantID)
	check(do("GET", sourcesURL, reader, nil), 400)
	page = discover("?environment=" + url.QueryEscape(scope.Environment) + "&customer=" + scope.PlatformTenantID)
	if len(page.Items) != 1 || page.Items[0].ID != source.ID {
		t.Fatal("explicit scope did not recover source inventory")
	}
	srv.WithDataRepairBindings([]repairapi.Binding{binding})
	check(create(), 409) // the source must first stop executing
	if _, err = store.CancelOperation(ctx, acct.ID, "", source.ID, 1); err != nil {
		t.Fatal(err)
	}
	page = discover("?release=buggy-import-v1")
	if len(page.Items) != 1 || !page.Items[0].CanPrepare || page.Items[0].SourceState != "settled" {
		t.Fatal("settled retained capture cannot be prepared")
	}
	check(do("POST", base, manager, map[string]any{"source_operation_id": source.ID, "scope": scope}, map[string]string{"Idempotency-Key": id}), 400)
	check(do("POST", base, manager, map[string]string{"source_operation_id": source.ID}, map[string]string{"Idempotency-Key": id, "Origin": "https://customer.invalid"}), 403)
	w := create()
	check(w, 200)
	var current repairapi.ManagedResponse
	load := func(w *httptest.ResponseRecorder) {
		t.Helper()
		check(w, 200)
		var next repairapi.ManagedResponse
		if err = json.Unmarshal(w.Body.Bytes(), &next); err != nil {
			t.Fatal(err)
		}
		current = next
	}
	load(w)
	if current.Plan.Candidates != 460 || current.Plan.Conflicts != 40 || current.Operation.ID != id {
		t.Fatalf("plan %+v", current.Operation.DataRepair)
	}
	if named && (len(current.Plan.Checks) != 2 || strings.Contains(w.Body.String(), "SELECT")) {
		t.Fatal("named declarations missing or query text exposed")
	}
	load(create())
	if current.Operation.LatestSequence != 1 {
		t.Fatal("prepare replay added lifecycle events")
	}
	check(do("GET", base+"/"+id, appReader, nil), 403)
	check(do("GET", base+"/"+id, manager, nil), 403)
	check(do("GET", base+"/"+id, reader, nil), 200)
	other, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "other-repair", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	check(do("GET", strings.Replace(base, app.Slug, other.Slug, 1)+"/"+id, reader, nil), 404)
	approval := current.ApprovalURL
	decision := api.OperationApprovalRequest{ExpectedGeneration: 1, Decision: api.OperationApprovalApproved}
	check(do("POST", approval, manager, decision), 409)
	action := func(name string) *httptest.ResponseRecorder {
		return do("POST", base+"/"+id+"/"+name, manager, map[string]any{"digest": current.Plan.Digest, "expected_sequence": current.Operation.LatestSequence})
	}
	check(action("apply"), 409)
	if named && scheduled {
		// Lose the publication response for a real failed guest check. Recovery
		// must publish the retired outcome without executing the guest again.
		if _, err := pool.Exec(ctx, `UPDATE repair_policy SET enabled=false`); err != nil {
			t.Fatal(err)
		}
		load(action("rehearse"))
		ledger := sched.NewNodeLedger()
		admission, err := sched.NewEngine(ctx, store, ledger, nil, nil, "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
		if err != nil {
			t.Fatal(err)
		}
		runner := datarepair.RunnerSpec{BaseKey: "test/base", LayerKey: "test/layer", KernelSHA256: strings.Repeat("1", 64), BaseSHA256: strings.Repeat("2", 64), LayerSHA256: strings.Repeat("3", 64)}
		vm := &managedRepairVMFixture{incarnation: uuid.NewString(), helper: os.Getenv("GREGALE_DATA_REPAIR_GUEST_HELPER"), failChecks: true}
		coordinator := &sched.DataRepairRehearsalCoordinator{Store: store, Admission: admission, VMM: vm, Bindings: []sched.DataRepairRehearsalBinding{{Scope: scope, Identity: binding.Identity, Executor: remote}}, Runner: runner, NodeID: node.ID, Enabled: true}
		processed, err := coordinator.ProcessNext(ctx)
		if !processed || err == nil || ledger.ResidentRAM() != 0 {
			t.Fatal("negative publication disconnect did not preserve a retired outcome", processed, err)
		}
		if err := coordinator.RecoverOnce(ctx); err != nil {
			t.Fatal(err)
		}
		load(do("GET", base+"/"+id, reader, nil))
		if current.Operation.DataRepair.Phase != api.DataRepairPrepared || current.Operation.DataRepair.RehearsalStatus != state.RepairRehearsalFailed || current.Plan.Rehearsal != nil || current.Plan.RehearsedAt != nil || current.Plan.CheckFailure == nil || !current.Plan.ValidCheckResults(current.Plan.CheckFailure.Evidence) || current.Plan.CheckFailure.Evidence.Passed || vm.runs != 1 || evidenceCalls.Load() != 1 {
			t.Fatal("negative report enabled approval or was replayed/lost")
		}
		check(do("POST", approval, manager, decision), 409)
		if _, err := pool.Exec(ctx, `UPDATE repair_policy SET enabled=true`); err != nil {
			t.Fatal(err)
		}
		loseEvidence.Store(true)
		evidenceCalls.Store(0)
	}
	load(action("rehearse"))
	if scheduled {
		if current.Operation.DataRepair.RehearsalStatus != state.RepairRehearsalQueued || current.Plan.Rehearsal != nil {
			t.Fatal("scheduled request executed inline")
		}
		sourceUnavailable.Store(true)
		check(action("cancel-rehearsal"), 503) // Plan refresh is unavailable, but intent must be durable.
		cancelled, e := store.OperationByID(ctx, acct.ID, "", id)
		if e != nil || !cancelled.DataRepair.RehearsalCancelRequested || cancelled.DataRepair.RehearsalStatus != state.RepairRehearsalCancelled {
			t.Fatal("source outage prevented cancellation", e)
		}
		sourceUnavailable.Store(false)
		load(do("GET", base+"/"+id, reader, nil))
		load(action("rehearse"))
		check(do("POST", approval, manager, decision), 409)
		check(action("apply"), 409)
		load(action("reconcile"))
		if current.Operation.DataRepair.Phase != api.DataRepairRehearsing {
			t.Fatal("API reconcile released an admitted task")
		}
		ledger := sched.NewNodeLedger()
		admitter, e := sched.NewEngine(ctx, store, ledger, nil, nil, "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
		if e != nil {
			t.Fatal(e)
		}
		node, e := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
		if e != nil {
			t.Fatal(e)
		}
		runner := datarepair.RunnerSpec{BaseKey: "test/base", LayerKey: "test/layer", KernelSHA256: strings.Repeat("1", 64), BaseSHA256: strings.Repeat("2", 64), LayerSHA256: strings.Repeat("3", 64)}
		vm := &managedRepairVMFixture{incarnation: uuid.NewString(), helper: os.Getenv("GREGALE_DATA_REPAIR_GUEST_HELPER"), loseResponse: true, failRetirement: true}
		coordinator := &sched.DataRepairRehearsalCoordinator{Store: store, Admission: admitter, VMM: vm, Bindings: []sched.DataRepairRehearsalBinding{{Scope: scope, Identity: binding.Identity, Executor: remote}}, Runner: runner, NodeID: node.ID, Enabled: true}
		claimLoss := &repairClaimLoss{DataRepairRehearsalStore: store}
		coordinator.Store = claimLoss
		processed, e := coordinator.ProcessNext(ctx)
		if processed || e == nil || ledger.ResidentRAM() == 0 {
			t.Fatal("uncertain claim released its reservation")
		}
		if e = coordinator.RecoverOnce(ctx); e != nil || ledger.ResidentRAM() != 0 {
			t.Fatal("uncommitted claim was not recovered", e)
		}
		processed, e = coordinator.ProcessNext(ctx)
		if !processed && e == nil {
			processed, e = coordinator.ProcessNext(ctx)
		}
		if !processed || e == nil || ledger.ResidentRAM() != api.DataRepairSandboxMemoryMB+api.PerVMOverheadMB {
			t.Fatal("lost VM result released unconfirmed capacity", processed, e)
		}
		load(do("GET", base+"/"+id, reader, nil))
		if current.Operation.DataRepair.RehearsalStatus != state.RepairRehearsalCleanup || current.Plan.Rehearsal != nil {
			t.Fatal("lost VM result supplied evidence")
		}
		vm.failRetirement = false
		recovery := *coordinator
		recovery.Enabled = false
		recovery.Bindings = nil
		rebuiltLedger := sched.NewNodeLedger()
		rebuilt, e := sched.NewEngine(ctx, store, rebuiltLedger, nil, nil, "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
		if e != nil {
			t.Fatal(e)
		}
		if e = rebuilt.SeedDataRepairRehearsals(ctx); e != nil || rebuiltLedger.ResidentRAM() != api.DataRepairSandboxMemoryMB+api.PerVMOverheadMB {
			t.Fatal("restart lost cleanup capacity", e)
		}
		recovery.Admission = rebuilt
		if e = recovery.RecoverOnce(ctx); e != nil {
			t.Fatal("cleanup without dispatch configuration", e)
		}
		ledger = rebuiltLedger
		coordinator.Admission = rebuilt
		load(do("GET", base+"/"+id, reader, nil))
		if current.Operation.DataRepair.Phase != api.DataRepairPrepared || ledger.ResidentRAM() != 0 || vm.runs != 1 {
			t.Fatal("cleanup replayed an abandoned VM")
		}
		load(action("rehearse")) // Explicitly request a new task for this same frozen plan.
		processed, e = coordinator.ProcessNext(ctx)
		if !processed && e == nil {
			processed, e = coordinator.ProcessNext(ctx)
		}
		if !processed || e == nil {
			t.Fatal("lost evidence response was not exercised", processed, e, vm.lastError)
		}
		load(do("GET", base+"/"+id, reader, nil))
		if current.Plan.Rehearsal == nil || current.Operation.DataRepair.RehearsalStatus != state.RepairRehearsalPublishing || ledger.ResidentRAM() != 0 {
			t.Fatal("evidence publication did not survive disconnect after cleanup")
		}
		// A restarted coordinator consumes the persisted publication without
		// restoring another database or starting another VM.
		recovered := *coordinator
		if e = recovered.RecoverOnce(ctx); e != nil {
			t.Fatal(e)
		}
		load(do("GET", base+"/"+id, reader, nil))
		if vm.runs != 2 || evidenceCalls.Load() != 1 {
			t.Fatal("publication recovery replayed rehearsal")
		}
		if _, e = store.CancelDataRepairRehearsal(ctx, acct.ID, id, current.Operation.LatestSequence); e == nil {
			t.Fatal("completed rehearsal was cancelled")
		}
	}
	if current.Operation.State != api.OperationWaiting {
		t.Fatal("rehearsal skipped approval")
	}
	if named && (current.Plan.Rehearsal == nil || !current.Plan.ValidCheckResults(*current.Plan.Rehearsal) || current.Plan.CheckFailure != nil) {
		t.Fatal("successful named rehearsal lost its results or kept an obsolete failure")
	}
	check(do("POST", approval, deployer, decision), 403)
	check(do("POST", approval, manager, map[string]any{"expected_generation": 1, "decision": "approved", "actor": map[string]string{"id": "forged"}}), 400)
	check(do("POST", approval, manager, decision), 200)
	load(do("GET", base+"/"+id, reader, nil))
	if current.Operation.DataRepair.Approval.Actor.ID != managerID {
		t.Fatal("approval actor not authenticated")
	}
	verify := func() datarepair.VerificationReport {
		t.Helper()
		before, err := store.OperationByID(ctx, acct.ID, "", id)
		if err != nil {
			t.Fatal(err)
		}
		response := do("POST", base+"/"+id+"/verify", reader, map[string]string{"digest": current.Plan.Digest})
		check(response, 200)
		var report datarepair.VerificationReport
		if json.Unmarshal(response.Body.Bytes(), &report) != nil || !report.Valid() || report.Scope != scope || report.PlanID != id || report.PlanDigest != current.Plan.Digest ||
			report.RepairOperationID != id || report.SourceOperationID != current.Plan.SourceOperationID || report.Restored != current.Plan.Candidates || report.Excluded != current.Plan.Conflicts+current.Plan.Unavailable {
			t.Fatal("hosted verification lost receipt identity or counts", response.Body.String())
		}
		after, err := store.OperationByID(ctx, acct.ID, "", id)
		beforeJSON, _ := json.Marshal(before)
		afterJSON, _ := json.Marshal(after)
		if err != nil || !bytes.Equal(beforeJSON, afterJSON) {
			t.Fatal("verification advanced CP outcome, approval or event history", err)
		}
		return report
	}
	if replaceDrift {
		parentID, parentDigest := id, current.Plan.Digest
		parentApproval, _ := json.Marshal(current.Operation.DataRepair.Approval)
		if _, err := pool.Exec(ctx, `UPDATE products SET category='human' WHERE id=41`); err != nil {
			t.Fatal(err)
		}
		// An actual row conflict leaves no receipt. Resolution must first fence
		// apply in CP, then obtain durable closure from this exact source.
		failBeforeApply.Store(false)
		loseApply.Store(false)
		check(action("apply"), 503)
		load(do("GET", base+"/"+id, reader, nil))
		if current.Operation.DataRepair.Phase != api.DataRepairUnknown || current.Plan.AppliedAt != nil {
			t.Fatal("row conflict did not preserve uncertain outcome")
		}
		resolvePath := base + "/" + id + "/resolve"
		resolveBody := map[string]any{"digest": parentDigest, "expected_sequence": current.Operation.LatestSequence}
		check(do("POST", resolvePath, "", resolveBody), 401)
		check(do("POST", resolvePath, reader, resolveBody), 403)
		check(do("POST", resolvePath, manager, resolveBody, map[string]string{"Origin": "https://customer.invalid"}), 403)
		check(do("POST", resolvePath, manager, map[string]any{"digest": parentDigest, "expected_sequence": current.Operation.LatestSequence, "actor": "forged"}), 400)
		check(do("POST", resolvePath, manager, map[string]any{"digest": parentDigest, "expected_sequence": current.Operation.LatestSequence, "apply_closed_at": time.Now()}), 400)
		check(do("POST", resolvePath, manager, map[string]any{"digest": strings.Repeat("0", 64), "expected_sequence": current.Operation.LatestSequence}), 409)
		check(do("POST", resolvePath, manager, map[string]any{"digest": parentDigest, "expected_sequence": current.Operation.LatestSequence - 1}), 409)
		sourceUnavailable.Store(true)
		check(do("POST", resolvePath, manager, resolveBody), 503)
		sourceUnavailable.Store(false)
		load(do("GET", base+"/"+id, reader, nil))
		if current.Operation.DataRepair.Phase != api.DataRepairResolving || current.Plan.ApplyResolution != nil {
			t.Fatal("unavailable source released apply authority")
		}
		check(action("apply"), 409)
		check(do("POST", base+"/"+id+"/replace", manager, map[string]any{"digest": parentDigest, "expected_sequence": current.Operation.LatestSequence}, map[string]string{"Idempotency-Key": uuid.NewString()}), 409)
		// A busy source is unresolved even when its visible receipt is absent.
		lock, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := lock.Exec(ctx, `SELECT id FROM gregale_repair.plans WHERE id=$1 FOR UPDATE`, id); err != nil {
			_ = lock.Rollback(ctx)
			t.Fatal(err)
		}
		check(do("POST", resolvePath, manager, resolveBody), 503)
		if err := lock.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		// Lose the source closure response from a different authenticated admin.
		// Its actor is recorded without rewriting the original approval. CP
		// remains resolving until the matching durable result is recovered.
		check(do("POST", resolvePath, admin, resolveBody), 503)
		load(do("GET", base+"/"+id, reader, nil))
		if current.Operation.DataRepair.Phase != api.DataRepairResolving || current.Plan.ApplyResolution == nil || current.Plan.ApplyResolution.ResolvedBy != adminID {
			t.Fatal("lost closure response lost source proof or released CP reservation")
		}
		load(do("POST", resolvePath, manager, resolveBody))
		if current.Operation.DataRepair.Phase != api.DataRepairClosed || current.Operation.FailureCode != "repair_not_committed" || current.Operation.DataRepair.ApplyClosedAt == nil || !current.Operation.DataRepair.ApplyClosedAt.Equal(current.Plan.ApplyResolution.ResolvedAt) {
			t.Fatal("durable source closure did not settle the hosted outcome")
		}
		closedSequence := current.Operation.LatestSequence
		load(do("POST", resolvePath, manager, resolveBody))
		if current.Operation.LatestSequence != closedSequence {
			t.Fatal("closure replay created duplicate history")
		}
		if cli := os.Getenv("GREGALE_DATA_REPAIR_CLI"); cli != "" {
			apiServer := httptest.NewServer(handler)
			defer apiServer.Close()
			cmd := exec.CommandContext(ctx, cli, "data-repair", "resolve", id, "--app", app.Slug, "--digest", parentDigest, "--yes")
			cmd.Env = append(os.Environ(), "FAAS_API="+apiServer.URL, "FAAS_TOKEN="+admin)
			output, err := cmd.CombinedOutput()
			var response repairapi.ManagedResponse
			if err != nil || json.Unmarshal(output, &response) != nil || response.Operation.DataRepair.Phase != api.DataRepairClosed || !response.Plan.ResolvedApply(id, parentDigest, id) {
				t.Fatalf("hosted resolution CLI: %s %v", output, err)
			}
		}
		replacementID := uuid.NewString()
		replacementPath := base + "/" + parentID + "/replace"
		body := map[string]any{"digest": parentDigest, "expected_sequence": current.Operation.LatestSequence}
		headers := map[string]string{"Idempotency-Key": replacementID}
		check(do("POST", replacementPath, "", body, headers), 401)
		check(do("POST", replacementPath, reader, body, headers), 403)
		check(do("POST", replacementPath, manager, body, map[string]string{"Idempotency-Key": replacementID, "Origin": "https://customer.invalid"}), 403)
		check(do("POST", replacementPath, manager, map[string]any{"digest": parentDigest, "expected_sequence": current.Operation.LatestSequence, "scope": scope}, headers), 400)
		check(do("POST", replacementPath, manager, map[string]any{"digest": strings.Repeat("0", 64), "expected_sequence": current.Operation.LatestSequence}, headers), 409)
		check(do("POST", replacementPath, manager, map[string]any{"digest": parentDigest, "expected_sequence": current.Operation.LatestSequence - 1}, headers), 409)
		// The source commits its child and parent fence, but its response is lost
		// before control-plane admission. Only the original identity can recover.
		check(do("POST", replacementPath, manager, body, headers), 503)
		load(do("GET", base+"/"+parentID, reader, nil))
		if current.Operation.DataRepair.Phase != api.DataRepairReplacing || current.Operation.DataRepair.ReplacementOperationID != replacementID || current.Plan.ReplacementPlanID != replacementID {
			t.Fatal("lost replacement response did not retain both fences")
		}
		if _, err := store.OperationByID(ctx, acct.ID, "", replacementID); err == nil {
			t.Fatal("child admitted before source acknowledgement")
		}
		saved, err := engine.Get(ctx, scope, replacementID)
		if err != nil || saved.Candidates != 459 || saved.Conflicts != 41 {
			t.Fatal("source replacement lost narrowed selection", err)
		}
		check(do("POST", replacementPath, manager, body, map[string]string{"Idempotency-Key": uuid.NewString()}), 409)
		check(action("apply"), 409)
		check(action("rehearse"), 409)
		health, err := store.DataRepairHealth(ctx, time.Now(), "", 100)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range health.Items {
			if item.OperationID == parentID && item.Phase == api.DataRepairReplacing && strings.Contains(item.RecoveryGuidance, "same UUID") {
				found = true
			}
		}
		if !found {
			t.Fatal("pending replacement is absent from operator health")
		}
		// Compact the old source payload while CP still has the lost-response
		// reservation. Backdate only this fixture's creation metadata; the
		// child's original expiry and frozen document must not be refreshed.
		if _, err := pool.Exec(ctx, `UPDATE gregale_repair.plans SET created_at=clock_timestamp()-
 CASE WHEN id=$1::uuid THEN interval '10 days' ELSE interval '8 days' END WHERE id IN ($1::uuid,$2::uuid)`, parentID, replacementID); err != nil {
			t.Fatal(err)
		}
		batch, err := engine.PlanRetention(ctx, scope, time.Time{}, api.DataRepairPurgeMaxItems)
		if err != nil || len(batch.Entries) != 1 || batch.Entries[0].ID != parentID || batch.Entries[0].Reason != "superseded_plan" || batch.Entries[0].ReplacementPlanID != replacementID {
			t.Fatal("superseded parent inventory", batch, err)
		}
		if _, err := engine.PurgeRetention(ctx, scope, batch.ID, batch.Digest, "maintenance-operator"); err != nil {
			t.Fatal(err)
		}
		load(do("GET", base+"/"+parentID, reader, nil))
		if current.Plan.PayloadPurgedAt == nil || len(current.Plan.Items) != 0 || current.Plan.ApplyResolution == nil || current.Operation.DataRepair.Phase != api.DataRepairReplacing {
			t.Fatal("compaction lost closure or released hosted replacement reservation")
		}
		load(do("POST", resolvePath, manager, resolveBody))
		if current.Plan.PayloadPurgedAt == nil || !current.Plan.ResolvedApply(parentID, parentDigest, parentID) || current.Operation.DataRepair.Phase != api.DataRepairReplacing {
			t.Fatal("compacted closure could not be recovered during replacement")
		}
		load(do("POST", replacementPath, manager, body, headers)) // replay old sequence with the same saved identity
		id = replacementID
		if current.Operation.ID != id || current.Operation.DataRepair.ReplacesPlanID != parentID || current.Operation.DataRepair.Approval != nil || current.Operation.DataRepair.Phase != api.DataRepairPrepared || current.Plan.ApprovedAt != nil || current.Plan.RehearsedAt != nil || current.Plan.Replacement == nil || !current.Plan.Replacement.Valid(current.Plan) {
			t.Fatal("replacement inherited authority or lost lineage")
		}
		replayed, err := engine.Get(ctx, scope, replacementID)
		before, _ := json.Marshal(saved)
		after, _ := json.Marshal(replayed)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("resumption refreshed the frozen child", err)
		}
		parent, err := store.OperationByID(ctx, acct.ID, "", parentID)
		approvalAfter, _ := json.Marshal(parent.DataRepair.Approval)
		if err != nil || parent.DataRepair.Phase != api.DataRepairReplaced || parent.DataRepair.PlanDigest != parentDigest || !bytes.Equal(parentApproval, approvalAfter) {
			t.Fatal("parent approval history changed", err)
		}
		check(do("POST", current.ApprovalURL, manager, decision), 409)
		resolutionReplay := do("POST", resolvePath, manager, resolveBody)
		check(resolutionReplay, 200)
		var resolvedParent repairapi.ManagedResponse
		if json.Unmarshal(resolutionReplay.Body.Bytes(), &resolvedParent) != nil || resolvedParent.Operation.DataRepair.Phase != api.DataRepairReplaced || resolvedParent.Operation.DataRepair.ApplyClosedAt == nil {
			t.Fatal("replacement invalidated original resolution replay")
		}
		// Replaying completion creates neither a second child nor another event.
		load(do("POST", replacementPath, manager, body, headers))
		if current.Operation.LatestSequence != 1 {
			t.Fatal("replacement replay added child lifecycle events")
		}
		if cli := os.Getenv("GREGALE_DATA_REPAIR_CLI"); cli != "" {
			cliOperator, _ := key(api.ScopeManagedPostgresRead, api.ScopeManagedPostgresManage)
			apiServer := httptest.NewServer(handler)
			defer apiServer.Close()
			cmd := exec.CommandContext(ctx, cli, "data-repair", "replace", parentID, "--app", app.Slug, "--digest", parentDigest, "--repair-operation", replacementID, "--yes")
			cmd.Env = append(os.Environ(), "FAAS_API="+apiServer.URL, "FAAS_TOKEN="+cliOperator)
			output, err := cmd.CombinedOutput()
			var response repairapi.ManagedResponse
			if err != nil || json.Unmarshal(output, &response) != nil || response.Operation.ID != id || response.Plan.Candidates != 459 || response.Plan.Replacement.ParentPlanID != parentID {
				t.Fatalf("hosted replacement CLI: %s %v", output, err)
			}
		}
		load(action("rehearse"))
		check(do("POST", current.ApprovalURL, manager, decision), 200)
		load(do("GET", base+"/"+id, reader, nil))
		failBeforeApply.Store(false)
		loseApply.Store(false)
		load(action("apply"))
		if current.Operation.DataRepair.Phase != api.DataRepairSucceeded || current.Plan.RepairOperationID == nil || *current.Plan.RepairOperationID != replacementID {
			t.Fatal("replacement receipt was not projected")
		}
		var restored, human, changes int
		if err := pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE category='original'),count(*) FILTER(WHERE category='human'),(SELECT count(*) FROM gregale_repair.changes WHERE operation_id=$1) FROM products`, id).Scan(&restored, &human, &changes); err != nil || restored != 459 || human != 41 || changes != 459 {
			t.Fatal("replacement result", restored, human, changes, err)
		}
		verificationPath := base + "/" + id + "/verify"
		check(do("POST", verificationPath, "", map[string]string{"digest": current.Plan.Digest}), 401)
		check(do("POST", verificationPath, appReader, map[string]string{"digest": current.Plan.Digest}), 403)
		check(do("POST", verificationPath, reader, map[string]string{"digest": current.Plan.Digest}, map[string]string{"Origin": "https://customer.invalid"}), 403)
		check(do("POST", verificationPath, reader, map[string]any{"digest": current.Plan.Digest, "scope": scope}), 400)
		check(do("POST", verificationPath, reader, map[string]any{"digest": current.Plan.Digest, "expected_sequence": current.Operation.LatestSequence}), 400)
		check(do("POST", verificationPath, reader, map[string]string{"digest": strings.Repeat("0", 64)}), 409)
		if r := verify(); !r.Available || r.Unchanged != 459 {
			t.Fatal("replacement verification did not observe its committed rows", r)
		}
		if _, err := pool.Exec(ctx, `UPDATE products SET category='human' WHERE id=42`); err != nil {
			t.Fatal(err)
		}
		if r := verify(); !r.Available || r.Restored != 459 || r.Unchanged != 458 || r.Edited != 1 || r.Excluded != 41 {
			t.Fatal("later edit changed the original commit result", r)
		}
		if cli := os.Getenv("GREGALE_DATA_REPAIR_CLI"); cli != "" {
			apiServer := httptest.NewServer(handler)
			defer apiServer.Close()
			cmd := exec.CommandContext(ctx, cli, "data-repair", "verify", id, "--app", app.Slug, "--digest", current.Plan.Digest)
			cmd.Env = append(os.Environ(), "FAAS_API="+apiServer.URL, "FAAS_TOKEN="+reader)
			output, err := cmd.CombinedOutput()
			var report datarepair.VerificationReport
			if err != nil || json.Unmarshal(output, &report) != nil || !report.Valid() || report.Restored != 459 || report.Unchanged != 458 || report.Edited != 1 {
				t.Fatalf("hosted read-only verification CLI: %s %v", output, err)
			}
		}
		check(do("POST", base+"/"+id+"/replace", manager, map[string]any{"digest": current.Plan.Digest, "expected_sequence": current.Operation.LatestSequence}, map[string]string{"Idempotency-Key": uuid.NewString()}), 409)
		// Recover the entire chain using only the bad import's source UUID.
		historyURL := sourcesURL + "/" + source.ID + "/history"
		readHistory := func(query string) datarepair.HistoryPage {
			t.Helper()
			limit := api.DataRepairSourcePageSize
			if strings.Contains(query, "limit=1") {
				limit = 1
			}
			response := do("GET", historyURL+query, reader, nil)
			check(response, 200)
			var page datarepair.HistoryPage
			if response.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(response.Body.Bytes(), &page) != nil || !page.Valid(limit) ||
				page.Scope != scope || page.SourceOperationID != source.ID || strings.Contains(response.Body.String(), `"category"`) {
				t.Fatal("invalid, cacheable or payload-bearing hosted history", response.Body.String())
			}
			return page
		}
		check(do("GET", historyURL, "", nil), 401)
		check(do("GET", historyURL, appReader, nil), 403)
		check(do("GET", historyURL, manager, nil), 403)
		check(do("GET", historyURL+"?scope=forged", reader, nil), 400)
		check(do("GET", historyURL+"?customer="+uuid.NewString(), reader, nil), 404)
		check(do("GET", strings.Replace(historyURL, app.Slug, other.Slug, 1), reader, nil), 404)
		first := readHistory("?limit=1")
		if len(first.Items) != 1 || first.Items[0].ID != id || first.Items[0].Candidates != 459 || first.Items[0].Conflicts != 41 || first.Items[0].AppliedAt == nil ||
			first.Items[0].Replacement == nil || first.Items[0].Replacement.ParentPlanID != parentID || first.Items[0].Execution == nil || first.Items[0].Execution.Phase != api.DataRepairSucceeded || first.NextCursor == "" {
			t.Fatal("history lost committed replacement", first)
		}
		next := readHistory("?limit=1&cursor=" + url.QueryEscape(first.NextCursor))
		if len(next.Items) != 1 || next.Items[0].ID != parentID || next.Items[0].ApplyResolution == nil || next.Items[0].ReplacementPlanID != id || next.Items[0].PayloadPurgedAt == nil || next.Items[0].AppliedAt != nil {
			t.Fatal("history lost compacted parent closure/fence", next)
		}
		changedBinding := binding
		changedBinding.Identity = strings.Repeat("f", 64)
		srv.WithDataRepairBindings([]repairapi.Binding{changedBinding})
		check(do("GET", historyURL+"?limit=1&cursor="+url.QueryEscape(first.NextCursor), reader, nil), 400)
		changedPage := readHistory("")
		if changedPage.Items[0].Execution.Availability != "unavailable" || changedPage.Items[0].Execution.Phase != "" {
			t.Fatal("history advertised execution authority across changed binding")
		}
		srv.WithDataRepairBindings([]repairapi.Binding{binding})
		retainedURL := sourcesURL + "/" + source.ID + "/plans/" + id
		check(do("GET", retainedURL, appReader, nil), 403)
		check(do("GET", retainedURL+"?limit=1", reader, nil), 400)
		check(do("GET", strings.Replace(retainedURL, source.ID, uuid.NewString(), 1), reader, nil), 404)
		check(do("POST", retainedURL+"/verify", reader, map[string]string{"digest": current.Plan.Digest}, map[string]string{"Origin": "https://customer.invalid"}), 403)
		check(do("POST", retainedURL+"/verify", reader, map[string]any{"digest": current.Plan.Digest, "expected_sequence": 1}), 400)
		check(do("POST", retainedURL+"/verify", reader, map[string]string{"digest": strings.Repeat("0", 64)}), 409)
		// Age only private fixture metadata, then use the reviewed owner purge.
		if _, err := pool.Exec(ctx, `UPDATE gregale_repair.plans SET created_at=clock_timestamp()-interval '8 days',applied_at=clock_timestamp()-interval '8 days' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		compact, err := engine.PlanRetention(ctx, scope, time.Time{}, api.DataRepairPurgeMaxItems)
		if err != nil || len(compact.Entries) != 1 || compact.Entries[0].ID != id {
			t.Fatal("replacement compaction inventory", compact, err)
		}
		if _, err := engine.PurgeRetention(ctx, scope, compact.ID, compact.Digest, "history-maintenance"); err != nil {
			t.Fatal(err)
		}
		// Expiry takes effect before asynchronous pruning removes stored rows.
		if _, err := controlPool.Exec(ctx, `UPDATE customer_operations
 SET expires_at=statement_timestamp()-interval '1 second',
 record=jsonb_set(record,'{expires_at}',to_jsonb(statement_timestamp()-interval '1 second'))
 WHERE id IN ($1::uuid,$2::uuid,$3::uuid)`, source.ID, parentID, id); err != nil {
			t.Fatal(err)
		}
		check(do("GET", base+"/"+id, reader, nil), 410)
		expiredSource := discover("?release=buggy-import-v1")
		if len(expiredSource.Items) != 1 || expiredSource.Items[0].CanPrepare || expiredSource.Items[0].SourceState != "not_retained" {
			t.Fatal("expired source Operation blocked metadata discovery or offered preparation")
		}
		expiredHistory := readHistory("")
		if len(expiredHistory.Items) != 2 {
			t.Fatal("source history became unavailable between ledger expiry and pruning")
		}
		for _, item := range expiredHistory.Items {
			if item.Execution.Availability != "not_retained" || item.Execution.Phase != "" {
				t.Fatal("expired execution leaked a phase or blocked source history", item)
			}
		}
		check(do("GET", retainedURL, reader, nil), 200)
		// The normal hosted ledger expires. Read-only recovery must neither
		// recreate it nor turn retained source approval into fresh apply authority.
		if _, err := store.PruneOperationState(ctx, time.Now().AddDate(1, 0, 0), api.OperationRetentionPageMax); err != nil {
			t.Fatal(err)
		}
		check(do("GET", base+"/"+id, reader, nil), 404)
		recovered := readHistory("")
		if len(recovered.Items) != 2 {
			t.Fatal("source history depended on retained hosted source Operation", recovered)
		}
		for _, item := range recovered.Items {
			if item.PayloadPurgedAt == nil || item.Execution.Availability != "not_retained" || item.Execution.Phase != "" {
				t.Fatal("expired execution history was fabricated or payload compaction lost", item)
			}
		}
		w := do("GET", retainedURL, reader, nil)
		check(w, 200)
		var historical repairapi.HistoricalPlanResponse
		if json.Unmarshal(w.Body.Bytes(), &historical) != nil || historical.Execution.Availability != "not_retained" || historical.Plan.PayloadPurgedAt == nil || historical.Plan.RepairOperationID == nil || *historical.Plan.RepairOperationID != id {
			t.Fatal("source receipt could not be reopened after hosted retention", w.Body.String())
		}
		beforeCalls := applyCalls.Load()
		w = do("POST", retainedURL+"/verify", reader, map[string]string{"digest": current.Plan.Digest})
		check(w, 200)
		var report datarepair.VerificationReport
		if json.Unmarshal(w.Body.Bytes(), &report) != nil || !report.MatchesPlan(historical.Plan) || report.Unchanged != 458 || report.Edited != 1 || report.Excluded != 41 {
			t.Fatal("verification after hosted retention lost original result", w.Body.String())
		}
		if cli := os.Getenv("GREGALE_DATA_REPAIR_CLI"); cli != "" {
			apiServer := httptest.NewServer(handler)
			defer apiServer.Close()
			for _, verb := range []string{"history", "get", "verify"} {
				target := id
				args := []string{"data-repair", verb}
				if verb == "history" {
					target = source.ID
				} else {
					args = append(args, "--source", source.ID)
				}
				args = append(args, target, "--app", app.Slug, "--environment", scope.Environment, "--customer", scope.PlatformTenantID)
				if verb == "verify" {
					args = append(args, "--digest", current.Plan.Digest)
				}
				cmd := exec.CommandContext(ctx, cli, args...)
				cmd.Env = append(os.Environ(), "FAAS_API="+apiServer.URL, "FAAS_TOKEN="+reader)
				if output, err := cmd.CombinedOutput(); err != nil || !bytes.Contains(output, []byte(current.Plan.Digest)) {
					t.Fatalf("hosted history CLI %s: %s %v", verb, output, err)
				}
			}
		}
		if applyCalls.Load() != beforeCalls {
			t.Fatal("history recovery submitted another apply")
		}
		check(do("GET", base+"/"+id, reader, nil), 404)
		return
	}
	if validateDrift {
		validationPath := base + "/" + id + "/validate"
		body := map[string]string{"digest": current.Plan.Digest}
		check(do("POST", validationPath, "", body), 401)
		check(do("POST", validationPath, appReader, body), 403)
		check(do("POST", validationPath, reader, body, map[string]string{"Origin": "https://customer.invalid"}), 403)
		check(do("POST", validationPath, reader, map[string]any{"digest": current.Plan.Digest, "scope": scope}), 400)
		check(do("POST", validationPath, reader, map[string]string{"digest": strings.Repeat("0", 64)}), 409)
		validate := func() datarepair.ValidationReport {
			t.Helper()
			response := do("POST", validationPath, reader, body)
			check(response, 200)
			var report datarepair.ValidationReport
			if response.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(response.Body.Bytes(), &report) != nil || !report.Valid() || report.Scope != scope {
				t.Fatal("invalid or cacheable hosted validation report")
			}
			return report
		}
		beforeOperation, _ := json.Marshal(current.Operation)
		beforePlan, _ := json.Marshal(current.Plan)
		fresh := validate()
		if !fresh.Matches || fresh.Checked != 460 || fresh.Unchanged != 460 || fresh.Excluded != 40 {
			t.Fatal("fresh plan did not match", fresh)
		}
		if _, err := pool.Exec(ctx, `UPDATE products SET category='human' WHERE id=41`); err != nil {
			t.Fatal(err)
		}
		drift := validate()
		if drift.Matches || drift.Changed != 1 || drift.Unchanged != 459 || drift.Excluded != 40 || len(drift.Rows) != 1 || drift.Rows[0].KeyJSON != `{"id": 41}` {
			t.Fatal("hosted validation lost the later edit", drift)
		}
		saved, err := store.OperationByID(ctx, acct.ID, "", id)
		if err != nil {
			t.Fatal(err)
		}
		afterOperation, _ := json.Marshal(saved.OperationResponse)
		plan, err := engine.Get(ctx, scope, id)
		afterPlan, _ := json.Marshal(plan)
		if err != nil || !bytes.Equal(beforeOperation, afterOperation) || !bytes.Equal(beforePlan, afterPlan) {
			t.Fatal("validation changed intent, approval, or the frozen plan", err)
		}
		if cli := os.Getenv("GREGALE_DATA_REPAIR_CLI"); cli != "" {
			apiServer := httptest.NewServer(handler)
			defer apiServer.Close()
			cmd := exec.CommandContext(ctx, cli, "data-repair", "validate", id, "--app", app.Slug, "--digest", current.Plan.Digest)
			cmd.Env = append(os.Environ(), "FAAS_API="+apiServer.URL, "FAAS_TOKEN="+reader)
			output, err := cmd.CombinedOutput()
			var report datarepair.ValidationReport
			if err == nil || json.Unmarshal(output, &report) != nil || !report.Valid() || report.Matches || report.Changed != 1 {
				t.Fatalf("hosted validation CLI: %s %v", output, err)
			}
		}
		failBeforeApply.Store(false)
		loseApply.Store(false)
		check(action("apply"), 503)
		var bad, human, restored, receipts int
		if err := pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE category='bad'),count(*) FILTER(WHERE category='human'),count(*) FILTER(WHERE category='original'),(SELECT count(*) FROM gregale_repair.operations WHERE id=$1) FROM products`, id).Scan(&bad, &human, &restored, &receipts); err != nil || bad != 459 || human != 41 || restored != 0 || receipts != 0 {
			t.Fatal("stale hosted apply committed a partial correction", bad, human, restored, receipts, err)
		}
		return
	}
	w = action("apply")
	check(w, 503)
	load(do("GET", base+"/"+id, reader, nil))
	if current.Operation.State != api.OperationRequiresReconciliation || current.Plan.AppliedAt != nil {
		t.Fatal("missing receipt was misclassified")
	}
	check(do("POST", base+"/"+id+"/replace", manager, map[string]any{"digest": current.Plan.Digest, "expected_sequence": current.Operation.LatestSequence}, map[string]string{"Idempotency-Key": uuid.NewString()}), 409)
	load(action("reconcile"))
	if current.Operation.State != api.OperationRequiresReconciliation || applyCalls.Load() != 1 {
		t.Fatal("reconciliation inferred rollback or retried writes")
	}
	w = action("apply")
	check(w, 503)
	if !strings.Contains(w.Body.String(), "repair_outcome_unknown") {
		t.Fatalf("lost commit result: %s", w.Body.String())
	}
	load(do("GET", base+"/"+id, reader, nil))
	if current.Operation.State != api.OperationRequiresReconciliation || current.Plan.AppliedAt == nil {
		t.Fatal("lost commit confused the control plane and database receipt")
	}
	if named && (current.Plan.ApplyChecks == nil || !current.Plan.ApplyChecks.Valid(true)) {
		t.Fatal("live named checks missing from commit receipt")
	}
	if r := verify(); !r.Available || r.Unchanged != 460 || current.Operation.DataRepair.Phase != api.DataRepairUnknown {
		t.Fatal("verification settled an unknown CP outcome or lost the source receipt", r)
	}
	historyBefore, _ := store.OperationByID(ctx, acct.ID, "", id)
	historyResponse := do("GET", sourcesURL+"/"+source.ID+"/history", reader, nil)
	check(historyResponse, 200)
	var committedHistory datarepair.HistoryPage
	if json.Unmarshal(historyResponse.Body.Bytes(), &committedHistory) != nil || len(committedHistory.Items) != 1 || committedHistory.Items[0].AppliedAt == nil || committedHistory.Items[0].Execution == nil || committedHistory.Items[0].Execution.Phase != api.DataRepairUnknown {
		t.Fatal("history conflated the committed source receipt with hosted execution status")
	}
	historyAfter, _ := store.OperationByID(ctx, acct.ID, "", id)
	beforeHistoryJSON, _ := json.Marshal(historyBefore)
	afterHistoryJSON, _ := json.Marshal(historyAfter)
	if !bytes.Equal(beforeHistoryJSON, afterHistoryJSON) {
		t.Fatal("reading history reconciled or rewrote a hosted operation")
	}
	// A durable application receipt may outlive its row payload while the
	// control plane still has an unknown outcome. Only this private fixture is
	// backdated; reconciliation must consume the compact receipt without apply.
	if _, err = pool.Exec(ctx, `BEGIN;
ALTER TABLE gregale_repair.operations DISABLE TRIGGER gregale_repair_protect_operation;
UPDATE gregale_repair.operations SET created_at=clock_timestamp()-interval '10 days';
ALTER TABLE gregale_repair.operations ENABLE TRIGGER gregale_repair_protect_operation;
UPDATE gregale_repair.plans SET created_at=clock_timestamp()-interval '10 days',expires_at=clock_timestamp()-interval '9 days',applied_at=clock_timestamp()-interval '8 days';
COMMIT;`); err != nil {
		t.Fatal(err)
	}
	page = discover("?limit=1")
	if len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatal("hosted source pagination omitted its continuation")
	}
	cursorBeforePurge, firstSourceID := page.NextCursor, page.Items[0].ID
	for range 2 { // Compact the plan, then its source and inverse journals.
		batch, e := engine.PlanRetention(ctx, scope, time.Time{}, api.DataRepairPurgeMaxItems)
		if e != nil || len(batch.Entries) == 0 {
			t.Fatalf("retention inventory: %+v %v", batch, e)
		}
		if _, e = engine.PurgeRetention(ctx, scope, batch.ID, batch.Digest, "maintenance-operator"); e != nil {
			t.Fatal(e)
		}
		r := verify()
		if r.Available && r.Unchanged != 460 || !r.Available && (len(r.Issues) != 1 || r.Issues[0] != "journal_purged") {
			t.Fatal("verification lost the distinction between plan and journal compaction", r)
		}
	}
	if r := verify(); r.Available || r.Checked != 0 || r.Restored != 460 || r.Excluded != 40 {
		t.Fatal("purged journal reported current observations", r)
	}
	check(do("POST", current.StatusURL+"/recover", admin, api.OperationRecoveryRequest{ExpectedGeneration: 1, Resolution: "safe_to_retry", RecoveryID: "cannot-reexecute"}), 409)
	load(action("resolve"))
	if current.Plan.ApplyResolution != nil {
		t.Fatal("lost commit response was incorrectly closed without a commit")
	}
	load(action("resolve")) // idempotent receipt resolution
	if current.Operation.State != api.OperationSucceeded || current.Plan.PayloadPurgedAt == nil || len(current.Plan.Items) != 0 || current.Plan.Candidates != 460 || current.Plan.Conflicts != 40 {
		t.Fatal("compact receipt did not settle operation")
	}
	load(action("apply"))
	if applyCalls.Load() != 2 {
		t.Fatal("receipt replay called apply again")
	}
	page = discover("?release=buggy-import-v1")
	if len(page.Items) != 1 || page.Items[0].ChangeRows != nil || page.Items[0].CanPrepare || page.Items[0].PayloadPurgedAt == nil || page.Items[0].LatestRepair == nil || page.Items[0].LatestRepair.OperationID != id {
		t.Fatalf("purged source discovery lost its receipt or advertised payload: %+v", page)
	}
	// The boundary remains valid after retention; capture identities are retained.
	page = discover("?limit=1&cursor=" + url.QueryEscape(cursorBeforePurge))
	if len(page.Items) != 1 || page.Items[0].ID == firstSourceID || page.NextCursor != "" {
		t.Fatal("retention invalidated the source continuation")
	}
	var restored, preserved, journal int
	if err = pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE category='original'),count(*) FILTER(WHERE category='human') FROM products`).Scan(&restored, &preserved); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM gregale_repair.changes WHERE operation_id=$1`, id).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if restored != 460 || preserved != 40 || journal != 0 {
		t.Fatalf("data: restored=%d preserved=%d journal=%d", restored, preserved, journal)
	}
	load(create())
	timeline, timelineURL := "", current.TimelineURL
	seenCursors := map[string]bool{}
	for timelineURL != "" {
		response := do("GET", timelineURL, admin, nil)
		check(response, 200)
		timeline += response.Body.String()
		var history api.OperationTimelineResponse
		if err := json.Unmarshal(response.Body.Bytes(), &history); err != nil || history.OperationID != id {
			t.Fatal("invalid repair timeline page", err)
		}
		timelineURL = ""
		if history.NextCursor != "" {
			if seenCursors[history.NextCursor] {
				t.Fatal("repair timeline repeated its continuation")
			}
			seenCursors[history.NextCursor] = true
			timelineURL = current.TimelineURL + "?cursor=" + url.QueryEscape(history.NextCursor)
		}
	}
	if !strings.Contains(timeline, "approval_decided") || !strings.Contains(timeline, "reconciliation_required") || !strings.Contains(timeline, "succeeded") {
		t.Fatal("timeline lost repair lifecycle")
	}
	if strings.Contains(timeline, `"category"`) {
		t.Fatal("row values leaked to Operations timeline")
	}
	check(do("GET", current.ReviewURL, reader, nil), 200)
	if cli := os.Getenv("GREGALE_DATA_REPAIR_CLI"); cli != "" {
		apiServer := httptest.NewServer(handler)
		defer apiServer.Close()
		cmd := exec.CommandContext(ctx, cli, "data-repair", "get", id, "--app", app.Slug)
		cmd.Env = append(os.Environ(), "FAAS_API="+apiServer.URL, "FAAS_TOKEN="+admin)
		output, e := cmd.CombinedOutput()
		if e != nil || !bytes.Contains(output, []byte(`"phase": "succeeded"`)) {
			t.Fatalf("hosted CLI: %s %v", output, e)
		}
		sourceCommand := exec.CommandContext(ctx, cli, "data-repair", "sources", "--app", app.Slug,
			"--environment", scope.Environment, "--customer", scope.PlatformTenantID, "--release", "buggy-import-v1")
		sourceCommand.Env = append(os.Environ(), "FAAS_API="+apiServer.URL, "FAAS_TOKEN="+reader)
		output, e = sourceCommand.CombinedOutput()
		var inventory datarepair.SourcePage
		if e != nil || json.Unmarshal(output, &inventory) != nil || len(inventory.Items) != 1 || inventory.Items[0].ID != source.ID || inventory.Items[0].CanPrepare || inventory.Items[0].LatestRepair == nil || inventory.Items[0].LatestRepair.OperationID != id {
			t.Fatalf("hosted CLI discovery lost the retained receipt: %s %v", output, e)
		}
		doctorCommand := exec.CommandContext(ctx, cli, "data-repair", "doctor", "--app", app.Slug,
			"--environment", scope.Environment, "--customer", scope.PlatformTenantID, "--table", "public.products")
		doctorCommand.Env = append(os.Environ(), "FAAS_API="+apiServer.URL, "FAAS_TOKEN="+reader)
		output, e = doctorCommand.Output()
		var readiness datarepair.DoctorReport
		if json.Unmarshal(output, &readiness) != nil || !readiness.Valid() || readiness.Scope != scope || len(readiness.Findings) != 10 ||
			(e == nil) != (readiness.Status == "ready") {
			t.Fatalf("hosted reader CLI lost readiness, scope or exit status: %s %v", output, e)
		}
	}
	changed := binding
	changed.Identity = strings.Repeat("f", 64)
	srv.WithDataRepairBindings([]repairapi.Binding{changed})
	check(do("GET", sourcesURL+"?limit=1&cursor="+url.QueryEscape(cursorBeforePurge), reader, nil), 400)
	check(do("GET", base+"/"+id, reader, nil), 503)
}

// This portable backend exercises orchestration and optionally the real guest
// process in an isolated container. It does not claim native KVM acceptance.
type managedRepairVMFixture struct {
	incarnation, helper          string
	runs                         int
	loseResponse, failRetirement bool
	lastError                    error
	failChecks                   bool
}

func (v *managedRepairVMFixture) DataRepairRehearsalIdentity(context.Context, string, datarepair.RunnerSpec) (string, error) {
	return v.incarnation, nil
}
func (v *managedRepairVMFixture) RetireDataRepairRehearsal(context.Context, datarepair.RehearsalFrame) error {
	if v.failRetirement {
		return datarepair.ErrUnavailable
	}
	return nil
}
func (v *managedRepairVMFixture) RunDataRepairRehearsal(ctx context.Context, f datarepair.RehearsalFrame, in datarepair.RehearsalInput) (datarepair.Evidence, error) {
	v.runs++
	if !f.Matches(in.Plan) || in.Validate() != nil {
		return datarepair.Evidence{}, datarepair.ErrInvalid
	}
	evidence := datarepair.Evidence{Passed: !v.failChecks, PlanDigest: f.PlanDigest, SnapshotSHA256: f.SnapshotSHA256, ChecksSHA256: f.ChecksSHA256, Runner: datarepair.MicroVMRunner}
	if len(in.Plan.Checks) != 0 {
		evidence.CheckResults = fixtureCheckReport(in.Plan, v.failChecks)
	}
	if v.helper != "" {
		var err error
		evidence, err = runRepairGuestFixture(ctx, v.helper, in)
		if err != nil {
			v.lastError = err
			return datarepair.Evidence{}, err
		}
	}
	if v.loseResponse {
		v.loseResponse = false
		return datarepair.Evidence{}, datarepair.ErrUnavailable
	}
	at := time.Now().UTC()
	evidence.TaskID, evidence.NodeID, evidence.RunnerSHA256, evidence.RetiredAt = f.TaskID, f.NodeID, f.Runner.Digest(), &at
	return evidence, nil
}

func fixtureCheckReport(p datarepair.Plan, failed bool) *datarepair.CheckReport {
	report := &datarepair.CheckReport{}
	for i, declaration := range p.Checks {
		status := "passed"
		if failed && i == len(p.Checks)-1 {
			status = "failed"
		}
		report.Results = append(report.Results, datarepair.CheckResult{Name: declaration.Name, SQLSHA256: declaration.SQLSHA256, Status: status})
	}
	return report
}

// Narrow task-store fault injection; no replacement state machine or Store wrappers.
type repairClaimLoss struct {
	state.DataRepairRehearsalStore
	failed, failRead bool
}

func (s *repairClaimLoss) ClaimDataRepairRehearsal(ctx context.Context, id string, f datarepair.RehearsalFrame) (state.DataRepairRehearsal, error) {
	if !s.failed {
		s.failed = true
		s.failRead = true
		return state.DataRepairRehearsal{}, datarepair.ErrUnavailable
	}
	return s.DataRepairRehearsalStore.ClaimDataRepairRehearsal(ctx, id, f)
}
func (s *repairClaimLoss) DataRepairRehearsal(ctx context.Context, id string) (state.DataRepairRehearsal, error) {
	if s.failRead {
		s.failRead = false
		return state.DataRepairRehearsal{}, datarepair.ErrUnavailable
	}
	return s.DataRepairRehearsalStore.DataRepairRehearsal(ctx, id)
}
