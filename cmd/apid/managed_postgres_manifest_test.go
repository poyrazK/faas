package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectManifestAsyncRoutesLoadForProjectReconciliation(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "project-async-routes@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.Default(), "gregale.dev", noopNotifier{})
	dir := t.TempDir()
	body := "async_routes: []\n"
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, problem := srv.loadAndResolveProjectManifest(context.Background(), acct, dir, []string{"reports"}, "")
	if problem != nil {
		t.Fatalf("project deploy async_routes: %+v", problem)
	}
	if !resolved.AsyncRoutesPresent || resolved.AsyncRoutes == nil || len(resolved.AsyncRoutes) != 0 {
		t.Fatalf("resolved async routes = %#v (present=%v), want explicit empty list", resolved.AsyncRoutes, resolved.AsyncRoutesPresent)
	}
}

func TestProjectManifestAsyncRoutesGateOnlySelectedWorkloads(t *testing.T) {
	routes := []gregalemanifest.AsyncRoute{{App: "reports", Name: "create-report"}}
	workloads := []reposcan.Workload{
		{Name: "reports", Class: reposcan.ClassHTTP},
		{Name: "worker", Class: reposcan.ClassWorker},
	}
	if problem := projectAsyncRoutesPlanProblem(api.PlanFree, routes, workloads[:1], nil); problem == nil || problem.Code != api.CodePlanEdgeRuleKindNotAllowed {
		t.Fatalf("selected Free route problem = %+v, want async route feature gate", problem)
	}
	workerRoute := []gregalemanifest.AsyncRoute{{App: "worker", Name: "consume"}}
	if problem := projectAsyncRoutesPlanProblem(api.PlanFree, workerRoute, workloads[:1], nil); problem != nil {
		t.Fatalf("unselected Free route must not block a partial project deploy: %+v", problem)
	}
}

func TestResolveManagedPostgresDatabaseRecordByIDOrName(t *testing.T) {
	databases := []managedpostgres.Database{
		{ID: "db-orders", Name: "orders", State: managedpostgres.StateReady},
		{ID: "db-analytics", Name: "analytics", State: managedpostgres.StateReady},
	}
	for _, reference := range []string{"db-orders", "orders"} {
		got, err := resolveManagedPostgresDatabaseRecord(databases, reference)
		if err != nil || got.ID != "db-orders" {
			t.Fatalf("reference %q resolved to %+v, err=%v", reference, got, err)
		}
	}
}

func TestResolveManagedPostgresDatabaseRecordRejectsAmbiguousName(t *testing.T) {
	databases := []managedpostgres.Database{
		{ID: "db-a", Name: "orders", State: managedpostgres.StateReady, CreatedAt: time.Unix(1, 0)},
		{ID: "db-b", Name: "orders", State: managedpostgres.StateReady, CreatedAt: time.Unix(2, 0)},
	}
	_, err := resolveManagedPostgresDatabaseRecord(databases, "orders")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("err = %v, want ambiguous database reference", err)
	}
}
