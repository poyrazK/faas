// ADR-521: customer operations preserve ownership, execution fences and independent delivery.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

func TestOperationsHTTPBoundary(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "operation-http@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "operation-http", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "export-http", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	handler := srv.handler()
	preflight := httptest.NewRequest(http.MethodOptions, "/v1/platform-tenant-self/customer-operations", nil)
	preflight.Header.Set("Origin", "https://customer.example")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight.Header.Set("Access-Control-Request-Headers", "authorization, content-type, idempotency-key")
	cors := httptest.NewRecorder()
	handler.ServeHTTP(cors, preflight)
	if cors.Code != http.StatusNoContent || cors.Header().Get("Access-Control-Allow-Origin") != "*" || cors.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("customer bearer preflight: %d %v", cors.Code, cors.Header())
	}
	preflight.URL.Path = "/v1/runtime/operations/" + uuid.NewString() + "/progress"
	cors = httptest.NewRecorder()
	handler.ServeHTTP(cors, preflight)
	if cors.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("browser CORS included privileged runtime reporting")
	}

	do := func(method, path, bearer string, body any, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+bearer)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	check := func(rec *httptest.ResponseRecorder, status int) {
		t.Helper()
		if rec.Code != status {
			t.Fatalf("HTTP %d, want %d: %s", rec.Code, status, rec.Body.String())
		}
	}
	spec := api.OperationDefinitionSpec{Name: "export", TransactionReceipt: api.OperationTransactionPostgres, Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant,
		InputSchema:  json.RawMessage(`{"type":"object","required":["count"],"properties":{"count":{"type":"integer","minimum":1}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","required":["file"],"properties":{"file":{"type":"string"}},"additionalProperties":false}`), ProgressStages: []string{"generating"}}
	definitionPath := "/v1/apps/" + app.Slug + "/deployments/" + dep.ID + "/operation-definitions/export"
	check(do("PUT", definitionPath, key, spec, nil), http.StatusServiceUnavailable)
	definitions, err := store.OperationDefinitionsForDeployment(ctx, acct.ID, app.ID, dep.ID)
	if err != nil || len(definitions) != 0 {
		t.Fatalf("closed production gate persisted definitions: %+v %v", definitions, err)
	}
	srv.operationsAdmissionEnabled = true
	define := do("PUT", "/v1/apps/"+app.Slug+"/deployments/"+dep.ID+"/operation-definitions/export", key, spec, nil)
	check(define, http.StatusOK)
	var def api.OperationDefinitionResponse
	if err := json.Unmarshal(define.Body.Bytes(), &def); err != nil {
		t.Fatal(err)
	}
	if def.Spec.TransactionReceipt != api.OperationTransactionPostgres {
		t.Fatal("definition lost explicit transaction receipt contract")
	}
	receiptDefinitions := do("GET", "/v1/apps/"+app.Slug+"/deployments/"+dep.ID+"/operation-definitions", key, nil, nil)
	check(receiptDefinitions, http.StatusOK)
	var receiptPage api.OperationDefinitionsResponse
	if err := json.Unmarshal(receiptDefinitions.Body.Bytes(), &receiptPage); err != nil || len(receiptPage.Definitions) != 1 || receiptPage.Definitions[0].TransactionReceipt != api.OperationTransactionPostgres {
		t.Fatalf("definition summary lost receipt opt-in: %s %v", receiptDefinitions.Body.String(), err)
	}
	token := func(name string, scopes []string) (string, state.PlatformTenant) {
		t.Helper()
		tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, name, name, 100)
		if err != nil {
			t.Fatal(err)
		}
		rec := do("POST", "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", key, api.CreatePlatformTenantAccessTokenRequest{Name: "browser", Scopes: scopes}, nil)
		check(rec, http.StatusCreated)
		var out api.CreatePlatformTenantAccessTokenResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Token, tenant
	}
	aliceKey, alice := token("alice", []string{api.ScopePlatformTenantOperationsRead, api.ScopePlatformTenantOperationsManage})
	bobKey, _ := token("bob", []string{api.ScopePlatformTenantOperationsRead})
	start := api.OperationStartRequest{DefinitionID: def.ID, Input: json.RawMessage(`{"count":1}`)}
	headers := map[string]string{"Idempotency-Key": "export-1"}
	rec := do("POST", "/v1/platform-tenant-self/customer-operations", aliceKey, start, headers)
	check(rec, http.StatusAccepted)
	var receipt api.OperationAcceptedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.StatusURL != "/v1/platform-tenant-self/customer-operations/"+receipt.ID || receipt.EventsURL != receipt.StatusURL+"/events" {
		t.Fatalf("customer receipt left its namespace: %+v", receipt)
	}
	check(do("GET", "/v1/platform-tenant-self/operations/"+receipt.ID, aliceKey, nil, nil), http.StatusForbidden)
	check(do("POST", "/v1/platform-tenant-self/operations/"+receipt.ID+"/cancel", aliceKey, api.OperationCancellationRequest{ExpectedGeneration: 1}, nil), http.StatusForbidden)
	t.Run("submission lookup stays scoped and read-only with admission closed", func(t *testing.T) {
		check := func(rec *httptest.ResponseRecorder, status int) {
			t.Helper()
			if rec.Code != status {
				t.Fatalf("HTTP %d, want %d: %s", rec.Code, status, rec.Body.String())
			}
		}
		lookupPath := "/v1/platform-tenant-self/customer-operations/submissions/lookup"
		lookup := api.OperationSubmissionLookupRequest{AppID: app.ID, Scope: def.Scope, Name: "export", IdempotencyKey: "export-1"}
		srv.operationsAdmissionEnabled = false
		defer func() { srv.operationsAdmissionEnabled = true }()
		found := do("POST", lookupPath, aliceKey, lookup, nil)
		check(found, http.StatusOK)
		var result api.OperationSubmissionLookupResponse
		if err := json.Unmarshal(found.Body.Bytes(), &result); err != nil || result.State != "accepted" || result.Receipt == nil || result.Receipt.ID != receipt.ID || result.AcceptedAt == nil || found.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("lookup acceptance: %s %v", found.Body, err)
		}
		// A read-only customer credential cannot discover another customer's key.
		foreign := do("POST", lookupPath, bobKey, lookup, nil)
		check(foreign, http.StatusOK)
		if err := json.Unmarshal(foreign.Body.Bytes(), &result); err != nil || result.State != "unresolved" {
			t.Fatal("foreign acceptance leaked", foreign.Body, err)
		}
		check(do("POST", lookupPath, key, lookup, nil), http.StatusForbidden)
		check(do("POST", lookupPath+"?platform_tenant_id="+alice.ID, aliceKey, lookup, nil), http.StatusBadRequest)
		check(do("POST", lookupPath, aliceKey, map[string]any{"app_id": app.ID, "scope": def.Scope, "name": "export", "idempotency_key": "export-1", "platform_tenant_id": alice.ID}, nil), http.StatusBadRequest)
		check(do("POST", lookupPath, aliceKey, map[string]string{"app_id": app.ID, "scope": def.Scope, "name": "export", "idempotency_key": strings.Repeat("x", api.OperationSubmissionLookupMaxBytes)}, nil), http.StatusRequestEntityTooLarge)
		lookup.ExpectedIdentity = &api.OperationTenantIdentity{AccountID: acct.ID, PlatformTenantID: alice.ID}
		check(do("POST", lookupPath, bobKey, lookup, nil), http.StatusConflict)
	})
	t.Run("durable submission fences credentials and feature scope", func(t *testing.T) {
		check := func(rec *httptest.ResponseRecorder, status int) {
			t.Helper()
			if rec.Code != status {
				t.Fatalf("HTTP %d, want %d: %s", rec.Code, status, rec.Body.String())
			}
		}
		fenced := start
		fenced.ExpectedIdentity = &api.OperationTenantIdentity{AccountID: acct.ID, PlatformTenantID: alice.ID}
		fenced.ExpectedScope = &api.OperationSubmissionScope{AppID: app.ID, Scope: "staging", Name: "export"}
		check(do("POST", "/v1/platform-tenant-self/customer-operations", aliceKey, fenced, headers), http.StatusConflict)
		fenced.ExpectedScope.Scope = def.Scope
		check(do("POST", "/v1/platform-tenant-self/customer-operations", aliceKey, fenced, headers), http.StatusAccepted)
		fenced.ExpectedIdentity.PlatformTenantID = app.ID
		conflict := do("POST", "/v1/platform-tenant-self/customer-operations", aliceKey, fenced, headers)
		check(conflict, http.StatusConflict)
		if !strings.Contains(conflict.Body.String(), "operation_identity_conflict") {
			t.Fatal(conflict.Body.String())
		}
	})
	duplicate := do("POST", "/v1/platform-tenant-self/customer-operations", aliceKey, start, headers)
	check(duplicate, http.StatusAccepted)
	if duplicate.Body.String() != rec.Body.String() {
		t.Fatal("HTTP receipt changed on duplicate submission")
	}
	check(do("POST", "/v1/platform-tenant-self/customer-operations", bobKey, start, headers), http.StatusForbidden)
	check(do("GET", receipt.StatusURL, bobKey, nil, nil), http.StatusNotFound)
	check(do("GET", receipt.StatusURL, key, nil, nil), http.StatusForbidden)
	check(do("GET", receipt.EventsURL+"?after=-1", aliceKey, nil, nil), http.StatusBadRequest)
	check(do("GET", "/v1/apps/"+app.Slug+"/operations/"+receipt.ID, key, nil, nil), http.StatusOK)
	check(do("POST", "/v1/platform-tenant-self/customer-operations", aliceKey, api.OperationStartRequest{DefinitionID: def.ID, Input: json.RawMessage(`{"count":2}`)}, headers), http.StatusConflict)
	check(do("POST", "/v1/platform-tenant-self/customer-operations", aliceKey, json.RawMessage(`{"definition_id":"`+def.ID+`","input":{"count":1},"tenant_id":"forged"}`), headers), http.StatusBadRequest)
	check(do("POST", "/v1/platform-tenant-self/customer-operations", aliceKey, json.RawMessage(`{"definition_id":"`+def.ID+`","input":{"count":1},"input":{"count":2}}`), headers), http.StatusBadRequest)
	check(do("POST", receipt.StatusURL+"/cancel", aliceKey, api.OperationCancellationRequest{ExpectedGeneration: 9}, nil), http.StatusConflict)
	srv.operationsAdmissionEnabled = false // reports and downloads survive admission rollback
	readKey, readHash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(ctx, acct.ID, readHash, "recovery-read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	inspectionPath := "/v1/apps/" + app.Slug + "/operations/" + receipt.ID + "/recovery-inspection"
	previewPath := "/v1/apps/" + app.Slug + "/operations/" + receipt.ID + "/recovery-preview"
	inspectionResponse := do("GET", inspectionPath, readKey, nil, nil)
	check(inspectionResponse, http.StatusOK)
	if inspectionResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("inspection may be cached")
	}
	var inspection api.OperationRecoveryInspection
	if err := json.Unmarshal(inspectionResponse.Body.Bytes(), &inspection); err != nil || inspection.OperationID != receipt.ID || inspection.ExecutionKind != "http" {
		t.Fatal("inspection response", err)
	}
	previewResponse := do("POST", previewPath, readKey, api.OperationRecoveryPreviewRequest{ExpectedGeneration: 1, Resolution: "safe_to_retry"}, nil)
	check(previewResponse, http.StatusOK)
	var preview api.OperationRecoveryPreview
	if err := json.Unmarshal(previewResponse.Body.Bytes(), &preview); err != nil || preview.Eligible || !preview.EvidenceRequired || len(preview.Blockers) != 1 || preview.Blockers[0] != "reconciliation_not_required" {
		t.Fatal("accepted work offered recovery", err)
	}
	check(do("POST", previewPath, readKey, api.OperationRecoveryPreviewRequest{ExpectedGeneration: 2, Resolution: "failed"}, nil), http.StatusConflict)
	check(do("POST", previewPath, readKey, json.RawMessage(`{"expected_generation":1,"resolution":"failed","evidence":"forged"}`), nil), http.StatusBadRequest)
	check(do("POST", previewPath, readKey, json.RawMessage(`{"expected_generation":1,"expected_generation":2,"resolution":"failed"}`), nil), http.StatusBadRequest)
	check(do("GET", inspectionPath, aliceKey, nil, nil), http.StatusForbidden)
	check(do("POST", previewPath, aliceKey, api.OperationRecoveryPreviewRequest{ExpectedGeneration: 1, Resolution: "failed"}, nil), http.StatusForbidden)
	check(do("POST", "/v1/apps/"+app.Slug+"/operations/"+receipt.ID+"/recover", readKey, api.OperationRecoveryRequest{}, nil), http.StatusForbidden)
	otherApp, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "foreign-operation", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	check(do("GET", "/v1/apps/"+otherApp.Slug+"/operations/"+receipt.ID+"/recovery-inspection", readKey, nil, nil), http.StatusNotFound)
	check(do("POST", "/v1/apps/"+otherApp.Slug+"/operations/"+receipt.ID+"/recovery-preview", readKey, api.OperationRecoveryPreviewRequest{ExpectedGeneration: 1, Resolution: "failed"}, nil), http.StatusNotFound)
	op, err := store.OperationByID(ctx, acct.ID, alice.ID, receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, uuid.NewString(), "")
	if err != nil {
		t.Fatal(err)
	}
	inv, err := store.ClaimInvocationWithCap(ctx, op.CurrentInvocationID, instance.ID, 60, 100)
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]string
	if err := json.Unmarshal(inv.Headers, &metadata); err != nil {
		t.Fatal(err)
	}
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(private, workloadidentity.DefaultIssuer, "operations-http", workloadidentity.DefaultTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	srv.operationsWorkloadVerifier, err = workloadidentity.NewVerifier(signer.JWKS(), workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	assertion, err := signer.Mint(time.Now(), acct.ID, app.ID, instance.ID, workloadidentity.OperationsAudience)
	if err != nil {
		t.Fatal(err)
	}
	proof := map[string]string{api.InvocationIDHeader: inv.ID, api.OperationAttemptHeader: strconv.Itoa(inv.Attempts), api.OperationCapabilityHeader: metadata[api.OperationCapabilityHeader]}
	controlURL := "/v1/runtime/operations/" + op.ID + "/control"
	check(do("GET", controlURL, aliceKey, nil, proof), http.StatusUnauthorized)
	check(do("GET", controlURL, key, nil, proof), http.StatusUnauthorized)
	check(do("GET", controlURL, assertion.AccessToken, nil, nil), http.StatusUnauthorized)
	staleProof := map[string]string{api.InvocationIDHeader: inv.ID, api.OperationAttemptHeader: strconv.Itoa(inv.Attempts + 1), api.OperationCapabilityHeader: metadata[api.OperationCapabilityHeader]}
	check(do("GET", controlURL, assertion.AccessToken, nil, staleProof), http.StatusConflict)
	readControl := func(cancelled bool) {
		t.Helper()
		before, err := store.OperationByID(ctx, acct.ID, alice.ID, op.ID)
		if err != nil {
			t.Fatal(err)
		}
		read := do("GET", controlURL, assertion.AccessToken, nil, proof)
		check(read, http.StatusOK)
		var control api.OperationExecutionControlResponse
		if err := json.Unmarshal(read.Body.Bytes(), &control); err != nil || control.OperationID != op.ID || control.InvocationID != inv.ID || control.Attempt != inv.Attempts || control.CancellationRequested != cancelled || !control.DeadlineAt.Equal(*inv.DeadlineAt) || !control.LeaseExpiresAt.Equal(*inv.LeaseExpiresAt) || !control.LeaseExpiresAt.After(control.ObservedAt) || control.PollAfterMS != api.OperationControlPollIntervalMS {
			t.Fatalf("control: %s %v", read.Body.String(), err)
		}
		if read.Header().Get("Cache-Control") != "no-store" || bytes.Contains(read.Body.Bytes(), []byte(metadata[api.OperationCapabilityHeader])) || bytes.Contains(read.Body.Bytes(), []byte("result")) {
			t.Fatal("control leaked or cached private work")
		}
		after, err := store.OperationByID(ctx, acct.ID, alice.ID, op.ID)
		current, invErr := store.InvocationByID(ctx, inv.ID)
		if err != nil || invErr != nil || after.LatestSequence != before.LatestSequence || after.ReportCount != before.ReportCount || !current.LeaseExpiresAt.Equal(*inv.LeaseExpiresAt) {
			t.Fatal("control read changed reports, events or lease")
		}
	}
	readControl(false) // Already admitted work can read control with admission closed.
	report := api.OperationReportRequest{ReportID: "chunk-1", Stage: "generating", Completed: 1, Total: 3}
	progressURL := "/v1/runtime/operations/" + op.ID + "/progress"
	check(do("POST", progressURL, aliceKey, report, proof), http.StatusUnauthorized)
	wrongAudience, _ := signer.Mint(time.Now(), acct.ID, app.ID, instance.ID, workloadidentity.FlagsAudience)
	check(do("POST", progressURL, wrongAudience.AccessToken, report, proof), http.StatusUnauthorized)
	check(do("POST", progressURL, assertion.AccessToken, report, proof), http.StatusOK)
	check(do("POST", progressURL, assertion.AccessToken, report, proof), http.StatusOK)
	provider, artifact := operationArtifactFixture(t, srv, store, acct, app, def.Scope)
	artifactURL := "/v1/runtime/operations/" + op.ID + "/artifacts"
	badArtifact := artifact
	badArtifact.SizeBytes++
	check(do("POST", artifactURL, assertion.AccessToken, badArtifact, proof), http.StatusConflict)
	check(do("POST", artifactURL, aliceKey, artifact, proof), http.StatusUnauthorized)
	backend := srv.operationArtifactStorage
	interrupted := &operationArtifactInterruptedStorage{StorageBackend: backend}
	srv.operationArtifactStorage = interrupted
	check(do("POST", artifactURL, assertion.AccessToken, artifact, proof), http.StatusServiceUnavailable)
	if interrupted.key == "" {
		t.Fatal("interrupted copy did not reach storage")
	}
	srv.operationArtifactStorage = backend
	attached := do("POST", artifactURL, assertion.AccessToken, artifact, proof)
	check(attached, http.StatusOK)
	var artifactStatus api.OperationResponse
	if err := json.Unmarshal(attached.Body.Bytes(), &artifactStatus); err != nil || len(artifactStatus.Artifacts) != 1 {
		t.Fatalf("artifact response: %s %v", attached.Body.String(), err)
	}
	provider.replace("changed after first attachment")
	provider.mu.Lock()
	priorReads := provider.reads
	provider.mu.Unlock()
	check(do("POST", artifactURL, assertion.AccessToken, artifact, proof), http.StatusOK)
	provider.mu.Lock()
	if provider.reads != priorReads {
		t.Error("verified receipt replay repeated object I/O")
	}
	provider.mu.Unlock()
	provider.replace("id,count\nalice,1\n")
	fileURL := receipt.StatusURL + "/artifacts/" + artifactStatus.Artifacts[0].ID
	check(do("GET", fileURL, aliceKey, nil, nil), http.StatusConflict)
	check(do("POST", receipt.StatusURL+"/cancel", aliceKey, api.OperationCancellationRequest{ExpectedGeneration: 2}, nil), http.StatusConflict)
	check(do("POST", receipt.StatusURL+"/cancel", aliceKey, api.OperationCancellationRequest{ExpectedGeneration: 1}, nil), http.StatusOK)
	readControl(true)
	if err := store.CompleteKeyedInvocation(ctx, inv.ID, inv.Attempts, json.RawMessage(`{"file":"exports/alice.csv"}`)); err != nil {
		t.Fatal(err)
	}
	result := do("GET", receipt.StatusURL, aliceKey, nil, nil)
	check(result, http.StatusOK)
	var out api.OperationResponse
	if err := json.Unmarshal(result.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.State != api.OperationSucceeded || !out.CancellationRequested || out.Progress == nil {
		t.Fatalf("operation outcome: %+v", out)
	}
	check(do("GET", controlURL, assertion.AccessToken, nil, proof), http.StatusConflict)
	if result.Header().Get("Cache-Control") != "no-store" || bytes.Contains(result.Body.Bytes(), []byte(metadata[api.OperationCapabilityHeader])) {
		t.Fatal("customer response leaked or cached execution authority")
	}
	download := do("GET", fileURL, aliceKey, nil, nil)
	check(download, http.StatusOK)
	if download.Body.String() != "id,count\nalice,1\n" || download.Header().Get("X-Gregale-Artifact-Sha256") != artifact.SHA256 {
		t.Fatal("unverified bytes served")
	}
	check(do("GET", fileURL, bobKey, nil, nil), http.StatusNotFound)
	provider.replace("changed export")
	provider.mu.Lock()
	provider.missing = true
	provider.mu.Unlock()
	retainedDownload := do("GET", fileURL, aliceKey, nil, nil)
	check(retainedDownload, http.StatusOK)
	if retainedDownload.Body.String() != "id,count\nalice,1\n" {
		t.Fatal("source mutation changed retained bytes")
	}
	retainedOp, err := store.OperationByID(ctx, acct.ID, alice.ID, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	artifactKey := retainedOp.ArtifactStorageKeys[artifactStatus.Artifacts[0].ID]
	if artifactKey == "" {
		t.Fatal("artifact has no retained storage receipt")
	}
	if artifactKey == interrupted.key {
		t.Fatal("retry replaced an uncertain upload in place")
	}
	cleanupAt := time.Now().Add(api.OperationArtifactStagingLifetime + time.Second)
	interrupted.deleteUnavailable = true
	srv.operationArtifactStorage = interrupted
	if err := srv.cleanupOperationArtifacts(ctx, cleanupAt); err == nil {
		t.Fatal("storage outage lost cleanup retry")
	}
	srv.operationArtifactStorage = backend
	if err := srv.cleanupOperationArtifacts(ctx, cleanupAt.Add(api.OperationArtifactCleanupRetry)); err != nil {
		t.Fatal(err)
	}
	if stream, err := backend.Get(ctx, interrupted.key); !storage.IsNotFound(err) {
		if stream != nil {
			_ = stream.Close()
		}
		t.Fatalf("abandoned copy survived cleanup: %v", err)
	}
	check(do("GET", fileURL, aliceKey, nil, nil), http.StatusOK)
	if err := srv.operationArtifactStorage.Put(ctx, artifactKey, bytes.NewBufferString("corrupt retained copy")); err != nil {
		t.Fatal(err)
	}
	changed := do("GET", fileURL, aliceKey, nil, nil)
	check(changed, http.StatusConflict)
	if !bytes.Contains(changed.Body.Bytes(), []byte("operation_artifact_changed")) {
		t.Fatal("changed artifact has no independent error")
	}
	if err := srv.cleanupOperationArtifacts(ctx, retainedOp.ExpiresAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	check(do("GET", fileURL, aliceKey, nil, nil), http.StatusGone)
	check(do("GET", receipt.StatusURL, aliceKey, nil, nil), http.StatusOK)
	provider.replace("id,count\nalice,1\n")
	check(do("POST", progressURL, assertion.AccessToken, report, proof), http.StatusConflict)

	streamServer := httptest.NewServer(handler)
	defer streamServer.Close()
	streamReq, _ := http.NewRequest("GET", streamServer.URL+receipt.EventsURL, nil)
	streamReq.Header.Set("Authorization", "Bearer "+aliceKey)
	streamReq.Header.Set("Accept", "text/event-stream")
	streamReq.Header.Set("Last-Event-ID", "3")
	streamClient := &http.Client{Timeout: 5 * time.Second}
	response, err := streamClient.Do(streamReq)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream status: %d", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	replayed := int64(0)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) > 6 && line[:6] == "data: " {
			var event api.OperationEvent
			if err := json.Unmarshal([]byte(line[6:]), &event); err != nil {
				t.Fatal(err)
			}
			if event.Sequence != 4 {
				t.Fatalf("durable stream resumed at %d, want 4", event.Sequence)
			}
			replayed = event.Sequence
			break
		}
	}
	if replayed == 0 {
		t.Fatalf("stream did not replay: %v", scanner.Err())
	}
	tokens, err := store.ListPlatformTenantAccessTokens(ctx, acct.ID, alice.ID)
	if err != nil || len(tokens) != 1 {
		t.Fatalf("token inventory: %v", err)
	}
	if _, _, err := store.RevokePlatformTenantAccessToken(ctx, acct.ID, alice.ID, tokens[0].ID); err != nil {
		t.Fatal(err)
	}
	revoked := false
	for scanner.Scan() {
		if scanner.Text() == "event: auth_expired" {
			revoked = true
			break
		}
	}
	if !revoked {
		t.Fatalf("live stream retained revoked authority: %v", scanner.Err())
	}
	_ = response.Body.Close()
	// Mint a replacement through the application's backend; the operation remains readable.
	plaintext, prefix, tokenHash, err := api.GeneratePlatformTenantAccessToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreatePlatformTenantAccessToken(ctx, state.PlatformTenantAccessTokenInput{AccountID: acct.ID, TenantID: alice.ID, Name: "replacement", Prefix: prefix, TokenHash: tokenHash, Scopes: []string{api.ScopePlatformTenantOperationsRead, api.ScopePlatformTenantOperationsManage}, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	aliceKey = plaintext
	srv.operationsAdmissionEnabled = false
	check(do("GET", receipt.StatusURL, aliceKey, nil, nil), http.StatusOK)
	check(do("POST", "/v1/platform-tenant-self/customer-operations", aliceKey, start, headers), http.StatusServiceUnavailable)

	// A native workflow uses the same customer submission/read contract and
	// exposes its own execution identity without issuing an HTTP runtime claim.
	srv.operationsAdmissionEnabled, srv.workflowRuntimeEnabled = true, true
	workflowSpec := api.WorkflowSpec{Name: "export-chain", Steps: []api.WorkflowStepSpec{{Name: "generating", Path: "/generate"}}}
	workflowJSON, _ := json.Marshal([]api.WorkflowSpec{workflowSpec})
	workflowDep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Workflows: workflowJSON})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, workflowDep.ID); err != nil {
		t.Fatal(err)
	}
	spec.Name, spec.Workflow = "workflow-export", workflowSpec.Name
	spec.TransactionReceipt = ""
	workflowPath := "/v1/apps/" + app.Slug + "/deployments/" + workflowDep.ID + "/operation-definitions/" + spec.Name
	defined := do("PUT", workflowPath, key, spec, nil)
	check(defined, http.StatusOK)
	var workflowDef api.OperationDefinitionResponse
	if err := json.Unmarshal(defined.Body.Bytes(), &workflowDef); err != nil || workflowDef.Spec.Workflow != workflowSpec.Name {
		t.Fatalf("workflow definition: %s %v", defined.Body.String(), err)
	}
	listed := do("GET", "/v1/apps/"+app.Slug+"/deployments/"+workflowDep.ID+"/operation-definitions", key, nil, nil)
	check(listed, http.StatusOK)
	var definitionsPage api.OperationDefinitionsResponse
	if err := json.Unmarshal(listed.Body.Bytes(), &definitionsPage); err != nil || len(definitionsPage.Definitions) != 1 || definitionsPage.Definitions[0].Workflow != workflowSpec.Name {
		t.Fatalf("workflow discovery: %s %v", listed.Body.String(), err)
	}
	workflowStart := api.OperationStartRequest{DefinitionID: workflowDef.ID, Input: start.Input}
	accepted := do("POST", "/v1/platform-tenant-self/customer-operations", aliceKey, workflowStart, map[string]string{"Idempotency-Key": "workflow-export-1"})
	check(accepted, http.StatusAccepted)
	var workflowReceipt api.OperationAcceptedResponse
	if err := json.Unmarshal(accepted.Body.Bytes(), &workflowReceipt); err != nil {
		t.Fatal(err)
	}
	check(do("GET", workflowReceipt.StatusURL, aliceKey, nil, nil), http.StatusOK)
	check(do("GET", workflowReceipt.StatusURL, bobKey, nil, nil), http.StatusNotFound)
	executions := do("GET", "/v1/apps/"+app.Slug+"/operations/"+workflowReceipt.ID+"/executions", key, nil, nil)
	check(executions, http.StatusOK)
	var workflowExecutions api.OperationExecutionsResponse
	if err := json.Unmarshal(executions.Body.Bytes(), &workflowExecutions); err != nil || len(workflowExecutions.Executions) != 1 || workflowExecutions.Executions[0].WorkflowRunID == "" || workflowExecutions.Executions[0].InvocationID != "" || bytes.Contains(executions.Body.Bytes(), []byte(`"invocation_id"`)) {
		t.Fatalf("workflow execution family: %s %v", executions.Body.String(), err)
	}
	runID := workflowExecutions.Executions[0].WorkflowRunID
	check(do("POST", "/v1/workflows/runs/"+runID+"/steps/generating/retry", key, nil, nil), http.StatusConflict)
	resumeCount := 0
	check(do("POST", "/v1/workflows/runs/"+runID+"/resume", key, api.ResumeWorkflowRunRequest{ExpectedResumeCount: &resumeCount}, nil), http.StatusConflict)

	// ADR-664: a batch Job also uses the existing customer receipt and read
	// contract, with one native run and no HTTP/workflow execution identity.
	job, err := store.JobCreate(ctx, acct.ID, "export-job", "batch", "registry.example/export:v1", []string{"export"}, 128, 60, 1, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.JobSetImageMaterialization(ctx, job.ID, job.ImageRef, "ready", "sha256:"+strings.Repeat("a", 64), "jobs/"+job.ID+".ext4", ""); err != nil {
		t.Fatal(err)
	}
	spec.Name, spec.Path, spec.Workflow, spec.Job = "job-export", "/job-exports", "", job.Name
	jobPath := "/v1/apps/" + app.Slug + "/deployments/" + workflowDep.ID + "/operation-definitions/" + spec.Name
	defined = do("PUT", jobPath, key, spec, nil)
	check(defined, http.StatusOK)
	var jobDef api.OperationDefinitionResponse
	if err := json.Unmarshal(defined.Body.Bytes(), &jobDef); err != nil || jobDef.Spec.Job != job.Name {
		t.Fatalf("Job definition: %s %v", defined.Body.String(), err)
	}
	jobStart := api.OperationStartRequest{DefinitionID: jobDef.ID, Input: start.Input}
	var jobReceipt api.OperationAcceptedResponse
	for i := 0; i < 2; i++ {
		accepted = do("POST", "/v1/platform-tenant-self/customer-operations", aliceKey, jobStart, map[string]string{"Idempotency-Key": "job-export-1"})
		check(accepted, http.StatusAccepted)
		var repeated api.OperationAcceptedResponse
		if err := json.Unmarshal(accepted.Body.Bytes(), &repeated); err != nil || i > 0 && repeated.ID != jobReceipt.ID {
			t.Fatalf("Job receipt replay: %s %v", accepted.Body.String(), err)
		}
		jobReceipt = repeated
	}
	check(do("GET", jobReceipt.StatusURL, aliceKey, nil, nil), http.StatusOK)
	check(do("GET", jobReceipt.StatusURL, bobKey, nil, nil), http.StatusNotFound)
	executions = do("GET", "/v1/apps/"+app.Slug+"/operations/"+jobReceipt.ID+"/executions", key, nil, nil)
	check(executions, http.StatusOK)
	var jobExecutions api.OperationExecutionsResponse
	if err := json.Unmarshal(executions.Body.Bytes(), &jobExecutions); err != nil || len(jobExecutions.Executions) != 1 || jobExecutions.Executions[0].JobRunID == "" || jobExecutions.Executions[0].InvocationID != "" || jobExecutions.Executions[0].WorkflowRunID != "" {
		t.Fatalf("Job execution family: %s %v", executions.Body.String(), err)
	}
}

// ADR-521: JSON normalization must not make ordinary integer control fields
// undecodable when progress, artifact sizes or generations are multiples of ten.
func TestOperationsTypedControlNumbers(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"report_id":"rows","stage":"generating","completed":100,"total":1000}`))
	rec := httptest.NewRecorder()
	var report api.OperationReportRequest
	if !decodeOperationBody(rec, req, &report, api.OperationReportBodyMaxBytes) || report.Completed != 100 || report.Total != 1000 {
		t.Fatalf("typed progress: %+v, %s", report, rec.Body.String())
	}
}

func TestOperationsBodyLimitProblem(t *testing.T) {
	body := bytes.Repeat([]byte(" "), api.OperationReportBodyMaxBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	var report api.OperationReportRequest
	if decodeOperationBody(rec, req, &report, api.OperationReportBodyMaxBytes) {
		t.Fatal("oversized operation body was accepted")
	}
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || rec.Code != http.StatusRequestEntityTooLarge || problem.Limit == nil || *problem.Limit != int64(api.OperationReportBodyMaxBytes) || problem.Observed == nil || *problem.Observed != int64(api.OperationReportBodyMaxBytes)+1 {
		t.Fatalf("body limit did not include its numeric bound: %d %s, %v", rec.Code, rec.Body.String(), err)
	}
}

func TestOperationsWorkflowRecoveryProblems(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"changed run", state.ErrWorkflowResumeConflict, http.StatusConflict, "operation_state_conflict"},
		{"unsafe resume", state.ErrWorkflowResumeUnsafe, http.StatusConflict, "operation_state_conflict"},
		{"unavailable target", state.ErrWorkflowResumeUnavailable, http.StatusConflict, "operation_state_conflict"},
		{"native quota", state.NewOperationLimitError("workflow_active_runs", 5, 6), http.StatusTooManyRequests, "operation_limit_exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeOperationError(rec, tc.err)
			var problem api.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || rec.Code != tc.status || problem.Code != tc.code {
				t.Fatalf("recovery problem: %d %s %v", rec.Code, rec.Body.String(), err)
			}
			if tc.status == http.StatusTooManyRequests && (problem.Limit == nil || *problem.Limit != 5 || problem.Observed == nil || *problem.Observed != 6) {
				t.Fatal("workflow quota omitted numeric bounds")
			}
		})
	}
}

// ADR-521: an explicit cursor takes precedence without silently ignoring invalid values.
func TestOperationsEventCursorPrecedence(t *testing.T) {
	for _, tc := range []struct {
		query, header string
		want          int64
		invalid       bool
	}{
		{"", "", 0, false}, {"", "2", 2, false}, {"?after=0", "2", 0, false},
		{"?after=1", "2", 1, false}, {"?after=", "2", 0, true},
		{"?after=-1", "2", 0, true}, {"", "9223372036854775808", 0, true},
	} {
		t.Run(tc.query+"/"+tc.header, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/"+tc.query, nil)
			req.Header.Set("Last-Event-ID", tc.header)
			got, err := operationEventsCursor(req)
			if (err != nil) != tc.invalid || got != tc.want {
				t.Fatalf("cursor=%d error=%v, want %d invalid=%v", got, err, tc.want, tc.invalid)
			}
		})
	}
}
