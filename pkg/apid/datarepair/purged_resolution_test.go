package datarepair_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	repairapi "github.com/onebox-faas/faas/pkg/apid/datarepair"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type purgedRepairHost struct {
	PgStore *state.PgStore
	App     state.App
}

func (h *purgedRepairHost) Store() state.Store                                         { return h.PgStore }
func (h *purgedRepairHost) EmitAudit(context.Context, string, *string, map[string]any) {}
func (h *purgedRepairHost) LoadApp(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool) {
	if acct.ID != h.App.AccountID || slug != h.App.Slug {
		http.NotFound(w, r)
		return state.App{}, false
	}
	return h.App, true
}

type purgedRepairRehearser struct{}

func (purgedRepairRehearser) Rehearse(_ context.Context, p core.Plan) (core.Evidence, error) {
	return core.Evidence{Passed: true, PlanDigest: p.Digest, ChecksSHA256: p.ChecksSHA256, SnapshotSHA256: strings.Repeat("a", 64), Runner: "test-only"}, nil
}

func TestManagedRepairPurgedPlanResolutionReleasesPendingQuota(t *testing.T) {
	ctx := t.Context()
	control := pgtest.OpenDatabase(t)
	if err := db.MigrateUp(ctx, control); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(control)
	acct, err := store.CreateAccount(ctx, "purged-repair-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "purged-repair", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:repair"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, "repair-customer", "Repair Customer", 100)
	if err != nil {
		t.Fatal(err)
	}
	def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: api.OperationDefinitionSpec{Name: "import", Method: "POST", Path: "/import", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"importing"}, InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	source, _, err := store.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: tenant.ID, IdempotencyKey: "source", Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CancelOperation(ctx, acct.ID, "", source.ID, source.Generation); err != nil {
		t.Fatal(err)
	}
	scope := core.Scope{AccountID: acct.ID, AppID: app.ID, Environment: dep.Scope, PlatformTenantID: tenant.ID}
	pool := pgtest.OpenDatabase(t)
	if _, err := pool.Exec(ctx, core.Schema()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE products(id int PRIMARY KEY,category text); SELECT gregale_repair.enroll('products',ARRAY['category']); INSERT INTO products(id,category) VALUES(1,'original')`); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.Capture(ctx, tx, scope, source.ID, "bad-import", "products", func(tx pgx.Tx) error { _, err := tx.Exec(ctx, `UPDATE products SET category='bad'`); return err }); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	engine := &core.Engine{Pool: pool, Rehearser: purgedRepairRehearser{}}
	token := strings.Repeat("t", 64)
	sourceHandler := (&repairapi.Handler{Engine: engine, Coordinator: true, Release: "repair-executor", Authenticate: repairapi.Bearer(token, repairapi.Principal{Scope: scope, Actor: acct.ID, CanApprove: true, CanApply: true})}).Routes()
	var failLookup atomic.Bool
	executor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failLookup.Load() && r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/data-repair/plans/") {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		sourceHandler.ServeHTTP(w, r)
	}))
	defer executor.Close()
	remote, err := repairapi.NewRemote(executor.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	host := &purgedRepairHost{PgStore: store, App: app}
	binding := repairapi.Binding{Scope: scope, Identity: repairapi.BindingIdentity(scope, uuid.NewString(), executor.URL), Executor: remote}
	managed := &repairapi.Managed{Host: host, Bindings: []repairapi.Binding{binding}}
	id := uuid.NewString()
	call := func(action string, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.SetPathValue("slug", app.Slug)
		r.SetPathValue("id", id)
		r.SetPathValue("action", action)
		w := httptest.NewRecorder()
		switch action {
		case "create":
			r.Header.Set("Idempotency-Key", id)
			managed.Create(w, r, acct)
		case "resolve":
			managed.Resolve(w, r, acct)
		default:
			managed.Action(w, r, acct)
		}
		return w
	}
	response := call("create", `{"source_operation_id":"`+source.ID+`"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("prepare: %d %s", response.Code, response.Body.String())
	}
	var prepared repairapi.ManagedResponse
	if err := json.Unmarshal(response.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	actionBody := func(sequence int64) string {
		raw, _ := json.Marshal(map[string]any{"digest": prepared.Plan.Digest, "expected_sequence": sequence})
		return string(raw)
	}
	response = call("rehearse", actionBody(prepared.Operation.LatestSequence))
	if response.Code != http.StatusOK {
		t.Fatalf("rehearse: %d %s", response.Code, response.Body.String())
	}
	op, err := store.OperationByID(ctx, acct.ID, "", id)
	if err != nil {
		t.Fatal(err)
	}
	op, err = store.DecideOperationApproval(ctx, state.OperationApprovalInput{AccountID: acct.ID, OperationID: id, Step: "repair", ApproverKind: api.OperationApproverAccount, ExpectedGeneration: op.Generation, Decision: api.OperationApprovalApproved, Actor: api.OperationApprovalActor{Kind: api.OperationApproverAccount, ID: acct.ID}})
	if err != nil {
		t.Fatal(err)
	}
	failLookup.Store(true)
	response = call("apply", actionBody(op.LatestSequence))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("lost source lookup: %d %s", response.Code, response.Body.String())
	}
	failLookup.Store(false)
	op, err = store.OperationByID(ctx, acct.ID, "", id)
	if err != nil || op.DataRepair.Phase != api.DataRepairUnknown {
		t.Fatalf("uncertain apply was not retained: %+v %v", op.DataRepair, err)
	}
	sourcePlan, err := engine.Get(ctx, scope, id)
	if err != nil || sourcePlan.ApprovedAt != nil || sourcePlan.AppliedAt != nil {
		t.Fatalf("failed lookup reached source approval/apply: %+v %v", sourcePlan, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE gregale_repair.plans SET created_at=clock_timestamp()-interval '10 days',expires_at=clock_timestamp()-interval '9 days' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	purge, err := engine.PlanRetention(ctx, scope, time.Time{}, api.DataRepairPurgeMaxItems)
	if err != nil || len(purge.Entries) != 1 || purge.Entries[0].ID != id {
		t.Fatalf("retention did not select the unapproved source plan: %+v %v", purge, err)
	}
	if _, err := engine.PurgeRetention(ctx, scope, purge.ID, purge.Digest, "retention-operator"); err != nil {
		t.Fatal(err)
	}
	if n, err := store.OperationPendingCount(ctx, acct.ID); err != nil || n != 1 {
		t.Fatalf("uncertain operation lost its reservation: %d %v", n, err)
	}
	request := actionBody(op.LatestSequence)
	response = call("resolve", request)
	if response.Code != http.StatusOK {
		t.Fatalf("purged outcome could not resolve: %d %s", response.Code, response.Body.String())
	}
	closed, err := store.OperationByID(ctx, acct.ID, "", id)
	if err != nil || closed.State != api.OperationFailed || closed.DataRepair.Phase != api.DataRepairClosed || closed.DataRepair.ApplyClosedAt == nil || closed.FailureCode != "repair_not_committed" {
		t.Fatalf("hosted result did not settle: %+v %v", closed.OperationResponse, err)
	}
	if n, err := store.OperationPendingCount(ctx, acct.ID); err != nil || n != 0 {
		t.Fatalf("closed operation still consumed pending quota: %d %v", n, err)
	}
	response = call("resolve", request) // A lost response replays the original sequence.
	replay, err := store.OperationByID(ctx, acct.ID, "", id)
	if response.Code != http.StatusOK || err != nil || replay.LatestSequence != closed.LatestSequence {
		t.Fatalf("resolution replay changed the hosted result: %d %v", response.Code, err)
	}
	var category string
	var version, receipts int
	if err := pool.QueryRow(ctx, `SELECT category,gregale_row_version,(SELECT count(*) FROM gregale_repair.operations WHERE id=$1) FROM products`, id).Scan(&category, &version, &receipts); err != nil || category != "bad" || version != 2 || receipts != 0 {
		t.Fatalf("metadata closure wrote business data: %s/%d/%d %v", category, version, receipts, err)
	}
}
