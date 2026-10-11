//go:build !no_pg

// adr: 670
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/operations"
	// Test-only downstream orchestration exercises the full workflow HTTP boundary.
	// Production apid remains control-plane-only.
	//nolint:depguard // The integration harness drives schedd-owned workflow execution.
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type workflowUploadFixture struct {
	instanceID               string
	pool                     *pgxpool.Pool
	store                    state.Store
	op                       state.Operation
	app                      state.App
	server                   *server
	runtime, owner, customer *api.Client
	collectCalls             int
}

func newWorkflowUploadFixture(t *testing.T, kind string, timeout time.Duration) *workflowUploadFixture {
	t.Helper()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	var store state.Store = state.NewMemStore()
	var pool *pgxpool.Pool
	if kind == "postgres" {
		pool = pgtest.OpenMigrated(t)
		store = state.NewPgStore(pool)
	}
	acct, err := store.CreateAccount(ctx, "workflow-file-http@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "workflow-file-http", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal([]api.WorkflowSpec{{Name: "export-chain", Steps: []api.WorkflowStepSpec{{Name: "collect", Path: "/collect"}, {Name: "finish", Path: "/finish", DependsOn: []string{"collect"}, Timeout: timeout}}}})
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
	def, err := ops.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: api.OperationDefinitionSpec{Name: "export", Workflow: "export-chain", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`{"type":"object","required":["artifact_id"],"properties":{"artifact_id":{"type":"string"}},"additionalProperties":false}`), ProgressStages: []string{"collect", "finish"}}}})
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
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv.WithOperationArtifactStorage(backend)
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
	t.Cleanup(httpServer.Close)
	runtime := api.NewClient(httpServer.URL, assertion.AccessToken)
	owner := api.NewClient(httpServer.URL, key)

	token, err := owner.CreatePlatformTenantAccessToken(ctx, tenant.ID, api.CreatePlatformTenantAccessTokenRequest{Name: "browser", Scopes: []string{api.ScopePlatformTenantOperationsRead}})
	if err != nil {
		t.Fatal(err)
	}
	return &workflowUploadFixture{store: store, pool: pool, op: op, app: app, server: srv, instanceID: instance.ID, runtime: runtime, owner: owner, customer: api.NewClient(httpServer.URL, token.Token)}
}

