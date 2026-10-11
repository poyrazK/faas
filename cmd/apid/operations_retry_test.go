// ADR-521: customer operations preserve ownership, execution fences and independent delivery.
package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOperationBuildRetryRetainsDefinition(t *testing.T) {
	ctx := context.Background()
	t.Setenv("FAAS_STORAGE_BACKEND", "local")
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "retry-operation@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "retry-operation", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "source.tar.gz")
	if err := os.WriteFile(source, []byte("retained source"), 0600); err != nil {
		t.Fatal(err)
	}
	spec := api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, InputSchema: []byte(`true`), OutputSchema: []byte(`true`), ProgressStages: []string{"generating"}}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: source, SourceBytes: 15})
	if err != nil {
		t.Fatal(err)
	}
	def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: spec}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FailSourceDeployment(ctx, dep.ID, "build failed"); err != nil {
		t.Fatal(err)
	}
	dep, err = store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	srv.operationsAdmissionEnabled = true
	retried, err := srv.enqueueRetry(ctx, app, dep, state.StageName("source_download"))
	if err != nil {
		t.Fatal(err)
	}
	installed, err := store.OperationDefinitionsForDeployment(ctx, acct.ID, app.ID, retried.ID)
	if err != nil || len(installed) != 1 || installed[0].Revision != def.Revision || installed[0].ID == def.ID || installed[0].Scope != retried.Scope {
		t.Fatalf("retry contract: %+v %v", installed, err)
	}
	if _, err := store.BuildByDeployment(ctx, retried.ID); err != nil {
		t.Fatal("accepted retry has no build", err)
	}
	srv.operationsAdmissionEnabled = false
	if _, err := srv.enqueueRetry(ctx, app, dep, state.StageName("source_download")); err == nil {
		t.Fatal("retry created an operation deployment while admission disabled")
	}
	latest, err := store.LatestDeployment(ctx, app.ID)
	if err != nil || latest.ID != retried.ID {
		t.Fatalf("disabled retry published another deployment: %+v %v", latest, err)
	}
}
