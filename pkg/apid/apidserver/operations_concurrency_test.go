// adr: 20261018210500
package apidserver_test

// Per-customer Operation concurrency: hidden until the account is
// allowlisted; the account manages policies; customers see their queue; the
// releaser starts held work, workflow Operations included.

import (
	"bytes"
	"context"
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
	"github.com/onebox-faas/faas/pkg/state"
)

type concurrencyRoutesFixture struct {
	t        *testing.T
	store    *state.MemStore
	handler  http.Handler
	acct     state.Account
	key      string
	def      api.OperationDefinitionResponse
	tokens   []string
	accounts string
}

func newConcurrencyRoutesFixture(t *testing.T) *concurrencyRoutesFixture {
	t.Helper()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_OPERATION_CONCURRENCY_ACCOUNTS", "")
	ctx := context.Background()
	f := &concurrencyRoutesFixture{t: t, store: state.NewMemStore()}
	var err error
	if f.acct, err = f.store.CreateAccount(ctx, "operation-concurrency@example.com", api.PlanPro); err != nil {
		t.Fatal(err)
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := f.store.CreateAPIKey(ctx, f.acct.ID, hash, "operation-concurrency", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	f.key, f.accounts = key, f.acct.ID
	srv := apidserver.NewServer(f.store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	(*srv.Operation()).AdmissionEnabled, (*srv.WorkflowRuntimeEnabled()) = true, true
	f.handler = srv.Handler()
	app, err := f.store.CreateApp(ctx, state.App{AccountID: f.acct.ID, Slug: "orders", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	workflow := api.WorkflowSpec{Name: "fulfil-chain", Steps: []api.WorkflowStepSpec{{Name: "fulfil", Path: "/fulfil"}}}
	workflows, _ := json.Marshal([]api.WorkflowSpec{workflow})
	dep, err := f.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Workflows: workflows})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	spec := api.OperationDefinitionSpec{Name: "fulfil", Workflow: workflow.Name, Method: "POST", Path: "/orders", Owner: api.OperationOwnerPlatformTenant,
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`), ProgressStages: []string{"fulfil"}}
	f.decode(f.do("PUT", "/v1/apps/orders/deployments/"+dep.ID+"/operation-definitions/fulfil", f.key, spec), http.StatusOK, &f.def)
	for _, ref := range []string{"alice", "bob"} {
		tenant, _, err := f.store.CreatePlatformTenant(ctx, f.acct.ID, ref, ref, 100)
		if err != nil {
			t.Fatal(err)
		}
		var token api.CreatePlatformTenantAccessTokenResponse
		f.decode(f.do("POST", "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", f.key, api.CreatePlatformTenantAccessTokenRequest{Name: "browser",
			Scopes: []string{api.ScopePlatformTenantOperationsRead, api.ScopePlatformTenantOperationsManage}}), http.StatusCreated, &token)
		f.tokens = append(f.tokens, token.Token)
	}
	return f
}

func (f *concurrencyRoutesFixture) do(method, path, bearer string, body any) *httptest.ResponseRecorder {
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

func (f *concurrencyRoutesFixture) decode(rec *httptest.ResponseRecorder, status int, out any) {
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

func (f *concurrencyRoutesFixture) start(token, key string) api.OperationAcceptedResponse {
	f.t.Helper()
	req := httptest.NewRequest("POST", "/v1/platform-tenant-self/customer-operations", bytes.NewReader(mustJSON(f.t, api.OperationStartRequest{DefinitionID: f.def.ID, Input: json.RawMessage(`{}`)})))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	var out api.OperationAcceptedResponse
	f.decode(rec, http.StatusAccepted, &out)
	return out
}

// runDue reports whether the Operation's workflow run is due to start.
func (f *concurrencyRoutesFixture) runDue(id string) bool {
	f.t.Helper()
	op, err := f.store.OperationByID(context.Background(), f.acct.ID, "", id)
	if err != nil {
		f.t.Fatal(err)
	}
	run, err := f.store.GetWorkflowRun(context.Background(), op.WorkflowRunID)
	if err != nil {
		f.t.Fatal(err)
	}
	return !run.ScheduledFor.After(time.Now().Add(time.Hour))
}

func TestOperationConcurrencyRoutesAreHiddenUntilAllowlisted(t *testing.T) {
	f := newConcurrencyRoutesFixture(t)
	policy := api.OperationConcurrencyPolicyRequest{Operation: "fulfil", MaxRunning: 1}
	for _, rec := range []*httptest.ResponseRecorder{
		f.do("PUT", "/v1/apps/orders/operation-concurrency", f.key, policy),
		f.do("GET", "/v1/apps/orders/operation-concurrency", f.key, nil),
		f.do("DELETE", "/v1/apps/orders/operation-concurrency/00000000-0000-0000-0000-000000000000", f.key, nil),
		f.do("GET", "/v1/platform-tenant-self/customer-operation-queue", f.tokens[0], nil),
	} {
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "no such API route") {
			t.Fatalf("before the allowlist: HTTP %d %s", rec.Code, rec.Body.String())
		}
	}
	t.Setenv("FAAS_OPERATION_CONCURRENCY_ACCOUNTS", f.accounts)
	f.decode(f.do("PUT", "/v1/apps/orders/operation-concurrency", f.key, policy), http.StatusCreated, nil)
}

func TestOperationConcurrencyQueuesWorkflowOperationsPerCustomer(t *testing.T) {
	f := newConcurrencyRoutesFixture(t)
	t.Setenv("FAAS_OPERATION_CONCURRENCY_ACCOUNTS", f.accounts)
	alice, bob := f.tokens[0], f.tokens[1]
	var policy api.OperationConcurrencyPolicy
	f.decode(f.do("PUT", "/v1/apps/orders/operation-concurrency", f.key, api.OperationConcurrencyPolicyRequest{Operation: "fulfil", MaxRunning: 1}), http.StatusCreated, &policy)
	f.decode(f.do("PUT", "/v1/apps/orders/operation-concurrency", f.key, api.OperationConcurrencyPolicyRequest{Operation: "fulfil", MaxRunning: 0}), http.StatusBadRequest, nil)

	first, second := f.start(alice, "order-1"), f.start(alice, "order-2")
	other := f.start(bob, "order-9")
	if !f.runDue(first.ID) || f.runDue(second.ID) || !f.runDue(other.ID) {
		t.Fatalf("due: first=%v second=%v other=%v", f.runDue(first.ID), f.runDue(second.ID), f.runDue(other.ID))
	}
	var queue api.OperationQueueResponse
	f.decode(f.do("GET", "/v1/platform-tenant-self/customer-operation-queue", alice, nil), http.StatusOK, &queue)
	if len(queue.Queued) != 1 || queue.Queued[0].OperationID != second.ID || queue.Queued[0].Position != 1 || queue.Queued[0].Operation != "fulfil" {
		t.Fatalf("alice's queue = %+v", queue)
	}
	f.decode(f.do("GET", "/v1/platform-tenant-self/customer-operation-queue", bob, nil), http.StatusOK, &queue)
	if len(queue.Queued) != 0 {
		t.Fatalf("bob's queue = %+v", queue)
	}
	var view api.OperationConcurrencyResponse
	f.decode(f.do("GET", "/v1/apps/orders/operation-concurrency", f.key, nil), http.StatusOK, &view)
	if len(view.Policies) != 1 || len(view.Queues) != 1 || view.Queues[0].Queued != 1 || view.Queues[0].InFlight != 1 || view.Queues[0].Limit != 1 {
		t.Fatalf("account view = %+v", view)
	}

	// Removing the policy lets the releaser start the held run.
	f.decode(f.do("DELETE", "/v1/apps/orders/operation-concurrency/"+policy.ID, f.key, nil), http.StatusNoContent, nil)
	if n, err := f.store.ReleaseQueuedOperations(context.Background(), time.Now()); err != nil || n != 1 || !f.runDue(second.ID) {
		t.Fatalf("release after delete = %d %v", n, err)
	}
	f.decode(f.do("GET", "/v1/platform-tenant-self/customer-operation-queue", alice, nil), http.StatusOK, &queue)
	if len(queue.Queued) != 0 {
		t.Fatalf("queue after release = %+v", queue)
	}
}