func (f *workflowUploadFixture) current(ctx context.Context, t *testing.T) state.Operation {
	t.Helper()
	op, err := f.store.(state.OperationStore).OperationByID(ctx, f.op.AccountID, f.op.PlatformTenantID, f.op.ID)
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func (f *workflowUploadFixture) dispatch(t *testing.T, finish func(api.OperationWorkflowRuntimeProof) (int, []byte, error)) {
	t.Helper()
	executor := workflowArtifactHTTPExecutor{call: func(h map[string]string) (int, []byte, error) {
		generation, _ := strconv.Atoi(h[api.OperationGenerationHeader])
		attempt, _ := strconv.Atoi(h[api.OperationAttemptHeader])
		proof := api.OperationWorkflowRuntimeProof{RunID: h[api.OperationWorkflowRunHeader], StepName: h[api.OperationWorkflowStepHeader], Generation: generation, Attempt: attempt, Capability: h[api.OperationWorkflowCapabilityHeader]}
		if proof.StepName == "collect" {
			f.collectCalls++
			if _, err := f.runtime.ReuseWorkflowOperationUpload(t.Context(), f.op.ID, proof, directJobDeclaration()); err == nil {
				t.Error("non-final action admitted a file")
			}
			return 200, []byte(`{"rows":1}`), nil
		}
		return finish(proof)
	}}
	if err := sched.NewWorkflowOrchestrator(f.store, executor, nil, nil, nil).DispatchTick(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestOperationWorkflowDirectUploadResume(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			f := newWorkflowUploadFixture(t, kind, 0)
			ctx, req := t.Context(), directJobDeclaration()
			var first api.OperationWorkflowRuntimeProof
			var artifactID string
			f.dispatch(t, func(proof api.OperationWorkflowRuntimeProof) (int, []byte, error) {
				first = proof
				before := f.current(ctx, t)
				for i := 0; i < 3; i++ {
					r, err := f.runtime.ReuseWorkflowOperationUpload(ctx, f.op.ID, proof, req)
					if err != nil || r.Available {
						t.Fatal("false receipt", err)
					}
				}
				after := f.current(ctx, t)
				if after.ReportCount != before.ReportCount || after.LatestSequence != before.LatestSequence {
					t.Fatal("absent lookup mutated ledger")
				}
				if _, err := f.owner.ReuseWorkflowOperationUpload(ctx, f.op.ID, proof, req); err == nil {
					t.Fatal("account key replaced workload assertion")
				}
				if _, err := f.customer.UploadWorkflowOperationArtifact(ctx, f.op.ID, proof, req, strings.NewReader(directJobCSV)); err == nil {
					t.Fatal("customer key replaced workload assertion")
				}
				stale := proof
				stale.Attempt++
				if _, err := f.runtime.ReuseWorkflowOperationUpload(ctx, f.op.ID, stale, req); err == nil {
					t.Fatal("stale attempt accepted")
				}
				loss := &directUploadLoss{base: http.DefaultTransport}
				f.runtime.HTTPClient().Transport = loss
				if _, err := f.runtime.UploadWorkflowOperationArtifact(ctx, f.op.ID, proof, req, strings.NewReader(directJobCSV)); err == nil || !loss.lost {
					t.Fatal("lost acknowledgement not exercised", err)
				}
				found, err := f.runtime.ReuseWorkflowOperationUpload(ctx, f.op.ID, proof, req)
				if err != nil || !found.Available || found.Artifact == nil {
					t.Fatal("durable receipt missing", err)
				}
				artifactID = found.Artifact.ID
				if found.Artifact.URI != "operation://"+f.op.ID+"/artifacts/"+artifactID {
					t.Fatal("physical key exposed")
				}
				replay, err := f.runtime.UploadWorkflowOperationArtifact(ctx, f.op.ID, proof, req, strings.NewReader("ignored replay body"))
				if err != nil || replay.Artifact.ID != artifactID {
					t.Fatal("replay consumed bytes", err)
				}
				changed := req
				changed.SHA256 = "sha256:" + strings.Repeat("a", 64)
				if _, err := f.runtime.ReuseWorkflowOperationUpload(ctx, f.op.ID, proof, changed); err == nil {
					t.Fatal("changed payload accepted")
				}
				pending := f.current(ctx, t)
				if pending.ReportCount != before.ReportCount+1 || len(pending.Artifacts) != 0 || len(pending.WorkflowArtifactReceipts) != 1 {
					t.Fatal("duplicate or published receipt")
				}
				if _, err := f.customer.DownloadPlatformTenantSelfOperationArtifact(ctx, f.op.ID, artifactID, io.Discard); err == nil {
					t.Fatal("unconfirmed file public")
				}
				return 503, []byte(`{"error":"uncertain step reply"}`), nil
			})
			if op := f.current(t.Context(), t); op.State != api.OperationRequiresReconciliation || len(op.Artifacts) != 0 {
				t.Fatal("uncertain step published business success")
			}
			if _, err := f.owner.RecoverOperation(ctx, f.app.Slug, f.op.ID, api.OperationRecoveryRequest{RecoveryID: "reuse", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "verified final-action file permits reuse without another business effect"}); err != nil {
				t.Fatal(err)
			}
			var puts int
			f.server.WithOperationArtifactStorage(&workflowCountStorage{StorageBackend: f.server.operationArtifactStorage, put: func() { puts++ }})
			f.dispatch(t, func(proof api.OperationWorkflowRuntimeProof) (int, []byte, error) {
				if _, err := f.runtime.ReuseWorkflowOperationUpload(ctx, f.op.ID, first, req); err == nil {
					t.Fatal("old generation accepted")
				}
				before := f.current(ctx, t)
				r, err := f.runtime.UploadWorkflowOperationArtifact(ctx, f.op.ID, proof, req, strings.NewReader("never transferred on resume"))
				if err != nil || !r.Available || r.Artifact.ID != artifactID {
					t.Fatal("stable resumed receipt lost", err)
				}
				rebound := f.current(ctx, t)
				receipt := rebound.WorkflowArtifactReceipts[req.ReportID]
				if receipt.Generation != proof.Generation || receipt.Attempt != proof.Attempt || rebound.ReportCount != before.ReportCount || rebound.LatestSequence != before.LatestSequence {
					t.Fatal("resume duplicated reports or failed to rebind")
				}
				return 200, []byte(fmt.Sprintf(`{"artifact_id":%q}`, artifactID)), nil
			})
			var downloaded bytes.Buffer
			if _, err := f.customer.DownloadPlatformTenantSelfOperationArtifact(ctx, f.op.ID, artifactID, &downloaded); err != nil || downloaded.String() != directJobCSV {
				t.Fatal("private download changed", err)
			}
			if puts != 0 || f.collectCalls != 1 || f.current(t.Context(), t).State != api.OperationSucceeded {
				t.Fatal("resume repeated confirmed prefix or transfer")
			}
			if _, err := f.runtime.ReuseWorkflowOperationUpload(ctx, f.op.ID, first, req); err == nil {
				t.Fatal("closed proof accepted")
			}
		})
	}
}

