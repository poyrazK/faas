//go:build !no_pg

// adr: 20261019001500
package apidserver_test

// Approvals with input: forms are hidden until the account is allowlisted;
// a decision's input is validated against the step's form inside the
// decision, replays exactly, and reaches the workflow in the callback payload.

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type approvalInputFixture struct {
	t        *testing.T
	store    state.Store
	handler  http.Handler
	account  state.Account
	key      string
	token    string
	op       api.OperationAcceptedResponse
	approval string // customer approval route for the review step
}

func newApprovalInputFixture(t *testing.T, kind string) *approvalInputFixture {
	t.Helper()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_OPERATION_APPROVAL_INPUTS_ACCOUNTS", "")
	ctx := t.Context()
	var store state.Store = state.NewMemStore()
	if kind == "postgres" {
		pool := pgtest.OpenMigrated(t)
		if err := db.MigrateUp(ctx, pool); err != nil {
			t.Fatal(err)
		}
		store = state.NewPgStore(pool)
	}
	f := &approvalInputFixture{t: t, store: store}
	var err error
	if f.account, err = store.CreateAccount(ctx, "approval-inputs@example.com", api.PlanPro); err != nil {
		t.Fatal(err)
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(ctx, f.account.ID, hash, "approval-inputs", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	f.key = key
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	(*srv.Operation()).AdmissionEnabled, (*srv.WorkflowRuntimeEnabled()) = true, true
	f.handler = srv.Handler()
	app, err := store.CreateApp(ctx, state.App{AccountID: f.account.ID, Slug: "refunds", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	workflow := api.WorkflowSpec{Name: "refund-chain", Steps: []api.WorkflowStepSpec{
		{Name: "validate", Path: "/validate"},
		{Name: "review", WaitForCallback: true, Timeout: time.Hour, DependsOn: []string{"validate"}},
		{Name: "finance", WaitForCallback: true, Timeout: time.Hour, DependsOn: []string{"review"}},
		{Name: "refund", Path: "/refund", DependsOn: []string{"finance"}},
	}}
	workflows, _ := json.Marshal([]api.WorkflowSpec{workflow})
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Workflows: workflows})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	spec := api.OperationDefinitionSpec{Name: "refund", Workflow: workflow.Name, Method: "POST", Path: "/refunds", Owner: api.OperationOwnerPlatformTenant,
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
		ProgressStages: []string{"validate", "review", "finance", "refund"},
		Approvals: []api.OperationApprovalSpec{{Step: "review", Approver: api.OperationApproverCustomer}, {Step: "finance", Approver: api.OperationApproverAccount}}}
	var def api.OperationDefinitionResponse
	f.decode(f.do("PUT", "/v1/apps/refunds/deployments/"+dep.ID+"/operation-definitions/refund", f.key, spec), http.StatusOK, &def)
	tenant, _, err := store.(state.PlatformTenantStore).CreatePlatformTenant(ctx, f.account.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	// Customer workflow callbacks need the customer's surface linked to the app.
	surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: f.account.ID, AppID: app.ID, Name: "alice", CertKind: state.CertKindPerHostSAN}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.(state.PlatformTenantStore).LinkPlatformTenantSurface(ctx, f.account.ID, tenant.ID, surface.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	var token api.CreatePlatformTenantAccessTokenResponse
	f.decode(f.do("POST", "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", f.key, api.CreatePlatformTenantAccessTokenRequest{Name: "browser",
		Scopes: []string{api.ScopePlatformTenantOperationsRead, api.ScopePlatformTenantOperationsManage}}), http.StatusCreated, &token)
	f.token = token.Token
	req := httptest.NewRequest("POST", "/v1/platform-tenant-self/customer-operations", bytes.NewReader(mustJSON(t, api.OperationStartRequest{DefinitionID: def.ID, Input: json.RawMessage(`{}`)})))
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("Idempotency-Key", "refund-1")
	started := httptest.NewRecorder()
	f.handler.ServeHTTP(started, req)
	f.decode(started, http.StatusAccepted, &f.op)
	// Advance the run to its approval exactly as the scheduler would.
	run, err := store.ClaimNextDueWorkflowRun(ctx)
	if err != nil || run == nil {
		t.Fatalf("claim operation run: %+v %v", run, err)
	}
	if err := store.MarkWorkflowStepStatus(ctx, run.ID, "validate", state.WorkflowStepStatusSucceeded, 1, json.RawMessage(`{"ok":true}`), nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ParkWorkflowEvent(ctx, run.ID, "review", api.WorkflowCallbackEventName(run.ID, "review"), time.Hour); err != nil {
		t.Fatal(err)
	}
	f.approval = "/v1/platform-tenant-self/customer-operations/" + f.op.ID + "/approvals/review"
	return f
}

func (f *approvalInputFixture) do(method, path, bearer string, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(mustJSON(f.t, body))
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func (f *approvalInputFixture) decode(rec *httptest.ResponseRecorder, status int, out any) {
	f.t.Helper()
	if rec.Code != status {
		f.t.Fatalf("HTTP %d, want %d: %s", rec.Code, status, rec.Body.String())
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			f.t.Fatal(err)
		}
	}
}

func forEachApprovalInputStore(t *testing.T, test func(t *testing.T, f *approvalInputFixture)) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) { test(t, newApprovalInputFixture(t, kind)) })
	}
}

var refundForm = json.RawMessage(`{"type":"object","required":["amount_cents"],"properties":{"amount_cents":{"type":"integer","minimum":1},"reason":{"type":"string"}},"additionalProperties":false}`)

