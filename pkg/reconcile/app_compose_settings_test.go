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

// x-gregale-streaming and x-gregale-wake-ahead set the app's streaming and
// wake-ahead on deploy and on a later change; a service that declares
// neither keeps its owner's settings.
func TestReconcileAppliesComposeStreamingAndWakeAhead(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "settings@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := reconcile.NewService(store, audit.New(store, log, nil, "reconcile"), log)
	on, off := true, false
	workload := func(name string, streaming, wakeAhead *bool) reposcan.Workload {
		return reposcan.Workload{Name: name, Image: "docker.io/library/nginx:1.27", Ports: []int{80}, Class: reposcan.ClassHTTP,
			Tier: reposcan.TierCompose, Streaming: streaming, WakeAhead: wakeAhead, DetectedBy: reposcan.Detection{Detector: "compose"}}
	}
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{workload("events", &on, &off), workload("web", nil, nil)}}
	if _, err := service.Reconcile(ctx, project, scan, "sha-1", "main", nil); err != nil {
		t.Fatal(err)
	}
	read := func() map[string][2]bool {
		t.Helper()
		apps, err := store.AppsForProject(ctx, account.ID, project.ID)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string][2]bool{}
		for _, app := range apps {
			setting, err := store.GetServiceWakeAhead(ctx, account.ID, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			out[app.WorkloadName] = [2]bool{app.StreamingEnabled, state.EffectiveServiceWakeAhead(setting, app)}
		}
		return out
	}
	got := read()
	if got["events"] != [2]bool{true, false} {
		t.Fatalf("events streaming/wake-ahead = %v, want [true false]", got["events"])
	}
	web := got["web"]
	scan.Workloads[0].Streaming, scan.Workloads[0].WakeAhead = &off, &on
	if _, err := service.Reconcile(ctx, project, scan, "sha-2", "main", nil); err != nil {
		t.Fatal(err)
	}
	got = read()
	if got["events"] != [2]bool{false, true} || got["web"] != web {
		t.Fatalf("after change = %v, want events [false true], web unchanged %v", got, web)
	}
}

func TestWorkloadPlanAdmissionRefusesStreamingOutsidePlan(t *testing.T) {
	on := true
	workloads := []reposcan.Workload{{Name: "events", Class: reposcan.ClassHTTP, Streaming: &on}}
	if reasons := reconcile.ProjectImageLifecycleAdmissionReasons(api.PlanFree, workloads); !strings.Contains(strings.Join(reasons, "\n"), "x-gregale-streaming") {
		t.Fatalf("free plan reasons = %v, want streaming refused", reasons)
	}
	if reasons := reconcile.ProjectImageLifecycleAdmissionReasons(api.PlanPro, workloads); len(reasons) != 0 {
		t.Fatalf("pro plan reasons = %v", reasons)
	}
}