type workflowCountStorage struct {
	storage.StorageBackend
	put func()
}

func (s *workflowCountStorage) Put(ctx context.Context, key string, reader io.Reader) error {
	s.put()
	return s.StorageBackend.Put(ctx, key, reader)
}

func TestOperationWorkflowDirectUploadConcurrentCopies(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			f := newWorkflowUploadFixture(t, kind, 0)
			backend := &competingDirectStorage{StorageBackend: f.server.operationArtifactStorage, ready: make(chan struct{})}
			f.server.WithOperationArtifactStorage(backend)
			f.dispatch(t, func(proof api.OperationWorkflowRuntimeProof) (int, []byte, error) {
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				results := make(chan api.OperationWorkflowArtifactResponse, 2)
				var wg sync.WaitGroup
				for i := 0; i < 2; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						r, err := f.runtime.UploadWorkflowOperationArtifact(ctx, f.op.ID, proof, directJobDeclaration(), strings.NewReader(directJobCSV))
						if err != nil {
							t.Error(err)
							return
						}
						results <- r
					}()
				}
				wg.Wait()
				close(results)
				var id string
				count := 0
				for r := range results {
					count++
					if r.Artifact == nil || id != "" && r.Artifact.ID != id {
						t.Fatal("competing receipts")
					}
					id = r.Artifact.ID
				}
				if count != 2 || len(backend.keys) != 2 || backend.keys[0] == backend.keys[1] {
					t.Fatal("copies overwrote one object")
				}
				op := f.current(t.Context(), t)
				if len(op.WorkflowArtifactReceipts) != 1 || len(op.ArtifactStorageKeys) != 1 || len(op.Artifacts) != 0 {
					t.Fatal("duplicate or public binding")
				}
				gc := f.store.(state.OperationResultBlobStore)
				orphan, err := gc.ClaimOperationArtifactCleanup(ctx, "losing-copy", time.Now().Add(api.OperationArtifactStagingLifetime+time.Second))
				if err != nil || orphan.StorageKey == op.ArtifactStorageKeys[id] {
					t.Fatal("winner collected", err)
				}
				if err := gc.CompleteOperationArtifactCleanup(ctx, orphan.ID, orphan.LeaseToken); err != nil {
					t.Fatal(err)
				}
				if _, err := gc.ClaimOperationArtifactCleanup(ctx, "winner", time.Now().Add(api.OperationArtifactStagingLifetime+time.Second)); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("retained winner collected", err)
				}
				return 200, []byte(fmt.Sprintf(`{"artifact_id":%q}`, id)), nil
			})
		})
	}
}

func TestOperationWorkflowDirectUploadIntegrity(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			f := newWorkflowUploadFixture(t, kind, 0)
			f.dispatch(t, func(proof api.OperationWorkflowRuntimeProof) (int, []byte, error) {
				before := f.current(t.Context(), t)
				for _, body := range []string{"short", directJobCSV + "extra", strings.ReplaceAll(directJobCSV, "alice", "other")} {
					if _, err := f.runtime.UploadWorkflowOperationArtifact(t.Context(), f.op.ID, proof, directJobDeclaration(), strings.NewReader(body)); err == nil {
						t.Fatal("unverified bytes retained")
					}
				}
				if after := f.current(t.Context(), t); after.ReportCount != before.ReportCount || len(after.WorkflowArtifactReceipts) != 0 || len(after.ArtifactStorageKeys) != 0 {
					t.Fatal("invalid transfer mutated ledger")
				}
				r, err := f.runtime.UploadWorkflowOperationArtifact(t.Context(), f.op.ID, proof, directJobDeclaration(), strings.NewReader(directJobCSV))
				if err != nil || r.Artifact == nil {
					t.Fatal(err)
				}
				return 200, []byte(fmt.Sprintf(`{"artifact_id":%q}`, r.Artifact.ID)), nil
			})
		})
	}
}

