//go:build !no_pg

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type workflowArtifactHTTPExecutor struct {
	call func(map[string]string) (int, []byte, error)
}

func (e workflowArtifactHTTPExecutor) ExecuteStep(context.Context, string, string, string, map[string]string, []byte, time.Duration) (int, []byte, error) {
	return 0, nil, errors.New("workflow identity missing")
}
func (e workflowArtifactHTTPExecutor) ExecuteWorkflowStep(_ context.Context, _ string, _ sched.WorkflowStepIdentity, _ string, _ string, h map[string]string, _ []byte, _ time.Duration, _ string, _ int64) (int, []byte, error) {
	return e.call(h)
}

func TestOperationWorkflowArtifactHTTP(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
			ctx := t.Context()
			var store state.Store = state.NewMemStore()
			if kind == "postgres" {
				store = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			acct, err := store.CreateAccount(ctx, "workflow-file-http@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "workflow-file-http", Type: state.AppTypeApp})
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal([]api.WorkflowSpec{{Name: "export-chain", Steps: []api.WorkflowStepSpec{{Name: "finish", Path: "/finish"}}}})
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Workflows: raw})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
				t.Fatal(err)
			}
			tenants := store.(state.PlatformTenantStore)
			tenant, _, err := tenants.CreatePlatformTenant(ctx, acct.ID, "alice", "Alice", 100)
			if err != nil {
				t.Fatal(err)
			}
			surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: acct.ID, AppID: app.ID, Name: "alice", CertKind: state.CertKindPerHostSAN}, api.MustLimitsFor(acct.Plan))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tenants.LinkPlatformTenantSurface(ctx, acct.ID, tenant.ID, surface.ID); err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive); err != nil {
				t.Fatal(err)
			}
			ops := store.(state.OperationStore)
			def, err := ops.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: api.OperationDefinitionSpec{Name: "export", Workflow: "export-chain", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`{"type":"object","required":["file"],"properties":{"file":{"type":"string"}},"additionalProperties":false}`), ProgressStages: []string{"finish"}}}})
			if err != nil {
				t.Fatal(err)
			}
			op, _, err := ops.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: tenant.ID, IdempotencyKey: "csv", Input: []byte(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			nodeID := uuid.NewString()
			if node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName); err == nil {
				nodeID = node.ID
			}
			instance, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, nodeID, "")
			if err != nil {
				t.Fatal(err)
			}
			srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
			provider, declaration := operationArtifactFixture(t, srv, store.(operationArtifactFixtureStore), acct, app, dep.Scope)
			private, err := rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				t.Fatal(err)
			}
			signer, err := workloadidentity.NewSigner(private, workloadidentity.DefaultIssuer, "workflow-files", workloadidentity.DefaultTokenTTL)
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
			key, hash, _ := api.GenerateAPIKey()
			if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "workflow-files", api.ScopesAdminOnly); err != nil {
				t.Fatal(err)
			}
			httpServer := httptest.NewServer(srv.handler())
			defer httpServer.Close()
			runtime := api.NewClient(httpServer.URL, assertion.AccessToken)
			owner := api.NewClient(httpServer.URL, key)
			requests := 0
			var firstProof api.OperationWorkflowRuntimeProof
			var artifactID string
			executor := workflowArtifactHTTPExecutor{call: func(h map[string]string) (int, []byte, error) {
				requests++
				generation, _ := strconv.Atoi(h[api.OperationGenerationHeader])
				attempt, _ := strconv.Atoi(h[api.OperationAttemptHeader])
				proof := api.OperationWorkflowRuntimeProof{RunID: h[api.OperationWorkflowRunHeader], StepName: h[api.OperationWorkflowStepHeader], Generation: generation, Attempt: attempt, Capability: h[api.OperationWorkflowCapabilityHeader]}
				observation, err := runtime.GetWorkflowOperationExecutionControl(ctx, op.ID, proof)
				if err != nil || observation.OperationID != op.ID || observation.WorkflowRunID != op.WorkflowRunID || observation.WorkflowStep != "finish" || observation.Generation != generation || observation.Attempt != attempt || observation.CancellationRequested {
					t.Fatalf("workflow HTTP control=%+v %v", observation, err)
				}
				request := httptest.NewRequest(http.MethodGet, "/v1/runtime/workflow-operations/"+op.ID+"/control", nil)
				request.Header.Set("Authorization", "Bearer "+assertion.AccessToken)
				for name, value := range h {
					request.Header.Set(name, value)
				}
				response := httptest.NewRecorder()
				srv.handler().ServeHTTP(response, request)
				if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || bytes.Contains(response.Body.Bytes(), []byte(proof.Capability)) || bytes.Contains(response.Body.Bytes(), []byte("invocation_id")) {
					t.Fatal("control leaked proof, invented an HTTP claim or was cacheable", response.Code)
				}
				if _, err := owner.GetWorkflowOperationExecutionControl(ctx, op.ID, proof); err == nil {
					t.Fatal("account key substituted for control workload assertion")
				}
				if requests == 1 {
					firstProof = proof
					if _, err := owner.ReuseWorkflowOperationArtifact(ctx, op.ID, proof, declaration); err == nil {
						t.Fatal("account key substituted for workload assertion")
					}
					receipt, err := runtime.ReuseWorkflowOperationArtifact(ctx, op.ID, proof, declaration)
					if err != nil || receipt.Available {
						t.Fatalf("preflight=%+v %v", receipt, err)
					}
					// A source hash mismatch cannot create a durable verified receipt.
					mismatch := declaration
					mismatch.SHA256 = "sha256:" + string(bytes.Repeat([]byte("0"), 64))
					if _, err := runtime.PrepareWorkflowOperationArtifact(ctx, op.ID, proof, mismatch); err == nil {
						t.Fatal("unverified bytes retained")
					}
					receipt, err = runtime.PrepareWorkflowOperationArtifact(ctx, op.ID, proof, declaration)
					if err != nil || !receipt.Available {
						t.Fatalf("prepare=%+v %v", receipt, err)
					}
					artifactID = receipt.Artifact.ID
					// Source deletion after retaining cannot force an upload on a retry/resume.
					provider.mu.Lock()
					provider.missing = true
					provider.mu.Unlock()
					if _, err := runtime.PrepareWorkflowOperationArtifact(ctx, op.ID, proof, declaration); err != nil {
						t.Fatal("lost response required original source", err)
					}
					if _, err := owner.DownloadOperationArtifact(ctx, app.Slug, op.ID, artifactID, io.Discard); err == nil {
						t.Fatal("unconfirmed file downloadable")
					}
					mixed := httptest.NewRequest(http.MethodPost, "/v1/runtime/operations/"+op.ID+"/artifacts", bytes.NewBufferString(`{}`))
					mixed.Header.Set("Authorization", "Bearer "+assertion.AccessToken)
					for name, value := range h {
						mixed.Header.Set(name, value)
					}
					rec := httptest.NewRecorder()
					srv.handler().ServeHTTP(rec, mixed)
					if rec.Code != http.StatusUnauthorized {
						t.Fatal("workflow proof accepted as HTTP invocation", rec.Code)
					}
					return 503, []byte(`{"error":"response lost"}`), nil
				}
				if _, err := runtime.GetWorkflowOperationExecutionControl(ctx, op.ID, firstProof); err == nil {
					t.Fatal("old proof read control after approved resume")
				}
				if _, err := runtime.ReuseWorkflowOperationArtifact(ctx, op.ID, firstProof, declaration); err == nil {
					t.Fatal("stale native attempt reused")
				}
				receipt, err := runtime.ReuseWorkflowOperationArtifact(ctx, op.ID, proof, declaration)
				if err != nil || !receipt.Available || receipt.Artifact.ID != artifactID {
					t.Fatalf("resumed copy=%+v %v", receipt, err)
				}
				return 200, []byte(`{"file":"export.csv"}`), nil
			}}
			orchestrator := sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil)
			if err := orchestrator.DispatchTick(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := owner.GetOperation(ctx, app.Slug, op.ID)
			if err != nil || got.State != api.OperationRequiresReconciliation || len(got.Artifacts) != 0 {
				t.Fatalf("uncertain copy=%+v %v", got, err)
			}
			if _, err := owner.RecoverOperation(ctx, app.Slug, op.ID, api.OperationRecoveryRequest{RecoveryID: "reuse", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "retained verified copy allows final action without external write"}); err != nil {
				t.Fatal(err)
			}
			if err := orchestrator.DispatchTick(ctx); err != nil {
				t.Fatal(err)
			}
			var csv bytes.Buffer
			if _, err := owner.DownloadOperationArtifact(ctx, app.Slug, op.ID, artifactID, &csv); err != nil || csv.String() != "id,count\nalice,1\n" {
				t.Fatalf("retained download=%q %v", csv.String(), err)
			}
			provider.mu.Lock()
			reads := provider.reads
			provider.mu.Unlock()
			if reads != 2 || requests != 2 {
				t.Fatalf("recovery reread source: reads=%d requests=%d", reads, requests)
			}
		})
	}
}
