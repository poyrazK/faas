//go:build !no_pg

// adr: 669
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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type httpUploadFixture struct {
	store                    state.Store
	op                       state.Operation
	inv                      state.Invocation
	proof                    api.OperationRuntimeProof
	server                   *server
	runtime, customer, owner *api.Client
}

func newHTTPUploadFixture(t *testing.T, kind string) httpUploadFixture {
	t.Helper()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	var store state.Store = state.NewMemStore()
	if kind == "postgres" {
		store = state.NewPgStore(pgtest.OpenMigrated(t))
	}
	account, err := store.CreateAccount(ctx, "http-upload@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "http-upload", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.(state.PlatformTenantStore).CreatePlatformTenant(ctx, account.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	ops := store.(state.OperationStore)
	def, err := ops.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"generating"}, Recovery: api.OperationRecoveryReconcile, InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`{"type":"object","required":["artifact_id"],"properties":{"artifact_id":{"type":"string"}}}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	op, _, err := ops.AdmitOperation(ctx, state.OperationAdmission{AccountID: account.ID, PlatformTenantID: tenant.ID, DefinitionID: def.ID, IdempotencyKey: "export", Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, node.ID, "")
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
	signer, err := workloadidentity.NewSigner(private, workloadidentity.DefaultIssuer, "http-upload", workloadidentity.DefaultTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	srv.operationsWorkloadVerifier, err = workloadidentity.NewVerifier(signer.JWKS(), workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv.WithOperationArtifactStorage(backend)
	assertion, err := signer.Mint(time.Now(), account.ID, app.ID, instance.ID, workloadidentity.OperationsAudience)
	if err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(srv.handler())
	t.Cleanup(host.Close)
	key, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(ctx, account.ID, hash, "owner", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	owner := api.NewClient(host.URL, key)
	token, err := owner.CreatePlatformTenantAccessToken(ctx, tenant.ID, api.CreatePlatformTenantAccessTokenRequest{Name: "browser", Scopes: []string{api.ScopePlatformTenantOperationsRead}})
	if err != nil {
		t.Fatal(err)
	}
	return httpUploadFixture{store: store, op: op, inv: inv, proof: api.OperationRuntimeProof{InvocationID: inv.ID, Attempt: inv.Attempts, Capability: metadata[api.OperationCapabilityHeader]}, server: srv, runtime: api.NewClient(host.URL, assertion.AccessToken), customer: api.NewClient(host.URL, token.Token), owner: owner}
}

func (f httpUploadFixture) current(t *testing.T) state.Operation {
	t.Helper()
	op, err := f.store.(state.OperationStore).OperationByID(t.Context(), f.op.AccountID, f.op.PlatformTenantID, f.op.ID)
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func TestOperationHTTPDirectUpload(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			f := newHTTPUploadFixture(t, kind)
			ctx, req := t.Context(), directJobDeclaration()
			before := f.current(t)
			for i := 0; i < 3; i++ {
				absent, err := f.runtime.ReuseOperationUpload(ctx, f.op.ID, f.proof, req)
				if err != nil || absent.Available {
					t.Fatal("false receipt", err)
				}
			}
			if after := f.current(t); after.ReportCount != before.ReportCount || after.LatestSequence != before.LatestSequence {
				t.Fatal("lookup mutated operation")
			}
			if _, err := f.owner.ReuseOperationUpload(ctx, f.op.ID, f.proof, req); err == nil {
				t.Fatal("account token replaced workload proof")
			}
			if _, err := f.customer.UploadOperationArtifact(ctx, f.op.ID, f.proof, req, strings.NewReader(directJobCSV)); err == nil {
				t.Fatal("customer token replaced workload proof")
			}
			stale := f.proof
			stale.Attempt++
			if _, err := f.runtime.ReuseOperationUpload(ctx, f.op.ID, stale, req); err == nil {
				t.Fatal("stale proof accepted")
			}
			loss := &directUploadLoss{base: http.DefaultTransport}
			f.runtime.HTTPClient().Transport = loss
			if _, err := f.runtime.UploadOperationArtifact(ctx, f.op.ID, f.proof, req, strings.NewReader(directJobCSV)); err == nil || !loss.lost {
				t.Fatal("lost acknowledgement not exercised", err)
			}
			found, err := f.runtime.ReuseOperationUpload(ctx, f.op.ID, f.proof, req)
			if err != nil || !found.Available || found.Artifact == nil {
				t.Fatal("receipt missing", err)
			}
			a := found.Artifact
			if a.URI != "operation://"+f.op.ID+"/artifacts/"+a.ID {
				t.Fatal("storage key exposed")
			}
			replay, err := f.runtime.UploadOperationArtifact(ctx, f.op.ID, f.proof, req, strings.NewReader("never read"))
			if err != nil || replay.Artifact.ID != a.ID {
				t.Fatal("receipt changed", err)
			}
			changed := req
			changed.SHA256 = "sha256:" + strings.Repeat("a", 64)
			if _, err := f.runtime.ReuseOperationUpload(ctx, f.op.ID, f.proof, changed); err == nil {
				t.Fatal("payload conflict accepted")
			}
			if op := f.current(t); op.State != api.OperationRunning || op.ReportCount != before.ReportCount+1 || len(op.Artifacts) != 1 {
				t.Fatal("upload settled or duplicated work")
			}
			if _, err := f.customer.DownloadPlatformTenantSelfOperationArtifact(ctx, f.op.ID, a.ID, io.Discard); err == nil {
				t.Fatal("file exposed before completion")
			}
			if err := f.store.CompleteKeyedInvocation(ctx, f.inv.ID, f.inv.Attempts, []byte(fmt.Sprintf(`{"artifact_id":%q}`, a.ID))); err != nil {
				t.Fatal(err)
			}
			var downloaded bytes.Buffer
			if _, err := f.customer.DownloadPlatformTenantSelfOperationArtifact(ctx, f.op.ID, a.ID, &downloaded); err != nil || downloaded.String() != directJobCSV {
				t.Fatal("private download changed", err)
			}
			if _, err := f.runtime.ReuseOperationUpload(ctx, f.op.ID, f.proof, req); err == nil {
				t.Fatal("completed invocation proof accepted")
			}
		})
	}
}

func TestOperationHTTPDirectUploadConcurrentCopies(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			f := newHTTPUploadFixture(t, kind)
			backend := &competingDirectStorage{StorageBackend: f.server.operationArtifactStorage, ready: make(chan struct{})}
			f.server.WithOperationArtifactStorage(backend)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			results := make(chan api.OperationArtifactUploadResponse, 2)
			var wg sync.WaitGroup
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					r, err := f.runtime.UploadOperationArtifact(ctx, f.op.ID, f.proof, directJobDeclaration(), strings.NewReader(directJobCSV))
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
				t.Fatal("copies overwrote storage")
			}
			op := f.current(t)
			if len(op.Artifacts) != 1 || len(op.ArtifactStorageKeys) != 1 || op.ReportCount != 1 {
				t.Fatal("duplicate binding")
			}
			gc := f.store.(state.OperationResultBlobStore)
			orphan, err := gc.ClaimOperationArtifactCleanup(ctx, "loser", time.Now().Add(api.OperationArtifactStagingLifetime+time.Second))
			if err != nil || orphan.StorageKey == op.ArtifactStorageKeys[id] {
				t.Fatal("winner collected", err)
			}
			if err := gc.CompleteOperationArtifactCleanup(ctx, orphan.ID, orphan.LeaseToken); err != nil {
				t.Fatal(err)
			}
			if _, err := gc.ClaimOperationArtifactCleanup(ctx, "winner", time.Now().Add(api.OperationArtifactStagingLifetime+time.Second)); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("pinned receipt collected", err)
			}
		})
	}
}

func TestOperationHTTPDirectUploadCancellationFence(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		for _, stage := range []string{"receiving", "copied"} {
			t.Run(kind+"/"+stage, func(t *testing.T) {
				f := newHTTPUploadFixture(t, kind)
				cancel := func() {
					if _, err := f.store.(state.OperationStore).CancelOperation(t.Context(), f.op.AccountID, f.op.PlatformTenantID, f.op.ID, 1); err != nil {
						t.Error(err)
					}
				}
				if stage == "copied" {
					f.server.WithOperationArtifactStorage(&jobArtifactAfterCopy{StorageBackend: f.server.operationArtifactStorage, after: cancel})
				}
				body := io.NopCloser(strings.NewReader(directJobCSV))
				if stage == "receiving" {
					body = io.NopCloser(&cancelDirectRead{Reader: body, cancel: cancel})
				}
				a := state.OperationExecutionAuthority{AccountID: f.op.AccountID, AppID: f.op.AppID, InstanceID: f.inv.InstanceID, InvocationID: f.inv.ID, Attempt: f.proof.Attempt, Capability: f.proof.Capability}
				_, err := f.server.retainOperationUpload(t.Context(), f.store.(state.OperationArtifactStore), f.current(t), a, directJobDeclaration(), body, func() error { return nil })
				if !errors.Is(err, state.ErrConflict) {
					t.Fatal("cancelled upload committed", err)
				}
				if op := f.current(t); len(op.Artifacts) != 0 || op.ReportCount != 0 || len(op.ArtifactStorageKeys) != 0 {
					t.Fatal("cancelled upload retained receipt")
				}
				if f.server.operationArtifactBudget.bytes != 0 || f.server.operationArtifactBudget.transfers != 0 {
					t.Fatal("cancelled transfer leaked budget")
				}
			})
		}
	}
}

func TestOperationHTTPDirectUploadIntegrityAndCancelledReplay(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			f := newHTTPUploadFixture(t, kind)
			ctx, req := t.Context(), directJobDeclaration()
			for _, body := range []string{"short", directJobCSV + "extra", strings.ReplaceAll(directJobCSV, "alice", "other")} {
				if _, err := f.runtime.UploadOperationArtifact(ctx, f.op.ID, f.proof, req, strings.NewReader(body)); err == nil {
					t.Fatal("unverified bytes retained")
				}
			}
			if op := f.current(t); op.ReportCount != 0 || len(op.Artifacts) != 0 {
				t.Fatal("failed verification changed ledger")
			}
			if _, err := f.runtime.ReportOperationProgress(ctx, f.op.ID, f.proof, api.OperationReportRequest{ReportID: "progress", Stage: "generating", Total: 1}); err != nil {
				t.Fatal(err)
			}
			collision := req
			collision.ReportID = "progress"
			if _, err := f.runtime.ReuseOperationUpload(ctx, f.op.ID, f.proof, collision); err == nil {
				t.Fatal("progress report reused as a file")
			}
			found, err := f.runtime.UploadOperationArtifact(ctx, f.op.ID, f.proof, req, strings.NewReader(directJobCSV))
			if err != nil || found.Artifact == nil {
				t.Fatal(err)
			}
			if _, err := f.store.(state.OperationStore).CancelOperation(ctx, f.op.AccountID, f.op.PlatformTenantID, f.op.ID, 1); err != nil {
				t.Fatal(err)
			}
			replay, err := f.runtime.ReuseOperationUpload(ctx, f.op.ID, f.proof, req)
			if err != nil || !replay.Available || replay.Artifact.ID != found.Artifact.ID {
				t.Fatal("committed cancelled receipt lost", err)
			}
			newFile := req
			newFile.ReportID = "second"
			newFile.Name = "second.csv"
			if _, err := f.runtime.UploadOperationArtifact(ctx, f.op.ID, f.proof, newFile, strings.NewReader(directJobCSV)); err == nil {
				t.Fatal("cancelled new file retained")
			}
		})
	}
}

func TestOperationHTTPDirectUploadClaimLostAfterCopy(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			f := newHTTPUploadFixture(t, kind)
			f.server.WithOperationArtifactStorage(&jobArtifactAfterCopy{StorageBackend: f.server.operationArtifactStorage, after: func() {
				reaper := f.store.(interface {
					RequeueExpiredInvocations(context.Context, time.Time, int) (int, error)
				})
				if n, err := reaper.RequeueExpiredInvocations(t.Context(), f.inv.LeaseExpiresAt.Add(time.Second), 100); err != nil || n != 1 {
					t.Error("claim not reaped", n, err)
				}
			}})
			if _, err := f.runtime.UploadOperationArtifact(t.Context(), f.op.ID, f.proof, directJobDeclaration(), strings.NewReader(directJobCSV)); err == nil {
				t.Fatal("lost claim retained file")
			}
			if op := f.current(t); op.State != api.OperationRequiresReconciliation || len(op.Artifacts) != 0 || len(op.ArtifactStorageKeys) != 0 {
				t.Fatal("unknown HTTP outcome replayed or published")
			}
			gc := f.store.(state.OperationResultBlobStore)
			if _, err := gc.ClaimOperationArtifactCleanup(t.Context(), "lost-claim", time.Now().Add(api.OperationArtifactStagingLifetime+time.Second)); err != nil {
				t.Fatal("abandoned copy lost cleanup intent", err)
			}
		})
	}
}