func TestOperationWorkflowDirectUploadFencedDuringIO(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		for _, stage := range []string{"receiving", "copied", "owner_revoked", "deadline"} {
			t.Run(kind+"/"+stage, func(t *testing.T) {
				var timeout time.Duration
				if stage == "deadline" {
					timeout = time.Second
				}
				f := newWorkflowUploadFixture(t, kind, timeout)
				f.dispatch(t, func(proof api.OperationWorkflowRuntimeProof) (int, []byte, error) {
					before := f.current(t.Context(), t)
					ctx := t.Context()
					cancel := func() {
						if _, err := f.store.(state.OperationStore).CancelOperation(ctx, f.op.AccountID, f.op.PlatformTenantID, f.op.ID, proof.Generation); err != nil {
							t.Error(err)
						}
					}
					if stage != "receiving" {
						after := cancel
						if stage == "owner_revoked" {
							after = func() {
								if _, err := f.store.(state.PlatformTenantStore).SetPlatformTenantStatus(ctx, f.op.AccountID, f.op.PlatformTenantID, state.PlatformTenantSuspended); err != nil {
									t.Error(err)
								}
							}
						}
						if stage == "deadline" {
							control, err := f.runtime.GetWorkflowOperationExecutionControl(ctx, f.op.ID, proof)
							if err != nil {
								t.Fatal(err)
							}
							after = func() {
								if remaining := time.Until(control.DeadlineAt); remaining > 0 {
									time.Sleep(remaining)
								}
								time.Sleep(30 * time.Millisecond)
							}
						}
						f.server.WithOperationArtifactStorage(&jobArtifactAfterCopy{StorageBackend: f.server.operationArtifactStorage, after: after})
					}
					var err error
					if stage == "receiving" {
						a := state.OperationWorkflowAuthority{AccountID: f.op.AccountID, AppID: f.op.AppID, InstanceID: f.instanceID, RunID: proof.RunID, StepName: proof.StepName, Generation: proof.Generation, Attempt: proof.Attempt, Capability: proof.Capability}
						_, err = f.server.retainWorkflowOperationUpload(ctx, f.store.(state.OperationWorkflowArtifactStore), f.op.ID, a, directJobDeclaration(), io.NopCloser(&cancelDirectRead{Reader: strings.NewReader(directJobCSV), cancel: cancel}))
					} else {
						_, err = f.runtime.UploadWorkflowOperationArtifact(ctx, f.op.ID, proof, directJobDeclaration(), strings.NewReader(directJobCSV))
					}
					if err == nil {
						t.Fatal("lost native authority committed file")
					}
					after := f.current(t.Context(), t)
					if len(after.WorkflowArtifactReceipts) != 0 || len(after.ArtifactStorageKeys) != 0 || len(after.Artifacts) != 0 || after.ReportCount != before.ReportCount {
						t.Fatal("fenced transfer published a receipt")
					}
					f.server.operationArtifactBudget.mu.Lock()
					transfers := f.server.operationArtifactBudget.transfers
					f.server.operationArtifactBudget.mu.Unlock()
					if transfers != 0 {
						t.Fatal("fenced transfer leaked spool budget")
					}
					if _, err := f.store.(state.OperationResultBlobStore).ClaimOperationArtifactCleanup(ctx, "fenced-copy", time.Now().Add(api.OperationArtifactStagingLifetime+time.Second)); err != nil {
						t.Fatal("abandoned copy lost cleanup intent", err)
					}
					return 503, []byte(`{"error":"unconfirmed"}`), nil
				})
			})
		}
	}
}

