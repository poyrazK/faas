//go:build !no_pg

// adr: 20261018183000
package apidserver_test

// A running job Operation starts children with its job run proof: the same
// key replays the same child, the children are listed for the job, and a
// cancelled parent starts no more.

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

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOperationChildrenFromJobParent(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
			ctx := t.Context()
			var store state.Store = state.NewMemStore()
			if kind == "postgres" {
				pool := pgtest.OpenMigrated(t)
				if err := db.MigrateUp(ctx, pool); err != nil {
					t.Fatal(err)
				}
				store = state.NewPgStore(pool)
			}
			account, err := store.CreateAccount(ctx, "job-children@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("FAAS_OPERATION_CHILDREN_ACCOUNTS", account.ID)
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "job-children", Type: state.AppTypeApp})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:app"})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
				t.Fatal(err)
			}
			job, err := store.JobCreate(ctx, account.ID, "import-job", "batch", "registry.example/import:v1", []string{"node", "import.mjs"}, 128, 60, 1, 3, nil)
			if err != nil {
				t.Fatal(err)
			}
			materializer, ok := store.(interface {
				JobSetImageMaterialization(ctx context.Context, id, sourceRef, status, resolvedDigest, storageKey, failure string) (state.Job, error)
			})
			if !ok {
				t.Fatal("store cannot materialize job images")
			}
			if _, err := materializer.JobSetImageMaterialization(ctx, job.ID, job.ImageRef, "ready", "sha256:"+strings.Repeat("a", 64), "jobs/"+job.ID+".ext4", ""); err != nil {
				t.Fatal(err)
			}
			tenant, _, err := store.(state.PlatformTenantStore).CreatePlatformTenant(ctx, account.ID, "alice", "Alice", 100)
			if err != nil {
				t.Fatal(err)
			}
			ops := store.(state.OperationStore)
			define := func(name, jobName, path string) state.OperationDefinition {
				def, err := ops.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID,
					Spec: api.OperationDefinitionSpec{Name: name, Job: jobName, Method: "POST", Path: path, Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"working"},
						InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`{"type":"object"}`)}}})
				if err != nil {
					t.Fatal(err)
				}
				return def
			}
			parentDef := define("import", job.Name, "/imports")
			define("import-file", "", "/imports/file")
			parent, _, err := ops.AdmitOperation(ctx, state.OperationAdmission{AccountID: account.ID, PlatformTenantID: tenant.ID, DefinitionID: parentDef.ID, IdempotencyKey: "import-1", Input: []byte(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			instanceID, lease := uuid.NewString(), uuid.NewString()
			node, _ := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if _, err := store.CreateAndClaimJobInstance(ctx, instanceID, job.ID, parent.JobRunID, 0, string(state.StateColdBooting), 128, node.ID, "", lease, time.Now().Add(time.Minute), "node"); err != nil {
				t.Fatal(err)
			}
			env, err := store.(state.JobOperationStore).OperationJobDispatchEnv(ctx, parent.JobRunID, instanceID, lease)
			if err != nil {
				t.Fatal(err)
			}
			proof := http.Header{api.OperationJobRunHeader: {parent.JobRunID}, api.OperationJobInstanceHeader: {instanceID},
				api.OperationJobCapabilityHeader: {env["GREGALE_CUSTOMER_OPERATION_JOB_CAPABILITY"]}, api.OperationGenerationHeader: {"1"}, api.OperationAttemptHeader: {"1"}}
			server := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
			(*server.Operation()).AdmissionEnabled = true
			handler := server.Handler()
			call := func(method, key string, mutate func(http.Header)) *httptest.ResponseRecorder {
				var body io.Reader
				if method == http.MethodPost {
					raw, _ := json.Marshal(api.OperationChildRequest{Operation: "import-file", Key: key, Input: json.RawMessage(`{}`)})
					body = bytes.NewReader(raw)
				}
				req := httptest.NewRequest(method, "/v1/runtime/job-operations/"+parent.ID+"/children", body)
				req.Header = proof.Clone()
				if mutate != nil {
					mutate(req.Header)
				}
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				return rec
			}
			var first, replay api.OperationChildStartResponse
			if rec := call(http.MethodPost, "file-1", nil); rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &first) != nil {
				t.Fatalf("start: HTTP %d %s", rec.Code, rec.Body.String())
			}
			if rec := call(http.MethodPost, "file-1", nil); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &replay) != nil || !replay.Replayed || replay.Operation.ID != first.Operation.ID {
				t.Fatalf("replay: HTTP %d %s", rec.Code, rec.Body.String())
			}
			if rec := call(http.MethodPost, "file-2", func(h http.Header) { h.Set(api.OperationJobInstanceHeader, uuid.NewString()) }); rec.Code < 400 {
				t.Fatalf("foreign instance started a child: HTTP %d", rec.Code)
			}
			var list api.OperationChildrenResponse
			if rec := call(http.MethodGet, "", nil); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Children) != 1 || list.Children[0].Key != "file-1" {
				t.Fatalf("list: HTTP %d %s", rec.Code, rec.Body.String())
			}
			if _, err := ops.CancelOperation(ctx, account.ID, "", parent.ID, parent.Generation); err != nil {
				t.Fatal(err)
			}
			if rec := call(http.MethodPost, "file-3", nil); rec.Code != http.StatusConflict {
				t.Fatalf("cancelled parent started a child: HTTP %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}
