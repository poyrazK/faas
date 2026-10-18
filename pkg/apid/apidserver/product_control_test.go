package apidserver_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlInternalBoundary(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "product-http@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "owner", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "product-http", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:product"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"generating"}, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	handler := srv.Handler()
	do := func(method, path, bearer string, body any, keys ...string) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		r.Header.Set("Idempotency-Key", "same-work")
		if len(keys) > 0 {
			r.Header.Set("Idempotency-Key", keys[0])
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	check := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("got %d want %d: %s", w.Code, status, w.Body.String())
		}
	}
	for _, path := range []string{"/v1/apps/" + app.Slug + "/product", "/v1/apps/" + app.Slug + "/product/operations/" + uuid.NewString() + "/explain", "/v1/platform-tenant-self/product?app=" + app.Slug, "/v1/product-control/portal", "/v1/product-control/portal.js", "/v1/product-control/portal.css"} {
		check(do("GET", path, key, nil), http.StatusNotFound)
	}
	srv.WithProductControlEnabled(true)
	check(do("GET", "/v1/apps/"+app.Slug+"/product", "", nil), http.StatusUnauthorized)
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: dep.Scope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}, "private": {Label: "Private operator plan", Features: map[string]api.ProductFeatureGrant{}}}}
	check(do("PUT", "/v1/apps/"+app.Slug+"/product", key, spec), http.StatusOK)
	token := func(name string) (string, state.PlatformTenant) {
		t.Helper()
		tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, name, name, 100)
		if err != nil {
			t.Fatal(err)
		}
		w := do("POST", "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", key, api.CreatePlatformTenantAccessTokenRequest{Name: "portal", Scopes: []string{api.ScopePlatformTenantOperationsRead, api.ScopePlatformTenantOperationsManage}})
		check(w, http.StatusCreated)
		var access api.CreatePlatformTenantAccessTokenResponse
		if err := json.Unmarshal(w.Body.Bytes(), &access); err != nil {
			t.Fatal(err)
		}
		return access.Token, tenant
	}
	aliceKey, alice := token("alice")
	bobKey, _ := token("bob")
	check(do("PUT", "/v1/apps/"+app.Slug+"/product/customers/"+alice.ID, key, map[string]any{"plan": "starter"}), http.StatusOK)
	check(do("GET", "/v1/apps/"+app.Slug+"/product", aliceKey, nil), http.StatusForbidden)
	check(do("GET", "/v1/platform-tenant-self/product?app="+app.Slug, key, nil), http.StatusForbidden)
	check(do("GET", "/v1/platform-tenant-self/product?app="+app.Slug, bobKey, nil), http.StatusNotFound)
	view := do("GET", "/v1/platform-tenant-self/product?app="+app.Slug+"&tenant_id=ignored", aliceKey, nil)
	check(view, http.StatusOK)
	if strings.Contains(view.Body.String(), "Private operator plan") || view.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("portal disclosed other plans or was cacheable: %s", view.Body.String())
	}
	page := do("GET", "/v1/product-control/portal", "", nil)
	check(page, http.StatusOK)
	if !strings.Contains(page.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || !strings.Contains(page.Body.String(), "Sign in to your product") {
		t.Fatal("portal shell security or login missing")
	}
	var quoted api.ProductPortal
	if err := json.Unmarshal(view.Body.Bytes(), &quoted); err != nil {
		t.Fatal(err)
	}
	start := api.ProductStartRequest{App: app.Slug, Feature: "export", Input: json.RawMessage(`{}`), ExpectedQuote: quoted.Quotes["export"]}
	// Product Control does not open the separate Operations preview gate.
	check(do("POST", "/v1/platform-tenant-self/product/start", aliceKey, start), http.StatusServiceUnavailable)
	srv.Operation().AdmissionEnabled = true
	check(do("POST", "/v1/platform-tenant-self/product/start", bobKey, start), http.StatusForbidden)
	bypass := api.OperationStartRequest{DefinitionID: def.ID, Input: json.RawMessage(`{}`)}
	check(do("POST", "/v1/platform-tenant-self/customer-operations", aliceKey, bypass), http.StatusForbidden)
	missingQuote := start
	missingQuote.ExpectedQuote = ""
	check(do("POST", "/v1/platform-tenant-self/product/start", aliceKey, missingQuote, "missing-quote"), http.StatusConflict)
	accepted := do("POST", "/v1/platform-tenant-self/product/start", aliceKey, start)
	check(accepted, http.StatusAccepted)
	var first api.OperationAcceptedResponse
	if err := json.Unmarshal(accepted.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	lookup := do("POST", "/v1/platform-tenant-self/product/submissions/lookup", aliceKey, map[string]string{"app": app.Slug, "feature": "export", "idempotency_key": "same-work"})
	check(lookup, http.StatusOK)
	var observation api.OperationSubmissionLookupResponse
	if err := json.Unmarshal(lookup.Body.Bytes(), &observation); err != nil || observation.Receipt == nil || observation.Receipt.ID != first.ID {
		t.Fatalf("tenant lookup failed: %s %v", lookup.Body.String(), err)
	}
	explainPath := "/v1/apps/" + app.Slug + "/product/operations/" + first.ID + "/explain"
	check(do("GET", explainPath, aliceKey, nil), http.StatusForbidden)
	check(do("GET", explainPath, bobKey, nil), http.StatusForbidden)
	check(do("GET", explainPath+"?after=-1", key, nil), http.StatusBadRequest)
	check(do("GET", explainPath+"?after=1&after=2", key, nil), http.StatusBadRequest)
	check(do("GET", "/v1/apps/"+app.Slug+"/product/operations/"+uuid.NewString()+"/explain", key, nil), http.StatusNotFound)
	var explanation api.ProductOperationExplanation
	decodeExplanation := func() {
		t.Helper()
		w := do("GET", explainPath, key, nil)
		check(w, http.StatusOK)
		if err := json.Unmarshal(w.Body.Bytes(), &explanation); err != nil {
			t.Fatal(err)
		}
	}
	decodeExplanation()
	if explanation.EntitlementDecision != "allowed_at_admission" || explanation.Charge.State != "pending" || explanation.Evidence.Receipt.Admission.DefinitionID != def.ID || len(explanation.Executions) != 1 {
		t.Fatalf("incomplete explanation: %+v", explanation)
	}
	usage := do("GET", "/v1/platform-tenant-self/product?app="+app.Slug, aliceKey, nil)
	check(usage, http.StatusOK)
	var portal api.ProductPortal
	if err := json.Unmarshal(usage.Body.Bytes(), &portal); err != nil {
		t.Fatal(err)
	}
	if len(portal.Usage.Lines) != 1 || portal.Usage.Lines[0].Starts != 1 || portal.Usage.Lines[0].Succeeded != 0 || portal.Usage.Lines[0].AmountMillicents != 0 {
		t.Fatalf("admission billed a success: %+v", portal.Usage)
	}
	if !portal.Bindings["export"].Available || portal.Bindings["export"].DefinitionID != def.ID {
		t.Fatal("portal binding is missing")
	}
	dep2, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:product-v2"})
	if err != nil {
		t.Fatal(err)
	}
	def2, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: dep2.ID, Scope: dep2.Scope, Spec: def.Spec}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep2.ID); err != nil {
		t.Fatal(err)
	}
	retried := do("POST", "/v1/platform-tenant-self/product/start", aliceKey, start)
	check(retried, http.StatusAccepted)
	var retry api.OperationAcceptedResponse
	if err := json.Unmarshal(retried.Body.Bytes(), &retry); err != nil || retry.ID != first.ID {
		t.Fatalf("redeploy duplicated work: %s %v", retried.Body.String(), err)
	}
	second := do("POST", "/v1/platform-tenant-self/product/start", aliceKey, start, "new-work")
	check(second, http.StatusAccepted)
	var next api.OperationAcceptedResponse
	if err := json.Unmarshal(second.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	nextOperation, err := store.OperationByID(ctx, acct.ID, alice.ID, next.ID)
	if err != nil || nextOperation.DefinitionID != def2.ID || nextOperation.ProductControl.Revision != portal.Customer.Revision {
		t.Fatalf("deployment binding changed prices or missed new code: %+v %v", nextOperation, err)
	}
	// Receipt settlement remains active while the Product Control surface is off.
	srv.WithProductControlEnabled(false)
	check(do("GET", "/v1/platform-tenant-self/product?app="+app.Slug, aliceKey, nil), http.StatusNotFound)
	check(do("POST", "/v1/platform-tenant-self/product/start", aliceKey, start), http.StatusNotFound)
	check(do("GET", explainPath, key, nil), http.StatusNotFound)
	check(do("GET", explainPath, "", nil), http.StatusNotFound)
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: uuid.NewString(), TargetURL: "unix:///tmp/product-http.sock", VPCPUs: 8, MemMB: 8192, MaxConcurrency: 32, AdmissionCeilingMB: 8192, VCPUBudget: 32, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	op, err := store.OperationByID(ctx, acct.ID, alice.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := store.ClaimInvocationWithCap(ctx, op.CurrentInvocationID, instance.ID, 60, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteKeyedInvocation(ctx, inv.ID, inv.Attempts, json.RawMessage(`{"secret_result":"customer-file"}`)); err != nil {
		t.Fatal(err)
	}
	srv.WithProductControlEnabled(true)
	decodeExplanation()
	if explanation.Charge.State != "settled" || explanation.Charge.AmountMillicents != 25000 || explanation.Evidence.SucceededAt == nil || !explanation.Outcome.ResultAvailable || explanation.Outcome.State != api.OperationSucceeded {
		t.Fatalf("success not traced: %+v", explanation)
	}
	w := do("GET", explainPath, key, nil)
	if strings.Contains(w.Body.String(), "secret_result") || strings.Contains(w.Body.String(), "customer-file") || strings.Contains(w.Body.String(), "execution_capability") || strings.Contains(w.Body.String(), "artifact_storage_keys") {
		t.Fatal("explanation exposed payload or execution authority")
	}
	srv.WithProductControlEnabled(false)
}
