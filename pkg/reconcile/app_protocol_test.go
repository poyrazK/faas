// adr: 20261105095300
package reconcile_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// A Compose service's x-gregale-app-protocol becomes its app's protocol on
// create and on a later change; a service that declares none keeps the
// protocol its owner set.
func TestReconcileAppliesComposeAppProtocol(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "protocol@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := reconcile.NewService(store, audit.New(store, log, nil, "reconcile"), log)
	workload := func(name, protocol string) reposcan.Workload {
		return reposcan.Workload{Name: name, Image: "docker.io/library/nginx:1.27", Ports: []int{80}, Class: reposcan.ClassHTTP,
			Tier: reposcan.TierCompose, AppProtocol: protocol, DetectedBy: reposcan.Detection{Detector: "compose"}}
	}
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{workload("orders", "grpc"), workload("web", "")}}
	if _, err := service.Reconcile(ctx, project, scan, "sha-1", "main", nil); err != nil {
		t.Fatal(err)
	}
	protocols := func() map[string]string {
		t.Helper()
		apps, err := store.AppsForProject(ctx, account.ID, project.ID)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, app := range apps {
			out[app.WorkloadName] = app.AppProtocol
		}
		return out
	}
	got := protocols()
	if got["orders"] != "grpc" {
		t.Fatalf("orders protocol = %q, want grpc", got["orders"])
	}
	webProtocol := got["web"]
	scan.Workloads[0].AppProtocol = "http2"
	if _, err := service.Reconcile(ctx, project, scan, "sha-2", "main", nil); err != nil {
		t.Fatal(err)
	}
	got = protocols()
	if got["orders"] != "http2" || got["web"] != webProtocol {
		t.Fatalf("protocols after change = %v, want orders=http2 web=%q", got, webProtocol)
	}
}

// A service a staging deploy creates gets its declared protocol; it stayed
// http1 because only production deploys synced it (ADR-20261108090000).
func TestReconcileEnvironmentAppliesProtocolToAddedService(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "stage-protocol@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := reconcile.NewService(store, audit.New(store, log, nil, "reconcile"), log)
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{{
		Name: "orders", Image: "docker.io/library/nginx:1.27", Ports: []int{80}, Class: reposcan.ClassHTTP,
		Tier: reposcan.TierCompose, AppProtocol: "grpc", DetectedBy: reposcan.Detection{Detector: "compose"}}}}
	result, err := service.ReconcileEnvironment(ctx, project, scan, "staging", "source", false)
	if err != nil || len(result.Added) != 1 {
		t.Fatalf("stage result: added=%d err=%v", len(result.Added), err)
	}
	app, err := store.AppByID(ctx, result.Added[0].ID)
	if err != nil || app.AppProtocol != "grpc" {
		t.Fatalf("orders protocol = %q, %v; want grpc", app.AppProtocol, err)
	}
}

// A plan without gRPC refuses a service that declares it, before any write.
func TestWorkloadPlanAdmissionRefusesAppProtocolOutsidePlan(t *testing.T) {
	workloads := []reposcan.Workload{{Name: "orders", Class: reposcan.ClassHTTP, AppProtocol: "grpc"}}
	reasons := reconcile.ProjectImageLifecycleAdmissionReasons(api.PlanFree, workloads)
	if len(reasons) == 0 || !strings.Contains(strings.Join(reasons, "\n"), "x-gregale-app-protocol grpc") {
		t.Fatalf("free plan reasons = %v, want the protocol refused", reasons)
	}
	if reasons := reconcile.ProjectImageLifecycleAdmissionReasons(api.PlanPro, workloads); len(reasons) != 0 {
		t.Fatalf("pro plan reasons = %v, want none", reasons)
	}
}
