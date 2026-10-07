//go:build !no_pg

// adr: 603
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type jobArtifactResponseLoss struct {
	base http.RoundTripper
	lost bool
}

func (r *jobArtifactResponseLoss) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := r.base.RoundTrip(req)
	if err == nil && !r.lost && strings.HasSuffix(req.URL.Path, "/artifacts") && response.StatusCode == 200 {
		r.lost = true
		_ = response.Body.Close()
		return nil, errors.New("lost prepare acknowledgement")
	}
	return response, err
}

type jobArtifactAfterCopy struct {
	storage.StorageBackend
	after func()
}

func (s *jobArtifactAfterCopy) Put(ctx context.Context, key string, reader io.Reader) error {
	if err := s.StorageBackend.Put(ctx, key, reader); err != nil {
		return err
	}
	s.after()
	return nil
}

type jobArtifactHTTPFixture struct {
	store   state.Store
	account state.Account
	app     state.App
	tenant  state.PlatformTenant
	op      state.Operation
	proof   api.OperationJobRuntimeProof
	lease   string
	server  *server
}

func newJobArtifactHTTPFixture(t *testing.T, kind string) jobArtifactHTTPFixture {
	t.Helper()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	var store state.Store = state.NewMemStore()
	if kind == "postgres" {
		store = state.NewPgStore(pgtest.OpenMigrated(t))
	}
	account, err := store.CreateAccount(ctx, "job-file-http@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "job-file-http", Type: state.AppTypeApp})
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
	job, err := store.JobCreate(ctx, account.ID, "export-job", "batch", "registry.example/export:v1", []string{"node", "export.mjs"}, 128, 60, 1, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.(state.JobImageMaterializationStore).JobSetImageMaterialization(ctx, job.ID, job.ImageRef, "ready", "sha256:"+strings.Repeat("a", 64), "jobs/"+job.ID+".ext4", ""); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.(state.PlatformTenantStore).CreatePlatformTenant(ctx, account.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	ops := store.(state.OperationStore)
	def, err := ops.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: api.OperationDefinitionSpec{Name: "export", Job: job.Name, Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"generating"}, InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`{"type":"object","required":["file"],"properties":{"file":{"type":"string"}}}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	op, _, err := ops.AdmitOperation(ctx, state.OperationAdmission{AccountID: account.ID, PlatformTenantID: tenant.ID, DefinitionID: def.ID, IdempotencyKey: "export", Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	instanceID, lease := uuid.NewString(), uuid.NewString()
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAndClaimJobInstance(ctx, instanceID, job.ID, op.JobRunID, 0, string(state.StateColdBooting), 128, node.ID, "", lease, time.Now().Add(time.Minute), "node"); err != nil {
		t.Fatal(err)
	}
	env, err := store.(state.JobOperationStore).OperationJobDispatchEnv(ctx, op.JobRunID, instanceID, lease)
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	return jobArtifactHTTPFixture{store: store, account: account, app: app, tenant: tenant, op: op, lease: lease, server: srv, proof: api.OperationJobRuntimeProof{RunID: op.JobRunID, InstanceID: instanceID, Generation: 1, Attempt: 1, Capability: env["GREGALE_CUSTOMER_OPERATION_JOB_CAPABILITY"]}}
}

func TestOperationJobArtifactHTTP(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			f := newJobArtifactHTTPFixture(t, kind)
			ctx := t.Context()
			provider, declaration := operationArtifactFixture(t, f.server, f.store.(operationArtifactFixtureStore), f.account, f.app, f.op.Scope)
			key, hash, _ := api.GenerateAPIKey()
			if _, err := f.store.CreateAPIKey(ctx, f.account.ID, hash, "job-files", api.ScopesAdminOnly); err != nil {
				t.Fatal(err)
			}
			httpServer := httptest.NewServer(f.server.handler())
			defer httpServer.Close()
			owner := api.NewClient(httpServer.URL, key)
			runtime := api.NewClient(httpServer.URL, "")
			token, err := owner.CreatePlatformTenantAccessToken(ctx, f.tenant.ID, api.CreatePlatformTenantAccessTokenRequest{Name: "browser", Scopes: []string{api.ScopePlatformTenantOperationsRead}})
			if err != nil {
				t.Fatal(err)
			}
			customer := api.NewClient(httpServer.URL, token.Token)
			other, _, err := f.store.(state.PlatformTenantStore).CreatePlatformTenant(ctx, f.account.ID, "bob", "Bob", 100)
			if err != nil {
				t.Fatal(err)
			}
			otherToken, err := owner.CreatePlatformTenantAccessToken(ctx, other.ID, api.CreatePlatformTenantAccessTokenRequest{Name: "browser", Scopes: []string{api.ScopePlatformTenantOperationsRead}})
			if err != nil {
				t.Fatal(err)
			}
			wrong := declaration
			wrong.ReportID = "hash-mismatch"
			wrong.SHA256 = "sha256:" + strings.Repeat("0", 64)
			if _, err := runtime.PrepareJobOperationArtifact(ctx, f.op.ID, f.proof, wrong); err == nil {
				t.Fatal("hash mismatch accepted")
			}
			wrong = declaration
			wrong.ReportID = "foreign"
			wrong.URI = strings.Replace(wrong.URI, f.app.ID, uuid.NewString(), 1)
			if _, err := runtime.PrepareJobOperationArtifact(ctx, f.op.ID, f.proof, wrong); err == nil {
				t.Fatal("foreign source accepted")
			}
			// The server committed the file, but its acknowledgement is lost.
			loss := &jobArtifactResponseLoss{base: http.DefaultTransport}
			runtime.HTTPClient().Transport = loss
			if _, err := runtime.PrepareJobOperationArtifact(ctx, f.op.ID, f.proof, declaration); err == nil || !loss.lost {
				t.Fatal("response loss not exercised", err)
			}
			provider.mu.Lock()
			provider.missing = true
			reads := provider.reads
			provider.mu.Unlock()
			receipt, err := runtime.ReuseJobOperationArtifact(ctx, f.op.ID, f.proof, declaration)
			if err != nil || !receipt.Available || receipt.Artifact == nil {
				t.Fatal("lost response could not be reconciled", err)
			}
			replayed, err := runtime.PrepareJobOperationArtifact(ctx, f.op.ID, f.proof, declaration)
			if err != nil || !replayed.Available || replayed.Artifact.ID != receipt.Artifact.ID {
				t.Fatal("receipt changed", err)
			}
			var csv bytes.Buffer
			if _, err := customer.DownloadPlatformTenantSelfOperationArtifact(ctx, f.op.ID, receipt.Artifact.ID, &csv); err == nil {
				t.Fatal("pending file exposed")
			}
			// Disabling new admission must leave reporting/downloads available.
			policyPath := filepath.Join(t.TempDir(), "preview.json")
			writeOperationPreviewPolicy(t, policyPath, f.account.ID, f.app.ID, f.op.Scope, f.tenant.ID)
			f.server.operationsPreview, err = operations.NewPreviewAdmission(policyPath)
			if err != nil || f.server.operationsPreview.AllowsTenantKind(f.account.ID, f.app.ID, f.op.Scope, f.tenant.ID, operations.ExecutionJob) {
				t.Fatal("HTTP-only policy admitted a Job", err)
			}
			if _, err := runtime.PrepareJobOperationResult(ctx, f.op.ID, f.proof, api.OperationJobReportRequest{ReportID: "result", Result: json.RawMessage(`{"file":"export.csv"}`)}); err != nil {
				t.Fatal(err)
			}
			if err := f.store.JobTaskCompleteClaimedWithLogs(ctx, f.op.JobRunID, 0, f.proof.InstanceID, f.lease, "succeeded", 0, "", "", "", false, time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := customer.DownloadPlatformTenantSelfOperationArtifact(ctx, f.op.ID, receipt.Artifact.ID, &csv); err != nil || csv.String() != "id,count\nalice,1\n" {
				t.Fatalf("retained download=%q %v", csv.String(), err)
			}
			if _, err := api.NewClient(httpServer.URL, otherToken.Token).DownloadPlatformTenantSelfOperationArtifact(ctx, f.op.ID, receipt.Artifact.ID, io.Discard); err == nil {
				t.Fatal("cross-customer download")
			}
			provider.mu.Lock()
			finalReads := provider.reads
			provider.mu.Unlock()
			if finalReads != reads {
				t.Fatal("replay/download read mutable source")
			}
		})
	}
}

func TestOperationJobArtifactOwnerRevokedDuringCopy(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			f := newJobArtifactHTTPFixture(t, kind)
			ctx := t.Context()
			_, declaration := operationArtifactFixture(t, f.server, f.store.(operationArtifactFixtureStore), f.account, f.app, f.op.Scope)
			f.server.WithOperationArtifactStorage(&jobArtifactAfterCopy{StorageBackend: f.server.operationArtifactStorage, after: func() {
				if _, err := f.store.(state.PlatformTenantStore).SetPlatformTenantStatus(ctx, f.account.ID, f.tenant.ID, state.PlatformTenantSuspended); err != nil {
					t.Error(err)
				}
			}})
			httpServer := httptest.NewServer(f.server.handler())
			defer httpServer.Close()
			if _, err := api.NewClient(httpServer.URL, "").PrepareJobOperationArtifact(ctx, f.op.ID, f.proof, declaration); err == nil {
				t.Fatal("revoked owner committed private receipt")
			}
			op, _, err := f.store.(state.JobOperationStore).OperationForJobRun(ctx, f.op.JobRunID)
			if err != nil || len(op.JobArtifactReceipts) != 0 || len(op.ArtifactStorageKeys) != 0 {
				t.Fatal("rejected copy bound", err)
			}
			blob, err := f.store.(state.OperationResultBlobStore).ClaimOperationArtifactCleanup(ctx, "abandoned", time.Now().Add(api.OperationArtifactStagingLifetime+time.Second))
			if err != nil || blob.JobRunID != f.op.JobRunID {
				t.Fatal("abandoned copy lost", err)
			}
		})
	}
}