func TestOperationApprovalFormsAreHiddenUntilAllowlisted(t *testing.T) {
	forEachApprovalInputStore(t, func(t *testing.T, f *approvalInputFixture) {
		form := api.OperationApprovalFormRequest{Operation: "refund", Step: "review", InputSchema: refundForm}
		for _, rec := range []*httptest.ResponseRecorder{
			f.do("PUT", "/v1/apps/refunds/operation-approval-forms", f.key, form),
			f.do("GET", "/v1/apps/refunds/operation-approval-forms", f.key, nil),
			f.do("DELETE", "/v1/apps/refunds/operation-approval-forms/00000000-0000-0000-0000-000000000000", f.key, nil),
			f.do("GET", "/v1/apps/refunds/operations/"+f.op.ID+"/approvals/review/form", f.key, nil),
			f.do("GET", f.approval+"/form", f.token, nil),
		} {
			if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "no such API route") {
				t.Fatalf("before the allowlist: HTTP %d %s", rec.Code, rec.Body.String())
			}
		}
		// Input is an unknown field outside the rollout; a plain decision works.
		withInput := api.OperationApprovalInputRequest{ExpectedGeneration: 1, Decision: api.OperationApprovalApproved, Input: json.RawMessage(`{"amount_cents":5}`)}
		f.decode(f.do("POST", f.approval, f.token, withInput), http.StatusBadRequest, nil)
		f.decode(f.do("POST", f.approval, f.token, api.OperationApprovalRequest{ExpectedGeneration: 1, Decision: api.OperationApprovalApproved}), http.StatusOK, nil)
	})
}

func TestOperationApprovalInputIsValidatedReplayedAndDelivered(t *testing.T) {
	forEachApprovalInputStore(t, func(t *testing.T, f *approvalInputFixture) {
		t.Setenv("FAAS_OPERATION_APPROVAL_INPUTS_ACCOUNTS", f.account.ID)
		forms := "/v1/apps/refunds/operation-approval-forms"
		f.decode(f.do("PUT", forms, f.key, api.OperationApprovalFormRequest{Operation: "refund", Step: "review", InputSchema: json.RawMessage(`{"$ref":"https://evil.example/schema"}`)}), http.StatusBadRequest, nil)
		var form api.OperationApprovalForm
		f.decode(f.do("PUT", forms, f.key, api.OperationApprovalFormRequest{Operation: "refund", Step: "review", InputSchema: json.RawMessage(`{"type":"object"}`)}), http.StatusCreated, &form)
		f.decode(f.do("PUT", forms, f.key, api.OperationApprovalFormRequest{Operation: "refund", Step: "review", InputSchema: refundForm, Required: true}), http.StatusOK, &form)
		var list api.OperationApprovalFormList
		f.decode(f.do("GET", forms, f.key, nil), http.StatusOK, &list)
		if len(list.Forms) != 1 || list.Forms[0].ID != form.ID || !list.Forms[0].Required {
			t.Fatalf("forms = %+v", list)
		}

		// The customer reads its pending form; the account's step stays private.
		var view api.OperationApprovalFormView
		f.decode(f.do("GET", f.approval+"/form", f.token, nil), http.StatusOK, &view)
		if view.Step != "review" || view.Approver != api.OperationApproverCustomer || !view.Required || view.Decision != nil {
			t.Fatalf("pending form = %+v", view)
		}
		f.decode(f.do("GET", "/v1/platform-tenant-self/customer-operations/"+f.op.ID+"/approvals/finance/form", f.token, nil), http.StatusNotFound, nil)

		decide := func(decision string, input string) *httptest.ResponseRecorder {
			req := api.OperationApprovalInputRequest{ExpectedGeneration: 1, Decision: decision}
			if input != "" {
				req.Input = json.RawMessage(input)
			}
			return f.do("POST", f.approval, f.token, req)
		}
		f.decode(decide(api.OperationApprovalApproved, ""), http.StatusBadRequest, nil)
		f.decode(decide(api.OperationApprovalApproved, `{"amount_cents":0}`), http.StatusBadRequest, nil)
		f.decode(decide(api.OperationApprovalApproved, `[1]`), http.StatusBadRequest, nil)
		f.decode(decide(api.OperationApprovalApproved, `{"amount_cents":1250,"reason":"damaged"}`), http.StatusOK, nil)
		f.decode(decide(api.OperationApprovalApproved, `{"reason":"damaged","amount_cents":1250}`), http.StatusOK, nil)
		f.decode(decide(api.OperationApprovalApproved, `{"amount_cents":999}`), http.StatusConflict, nil)

		f.decode(f.do("GET", "/v1/apps/refunds/operations/"+f.op.ID+"/approvals/review/form", f.key, nil), http.StatusOK, &view)
		var input struct {
			AmountCents int    `json:"amount_cents"`
			Reason      string `json:"reason"`
		}
		if view.Decision == nil || view.Decision.Decision != api.OperationApprovalApproved || view.Decision.Actor.Kind != api.OperationApproverCustomer ||
			json.Unmarshal(view.Decision.Input, &input) != nil || input.AmountCents != 1250 || input.Reason != "damaged" {
			t.Fatalf("decided form = %+v", view)
		}

		f.decode(f.do("DELETE", forms+"/"+form.ID, f.key, nil), http.StatusNoContent, nil)
		f.decode(f.do("GET", f.approval+"/form", f.token, nil), http.StatusNotFound, nil)
	})
}
