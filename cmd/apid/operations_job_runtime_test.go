// adr: 645
package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestJobOperationRuntimeBoundary(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "job-runtime@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "job-operation", Type: state.AppTypeApp})
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
	job, err := store.JobCreate(ctx, account.ID, "export-job", "batch", "registry.example/export:v1", []string{"node", "export.mjs"}, 128, 60, 1, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.JobSetImageMaterialization(ctx, job.ID, job.ImageRef, "ready", "sha256:"+strings.Repeat("a", 64), "jobs/"+job.ID+".ext4", ""); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: api.OperationDefinitionSpec{Name: "export", Job: job.Name, Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"generating"}, InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`{"type":"object","required":["file"],"properties":{"file":{"type":"string"}}}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	op, _, err := store.AdmitOperation(ctx, state.OperationAdmission{AccountID: account.ID, PlatformTenantID: tenant.ID, DefinitionID: def.ID, IdempotencyKey: "export", Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	instanceID, lease := uuid.NewString(), uuid.NewString()
	node, _ := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if _, err := store.CreateAndClaimJobInstance(ctx, instanceID, job.ID, op.JobRunID, 0, string(state.StateColdBooting), 128, node.ID, "", lease, time.Now().Add(time.Minute), "node"); err != nil {
		t.Fatal(err)
	}
	env, err := store.OperationJobDispatchEnv(ctx, op.JobRunID, instanceID, lease)
	if err != nil {
		t.Fatal(err)
	}
	proof := http.Header{api.OperationJobRunHeader: {op.JobRunID}, api.OperationJobInstanceHeader: {instanceID}, api.OperationJobCapabilityHeader: {env["GREGALE_CUSTOMER_OPERATION_JOB_CAPABILITY"]}, api.OperationGenerationHeader: {"1"}, api.OperationAttemptHeader: {"1"}}
	server := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	handler := server.handler()
	for _, tc := range []struct {
		name, kind, body string
		headers          func(http.Header)
		status           int
	}{
		{name: "live control", kind: "control", status: 200},
		{name: "typed result", kind: "result", body: `{"report_id":"result","result":{"file":"export.csv"}}`, status: 200},
		{name: "invalid output", kind: "result", body: `{"report_id":"bad","result":{"file":42}}`, status: 409},
		{name: "account bearer", kind: "control", headers: func(h http.Header) { h.Set("Authorization", "Bearer customer-account-token") }, status: 404},
		{name: "HTTP proof", kind: "control", headers: func(h http.Header) { h.Set(api.OperationCapabilityHeader, "http-capability") }, status: 404},
		{name: "duplicate capability", kind: "control", headers: func(h http.Header) { h.Add(api.OperationJobCapabilityHeader, "duplicate") }, status: 404},
		{name: "wrong instance", kind: "control", headers: func(h http.Header) { h.Set(api.OperationJobInstanceHeader, uuid.NewString()) }, status: 404},
		{name: "stale generation", kind: "control", headers: func(h http.Header) { h.Set(api.OperationGenerationHeader, "2") }, status: 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			method := http.MethodPost
			if tc.kind == "control" {
				method = http.MethodGet
			}
			req := httptest.NewRequest(method, "/v1/runtime/job-operations/"+op.ID+"/"+tc.kind, strings.NewReader(tc.body))
			req.Header = proof.Clone()
			if tc.headers != nil {
				tc.headers(req.Header)
			}
			out := httptest.NewRecorder()
			handler.ServeHTTP(out, req)
			if out.Code != tc.status {
				t.Fatalf("status=%d body=%s", out.Code, out.Body.String())
			}
			if strings.Contains(out.Body.String(), proof.Get(api.OperationJobCapabilityHeader)) {
				t.Fatal("capability leaked")
			}
		})
	}
	got, _ := store.OperationByID(ctx, account.ID, tenant.ID, op.ID)
	if got.State != api.OperationRunning || len(got.Result) > 0 {
		t.Fatal("runtime settled business before host exit")
	}
}
