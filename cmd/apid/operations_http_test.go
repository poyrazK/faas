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
	spec := api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant,
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