func TestOperationWorkflowDirectUploadBlobLockDeadline(t *testing.T) {
	for _, action := range []string{"commit", "reuse"} {
		t.Run(action, func(t *testing.T) {
			f := newWorkflowUploadFixture(t, "postgres", 30*time.Second)

			f.dispatch(t, func(proof api.OperationWorkflowRuntimeProof) (int, []byte, error) {
				ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
				defer cancel()
				a := state.OperationWorkflowAuthority{AccountID: f.op.AccountID, AppID: f.op.AppID, InstanceID: f.instanceID, RunID: proof.RunID, StepName: proof.StepName, Generation: proof.Generation, Attempt: proof.Attempt, Capability: proof.Capability}
				store := f.store.(state.OperationWorkflowArtifactStore)
				declaration := operations.WorkflowUploadArtifactDeclaration(f.op.ID, a.RunID, a.StepName, directJobDeclaration())
				blob, _, err := store.ReserveWorkflowOperationArtifact(ctx, f.op.ID, a, declaration)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.server.operationArtifactStorage.Put(ctx, blob.StorageKey, strings.NewReader(directJobCSV)); err != nil {
					t.Fatal(err)
				}
				if action == "reuse" {
					if _, err := store.PrepareVerifiedWorkflowOperationArtifact(ctx, f.op.ID, a, declaration, blob.ID); err != nil {
						t.Fatal(err)
					}
				}
				before := f.current(t.Context(), t)
				control, err := f.runtime.GetWorkflowOperationExecutionControl(ctx, f.op.ID, proof)
				if err != nil {
					t.Fatal(err)
				}
				lock, err := f.pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = lock.Rollback(ctx) }()
				var blobID pgtype.UUID
				if err := blobID.Scan(blob.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := sqlc.New().LockCustomerOperationBlob(ctx, lock, blobID); err != nil {
					t.Fatal(err)
				}
				completed := make(chan error, 1)
				var workers sync.WaitGroup
				workers.Add(1)
				go func() {
					defer workers.Done()
					var err error
					if action == "reuse" {
						_, err = store.ReuseWorkflowOperationArtifact(ctx, f.op.ID, a, declaration)
					} else {
						_, err = store.PrepareVerifiedWorkflowOperationArtifact(ctx, f.op.ID, a, declaration, blob.ID)
					}
					completed <- err
				}()
				defer func() {
					cancel()
					cleanup, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					defer cleanupCancel()
					_ = lock.Rollback(cleanup)
					workers.Wait()
				}()
				// Observe the actual blob-lock wait. A short control-query timeout
				// can also mean a slow connection or query, before the file transaction
				// has acquired the run lock. The setup budget does not change the
				// assertion: release this lock only after the fixed deadline expires.
				for {
					var blocked bool
					if err := f.pool.QueryRow(ctx, `SELECT EXISTS (
						SELECT 1 FROM pg_stat_activity WHERE datname=current_database()
						AND $1::integer=ANY(pg_blocking_pids(pid))
						AND query LIKE '%LockCustomerOperationBlob%'
					)`, int32(lock.Conn().PgConn().PID())).Scan(&blocked); err != nil {
						t.Fatal(err)
					}
					if blocked {
						break
					}
					select {
					case err := <-completed:
						t.Fatal("file transaction did not wait on the blob lock", err)
					case <-ctx.Done():
						t.Fatal("file transaction did not reach the blob lock", ctx.Err())
					case <-time.After(5 * time.Millisecond):
					}
				}
				if remaining := time.Until(control.DeadlineAt); remaining > 0 {
					time.Sleep(remaining)
				}
				time.Sleep(30 * time.Millisecond)
				if err := lock.Rollback(ctx); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-completed:
					if !errors.Is(err, state.ErrOperationStaleAttempt) {
						t.Fatal("blob lock wait extended step authority", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("file transaction remained blocked")
				}
				after := f.current(t.Context(), t)
				if after.ReportCount != before.ReportCount || after.LatestSequence != before.LatestSequence || len(after.WorkflowArtifactReceipts) != len(before.WorkflowArtifactReceipts) || len(after.Artifacts) != 0 {
					t.Fatal("expired transaction changed or published the receipt")
				}
				return 503, nil, nil
			})
		})
	}
}
