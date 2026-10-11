// adr: 521 — preview rollout is scoped; closing admission preserves retained work.
package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

func writeOperationPreviewPolicy(t *testing.T, path string, account, app, scope string, tenants ...string) {
	t.Helper()
	now := time.Now().UTC()
	policy := operations.PreviewPolicy{Version: 1, Enabled: true, NotBefore: now.Add(-time.Minute), ExpiresAt: now.Add(15 * time.Minute), Cohorts: []operations.PreviewCohort{{AccountID: uuid.MustParse(account).String(), AppID: uuid.MustParse(app).String(), Scope: scope, PlatformTenantIDs: tenants}}}
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	stage := path + ".next"
	if err := os.WriteFile(stage, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(stage, path); err != nil {
		t.Fatal(err)
	}
}

func TestOperationsPreviewAdmissionAndRollback(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "preview-http@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "preview-http", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	staging, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "preview", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(private, workloadidentity.DefaultIssuer, "preview-http", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	jwksPath := filepath.Join(t.TempDir(), "public-jwks.json")
	jwks, err := json.Marshal(signer.JWKS())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jwksPath, jwks, 0600); err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(t.TempDir(), "preview.json")
	srv.operationsAdmissionEnabled = true // startup must override this private fixture fallback
	if err := srv.configureOperations(Config{OperationsPreviewPolicyPath: policyPath, OperationsWorkloadJWKSPath: jwksPath}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	localStorage, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv.WithOperationArtifactStorage(localStorage)
	do := func(method, path, bearer string, body any, idempotency string) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+bearer)
		if idempotency != "" {
			r.Header.Set("Idempotency-Key", idempotency)
		}
		w := httptest.NewRecorder()
		srv.handler().ServeHTTP(w, r)
		return w
	}
	check := func(w *httptest.ResponseRecorder, expected int) {
		t.Helper()
		if w.Code != expected {
			t.Fatalf("status %d want %d: %s", w.Code, expected, w.Body.String())
		}
	}
	spec := api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`true`), ProgressStages: []string{"generating"}}
	definitionPath := "/v1/apps/" + app.Slug + "/deployments/" + dep.ID + "/operation-definitions/export"
	check(do(http.MethodPut, definitionPath, key, spec, ""), http.StatusServiceUnavailable)
	var tenants []state.PlatformTenant
	var tokens []string
	for _, name := range []string{"alice", "bob"} {
		tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, name, name, 100)
		if err != nil {
			t.Fatal(err)
		}
		tenants = append(tenants, tenant)
		w := do(http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", key, api.CreatePlatformTenantAccessTokenRequest{Name: "preview", Scopes: []string{api.ScopePlatformTenantOperationsRead, api.ScopePlatformTenantOperationsManage}}, "")
		check(w, http.StatusCreated)
		var token api.CreatePlatformTenantAccessTokenResponse
		if err := json.Unmarshal(w.Body.Bytes(), &token); err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, token.Token)
	}
	writeOperationPreviewPolicy(t, policyPath, acct.ID, app.ID, dep.Scope, tenants[0].ID)
	w := do(http.MethodPut, definitionPath, key, spec, "")
	check(w, http.StatusOK)
	var def api.OperationDefinitionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &def); err != nil {
		t.Fatal(err)
	}
	check(do(http.MethodPut, "/v1/apps/"+app.Slug+"/deployments/"+staging.ID+"/operation-definitions/export", key, spec, ""), http.StatusServiceUnavailable)
	stagingDef, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: staging.ID, Scope: staging.Scope, Spec: spec}})
	if err != nil {
		t.Fatal(err)
	}
	start := api.OperationStartRequest{DefinitionID: def.ID, Input: []byte(`{}`)}
	check(do(http.MethodPost, "/v1/platform-tenant-self/customer-operations", tokens[0], api.OperationStartRequest{DefinitionID: stagingDef.ID, Input: []byte(`{}`)}, "staging"), http.StatusServiceUnavailable)
	check(do(http.MethodPost, "/v1/platform-tenant-self/customer-operations", tokens[1], start, "bob"), http.StatusServiceUnavailable)
	w = do(http.MethodPost, "/v1/platform-tenant-self/customer-operations", tokens[0], start, "preview-export")
	check(w, http.StatusAccepted)
	var receipt api.OperationAcceptedResponse
	if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	verifier, backend := srv.operationsWorkloadVerifier, srv.operationArtifactStorage
	srv.operationsWorkloadVerifier = nil
	check(do(http.MethodPost, "/v1/platform-tenant-self/customer-operations", tokens[0], start, "unready-trust"), http.StatusServiceUnavailable)
	srv.operationsWorkloadVerifier = verifier
	srv.operationArtifactStorage = nil
	check(do(http.MethodPost, "/v1/platform-tenant-self/customer-operations", tokens[0], start, "unready-storage"), http.StatusServiceUnavailable)
	srv.operationArtifactStorage = backend
	for _, raw := range [][]byte{[]byte(`{"version":1,"enabled":false}`), []byte(`{"version":1,"enabled":`)} {
		if err := os.WriteFile(policyPath, raw, 0600); err != nil {
			t.Fatal(err)
		}
		check(do(http.MethodPost, "/v1/platform-tenant-self/customer-operations", tokens[0], start, "after-rollback"), http.StatusServiceUnavailable)
		check(do(http.MethodPut, definitionPath, key, spec, ""), http.StatusServiceUnavailable)
		check(do(http.MethodGet, receipt.StatusURL, tokens[0], nil, ""), http.StatusOK)
	}
	if err := os.Remove(policyPath); err != nil {
		t.Fatal(err)
	}
	check(do(http.MethodPost, "/v1/platform-tenant-self/customer-operations", tokens[0], start, "removed"), http.StatusServiceUnavailable)
	check(do(http.MethodGet, receipt.EventsURL, tokens[0], nil, ""), http.StatusOK)
	retained, err := store.OperationByID(ctx, acct.ID, tenants[0].ID, receipt.ID)
	if err != nil || retained.State != api.OperationAccepted {
		t.Fatal("rollback altered retained work", err)
	}
	manifest := &gregalemanifest.Manifest{ResolvedOperations: []api.OperationDefinitionSpec{spec}}
	staged, problem := srv.applySourceRefManifest(ctx, acct, app, manifest, dep.Scope, true)
	if problem == nil || problem.Status != http.StatusServiceUnavailable || sourceRefManifestNeedsRollback(staged) {
		t.Fatal("closed source cohort reached manifest mutation")
	}
	writeOperationPreviewPolicy(t, policyPath, acct.ID, app.ID, dep.Scope, tenants[0].ID)
	check(do(http.MethodPost, "/v1/platform-tenant-self/customer-operations", tokens[0], start, "preview-export"), http.StatusAccepted)
	if err := srv.configureOperations(Config{OperationsWorkloadJWKSPath: jwksPath}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	check(do(http.MethodPost, "/v1/platform-tenant-self/customer-operations", tokens[0], start, "default"), http.StatusServiceUnavailable)
	check(do(http.MethodGet, receipt.StatusURL, tokens[0], nil, ""), http.StatusOK)
}
