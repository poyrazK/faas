//go:build !no_pg

// adr: 604 — HTTP qualification cannot grant native execution admission.
package main

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

func TestOperationsExecutionAdmission(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
			ctx := t.Context()
			var store state.Store = state.NewMemStore()
			if backend == "postgres" {
				store = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			account, err := store.CreateAccount(ctx, "execution-admission@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "execution-admission", Type: state.AppTypeApp})
			if err != nil {
				t.Fatal(err)
			}
			workflow, _ := json.Marshal([]api.WorkflowSpec{{Name: "export-chain", Steps: []api.WorkflowStepSpec{{Name: "finish", Path: "/finish"}}}})
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Workflows: workflow})
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
			specs := []api.OperationDefinitionSpec{}
			defs := []state.OperationDefinition{}
			for _, kind := range []string{operations.ExecutionHTTP, operations.ExecutionWorkflow, operations.ExecutionJob} {
				spec := api.OperationDefinitionSpec{Name: kind, Method: "POST", Path: "/" + kind, Owner: api.OperationOwnerPlatformTenant, InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`true`), ProgressStages: []string{"finish"}}
				if kind == operations.ExecutionWorkflow {
					spec.Workflow = "export-chain"
				}
				if kind == operations.ExecutionJob {
					spec.Job = job.Name
				}
				def, err := ops.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: spec}})
				if err != nil {
					t.Fatal(err)
				}
				specs, defs = append(specs, spec), append(defs, def)
			}
			srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
			srv.operationsAdmissionEnabled = true // a configured gate overrides private fixtures
			srv.operationsWorkloadVerifier = &workloadidentity.Verifier{}
			srv.operationArtifactStorage, err = storage.NewLocalStorageBackend(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "preview.json")
			srv.operationsPreview, _ = operations.NewPreviewAdmission(path)
			now := time.Now().UTC()
			policy := operations.PreviewPolicy{Version: 1, Enabled: true, NotBefore: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute), Cohorts: []operations.PreviewCohort{{AccountID: uuid.MustParse(account.ID).String(), AppID: uuid.MustParse(app.ID).String(), Scope: dep.Scope, PlatformTenantIDs: []string{tenant.ID}}}}
			write := func(kinds []string) {
				t.Helper()
				policy.Cohorts[0].ExecutionKinds = kinds
				raw, err := json.Marshal(policy)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path+".next", raw, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(path+".next", path); err != nil {
					t.Fatal(err)
				}
			}
			write(nil) // legacy policies admit HTTP only
			key, hash, err := api.GenerateAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateAPIKey(ctx, account.ID, hash, "admission", api.ScopesAdminOnly); err != nil {
				t.Fatal(err)
			}
			httpServer := httptest.NewServer(srv.handler())
			defer httpServer.Close()
			owner := api.NewClient(httpServer.URL, key)
			token, err := owner.CreatePlatformTenantAccessToken(ctx, tenant.ID, api.CreatePlatformTenantAccessTokenRequest{Name: "browser", Scopes: []string{api.ScopePlatformTenantOperationsRead, api.ScopePlatformTenantOperationsManage}})
			if err != nil {
				t.Fatal(err)
			}
			customer := api.NewClient(httpServer.URL, token.Token)
			routes := gateway.DurableOperationRoutes{Store: store.(gateway.OperationRouteStore), Admission: srv.operationsPreview}
			closed := func(err error) {
				t.Helper()
				problem := api.AsProblem(err)
				var clientError *api.APIError
				if errors.As(err, &clientError) {
					problem = &clientError.Problem
				}
				if problem == nil || problem.Status != http.StatusServiceUnavailable {
					t.Fatalf("expected closed admission: %v", err)
				}
			}
			for _, def := range defs {
				_, err := owner.PutOperationDefinition(ctx, app.Slug, dep.ID, def.Spec.Name, def.Spec)
				if def.Spec.Name == operations.ExecutionHTTP {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					closed(err)
					_, err = customer.StartPlatformTenantSelfOperation(ctx, api.OperationStartRequest{DefinitionID: def.ID, Input: []byte(`{}`)}, "closed")
					closed(err)
					if _, err := routes.EnqueueOperationRoute(ctx, gateway.OperationRoute{Definition: def}, tenant.ID, "closed", []byte(`{}`)); !errors.Is(err, operations.ErrAdmissionClosed) {
						t.Fatalf("gateway bypassed native gate: %v", err)
					}
				}
			}
			count, err := store.(state.OperationDiagnosticStore).OperationPendingCount(ctx, account.ID)
			if err != nil || count != 0 {
				t.Fatal("closed requests created work", count, err)
			}
			manifest := &gregalemanifest.Manifest{ResolvedOperations: specs}
			staged, problem := srv.applySourceRefManifest(ctx, account, app, manifest, dep.Scope, true)
			if problem == nil || problem.Status != 503 || sourceRefManifestNeedsRollback(staged) {
				t.Fatal("mixed source deployment reached mutations", problem)
			}
			_, err = srv.retryOperationDefinitions(ctx, app, dep)
			closed(err)
			_, err = apidsource.Enqueue(ctx, store, noopNotifier{}, apidsource.EnqueueParams{OperationDefinitions: specs, OperationAdmissionEnabled: srv.operationDefinitionsAdmission(account.ID, app.ID, dep.Scope, specs)})
			closed(err)
			for _, def := range defs {
				kind := operations.DefinitionExecutionKind(def.Spec)
				write([]string{kind})
				if _, err := owner.PutOperationDefinition(ctx, app.Slug, dep.ID, def.Spec.Name, def.Spec); err != nil {
					t.Fatal(err)
				}
				report, err := owner.GetOperationDoctor(ctx, app.Slug, dep.ID, tenant.ID, def.Spec.Name)
				if err != nil || report.SubmissionState != "eligible" {
					t.Fatalf("kind-only doctor differs from admission: %+v %v", report, err)
				}
				found := false
				for _, check := range report.Checks {
					if check.Check == "execution_preview" && check.ExecutionKind == kind && check.Status == "observed" {
						found = true
					}
				}
				if !found {
					t.Fatal("missing typed eligibility", report)
				}
				accepted, err := customer.StartPlatformTenantSelfOperation(ctx, api.OperationStartRequest{DefinitionID: def.ID, Input: []byte(`{}`)}, "allowed-"+kind)
				if err != nil {
					t.Fatal(err)
				}
				viaGateway, err := routes.EnqueueOperationRoute(ctx, gateway.OperationRoute{Definition: def}, tenant.ID, "allowed-"+kind, []byte(`{}`))
				if err != nil || viaGateway.ID != accepted.ID {
					t.Fatal("gateway kind admission differs", err)
				}
				write([]string{operations.ExecutionHTTP})
				if kind != operations.ExecutionHTTP {
					_, err = customer.StartPlatformTenantSelfOperation(ctx, api.OperationStartRequest{DefinitionID: def.ID, Input: []byte(`{}`)}, "allowed-"+kind)
					closed(err) // rollback also rejects idempotent resubmissions
					report, err = owner.GetOperationDoctor(ctx, app.Slug, dep.ID, tenant.ID, def.Spec.Name)
					if err != nil || report.SubmissionState != "blocked" {
						t.Fatal("rollback doctor remained eligible", report, err)
					}
				}
				if retained, err := customer.GetPlatformTenantSelfOperation(ctx, accepted.ID); err != nil || retained.ID != accepted.ID {
					t.Fatal("rollback hid retained work", err)
				}
				if _, err := customer.GetPlatformTenantSelfOperationEvents(ctx, accepted.ID, 0); err != nil {
					t.Fatal("rollback hid events", err)
				}
			}
		})
	}
}
