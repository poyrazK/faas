// adr: 521
package apidserver_test

// Suspending a customer closes its own Operation reads, events and downloads
// at once; the owning account still reads every outcome, and resuming the
// customer reopens its reads.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPlatformTenantSelfOperationReadsCloseWhileSuspended(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "operation-tenant-suspension@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "operation-tenant-suspension", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	(*srv.Operation()).AdmissionEnabled, (*srv.WorkflowRuntimeEnabled()) = true, true
	handler := srv.Handler()
	do := func(method, path, bearer string, body any, idempotency string) *httptest.ResponseRecorder {
		t.Helper()
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(mustJSON(t, body))
		}
		req := httptest.NewRequest(method, path, reader)
		req.Header.Set("Authorization", "Bearer "+bearer)
		if idempotency != "" {
			req.Header.Set("Idempotency-Key", idempotency)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	check := func(rec *httptest.ResponseRecorder, status int, label string) {
		t.Helper()
		if rec.Code != status {
			t.Fatalf("%s: HTTP %d, want %d: %s", label, rec.Code, status, rec.Body.String())
		}
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "orders", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	workflow := api.WorkflowSpec{Name: "order-chain", Steps: []api.WorkflowStepSpec{
		{Name: "validate", Path: "/validate"},
		{Name: "finish", Path: "/finish", DependsOn: []string{"validate"}},
	}}
	workflows, _ := json.Marshal([]api.WorkflowSpec{workflow})
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Workflows: workflows})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	spec := api.OperationDefinitionSpec{Name: "fulfil", Workflow: workflow.Name, Method: "POST", Path: "/orders", Owner: api.OperationOwnerPlatformTenant,
		InputSchema:    json.RawMessage(`{"type":"object","required":["order_id"],"properties":{"order_id":{"type":"string"}},"additionalProperties":false}`),
		OutputSchema:   json.RawMessage(`{"type":"object"}`),
		ProgressStages: []string{"validate", "finish"}}
	defined := do("PUT", "/v1/apps/orders/deployments/"+dep.ID+"/operation-definitions/fulfil", key, spec, "")
	check(defined, http.StatusOK, "define")
	var def api.OperationDefinitionResponse
	if err := json.Unmarshal(defined.Body.Bytes(), &def); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: acct.ID, AppID: app.ID, Name: "alice", CertKind: state.CertKindPerHostSAN}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantSurface(ctx, acct.ID, tenant.ID, surface.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	minted := do("POST", "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", key, api.CreatePlatformTenantAccessTokenRequest{Name: "browser", Scopes: []string{api.ScopePlatformTenantOperationsRead, api.ScopePlatformTenantOperationsManage}}, "")
	check(minted, http.StatusCreated, "mint")
	var token api.CreatePlatformTenantAccessTokenResponse
	if err := json.Unmarshal(minted.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	started := do("POST", "/v1/platform-tenant-self/customer-operations", token.Token, api.OperationStartRequest{DefinitionID: def.ID, Input: json.RawMessage(`{"order_id":"ord_1"}`)}, "order-1")
	check(started, http.StatusAccepted, "start")
	var receipt api.OperationAcceptedResponse
	if err := json.Unmarshal(started.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}

	self := "/v1/platform-tenant-self/customer-operations"
	reads := []struct{ method, path string }{
		{"GET", self + "/" + receipt.ID},
		{"GET", self + "/" + receipt.ID + "/events"},
		{"GET", self + "/" + receipt.ID + "/milestones"},
		{"GET", self + "/" + receipt.ID + "/artifacts/export-file"},
		{"GET", self},
		{"GET", self + "/identity"},
		{"GET", "/v1/platform-tenant-self/customer-operation-milestones"},
	}
	for _, read := range reads {
		if rec := do(read.method, read.path, token.Token, nil, ""); rec.Code == http.StatusForbidden {
			t.Fatalf("active customer refused on %s: %s", read.path, rec.Body.String())
		}
	}
	lookup := api.OperationSubmissionLookupRequest{AppID: app.ID, Scope: dep.Scope, Name: "fulfil", IdempotencyKey: "order-1"}
	if rec := do("POST", self+"/submissions/lookup", token.Token, lookup, ""); rec.Code == http.StatusForbidden {
		t.Fatalf("active customer refused on lookup: %s", rec.Body.String())
	}

	// An open event stream closes at its next credential recheck.
	streamServer := httptest.NewServer(handler)
	defer streamServer.Close()
	streamReq, _ := http.NewRequest("GET", streamServer.URL+receipt.EventsURL, nil)
	streamReq.Header.Set("Authorization", "Bearer "+token.Token)
	streamReq.Header.Set("Accept", "text/event-stream")
	stream, err := (&http.Client{Timeout: 10 * time.Second}).Do(streamReq)
	if err != nil || stream.StatusCode != http.StatusOK {
		t.Fatalf("open stream: %v %+v", err, stream)
	}
	defer stream.Body.Close()
	frames := bufio.NewScanner(stream.Body)

	if _, err := store.SetPlatformTenantStatus(ctx, acct.ID, tenant.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	closed := false
	for frames.Scan() {
		if frames.Text() == "event: auth_expired" {
			closed = true
			break
		}
	}
	if !closed {
		t.Fatalf("live stream outlived the suspension: %v", frames.Err())
	}
	for _, read := range reads {
		check(do(read.method, read.path, token.Token, nil, ""), http.StatusForbidden, "suspended "+read.path)
	}
	check(do("POST", self+"/submissions/lookup", token.Token, lookup, ""), http.StatusForbidden, "suspended lookup")
	check(do("GET", "/v1/apps/orders/operations/"+receipt.ID, key, nil, ""), http.StatusOK, "account read while suspended")

	if _, err := store.SetPlatformTenantStatus(ctx, acct.ID, tenant.ID, state.PlatformTenantActive); err != nil {
		t.Fatal(err)
	}
	check(do("GET", self+"/"+receipt.ID, token.Token, nil, ""), http.StatusOK, "read after resume")
}
