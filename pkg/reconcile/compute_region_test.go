package reconcile

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReconcileComputeRegionDefaultAndImmutableAdmission(t *testing.T) {
	store := newFakeStore()
	acct, _ := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: acct.ID, Slug: "regional-project", ComputeRegion: "local", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, newFakeAuditor(store), nil)
	svc.ValidateNewComputeRegion = func(_ctx context.Context, id string) error {
		if p := state.ValidateNewComputeRegion(_ctx, store, true, id); p != nil {
			return p
		}
		return nil
	}
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{{Name: "regional-api", Class: reposcan.ClassHTTP, Tier: reposcan.TierCompose, Source: "compose.yaml: regional-api"}}}
	if _, err := svc.Reconcile(t.Context(), project, scan, "sha", "main", nil); err != nil {
		t.Fatal(err)
	}
	app, err := store.AppBySlug(t.Context(), "regional-api")
	if err != nil || app.ComputeRegion != "local" {
		t.Fatalf("app=%+v err=%v", app, err)
	}
	// A GitHub-style reconcile with no region in source preserves placement.
	scan.Workloads[0].Command = []string{"serve"}
	if _, err := svc.Reconcile(t.Context(), project, scan, "sha", "main", nil); err != nil {
		t.Fatal(err)
	}
	before, _ := store.AppBySlug(t.Context(), app.Slug)
	scan.Workloads[0].ComputeRegion = "eu-hel"
	scan.Workloads = append(scan.Workloads, reposcan.Workload{Name: "regional-new", Tier: reposcan.TierCompose})
	for _, planOnly := range []bool{true, false} {
		var err error
		if planOnly {
			_, err = svc.Plan(t.Context(), project, scan, "sha", "main", nil)
		} else {
			_, err = svc.Reconcile(t.Context(), project, scan, "sha", "main", nil)
		}
		if err == nil {
			t.Fatal("accepted immutable-region change")
		}
		apps, _ := store.AppsForProject(t.Context(), acct.ID, project.ID)
		if len(apps) != 1 || apps[0].ComputeRegion != before.ComputeRegion || apps[0].StartCommand != before.StartCommand {
			t.Fatalf("invalid plan mutated apps: %+v", apps)
		}
	}
	// Pausing admission permits existing updates but atomically refuses expansion.
	scan.Workloads = scan.Workloads[:1]
	scan.Workloads[0].ComputeRegion = ""
	if _, err := store.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: "local", DisplayName: "Local", AcceptingNewApps: false}); err != nil {
		t.Fatal(err)
	}
	svc.ValidateNewComputeRegion = nil
	if _, err := svc.Reconcile(t.Context(), project, scan, "sha", "main", nil); err != nil {
		t.Fatal(err)
	}
	scan.Workloads = append(scan.Workloads, reposcan.Workload{Name: "regional-new", Tier: reposcan.TierCompose})
	if _, err := svc.Reconcile(t.Context(), project, scan, "sha", "main", nil); err == nil {
		t.Fatal("disabled gate admitted new regional member")
	}
}

func TestComputeRegionOmissionPreservesLegacyMembers(t *testing.T) {
	project := state.Project{ID: "project", ComputeRegion: "eu-fsn"}
	existing := []state.App{{ProjectID: project.ID, WorkloadName: "legacy", ComputeRegion: ""}, {ProjectID: project.ID, WorkloadName: "regional", ComputeRegion: "eu-hel"}}
	workloads := []reposcan.Workload{{Name: "legacy"}, {Name: "regional"}, {Name: "new"}, {Name: "declared", ComputeRegion: "us-east"}}
	out := WorkloadsWithComputeRegions(workloads, existing, project)
	for i, want := range []string{"", "eu-hel", "eu-fsn", "us-east"} {
		if out[i].ComputeRegion != want {
			t.Fatalf("workload %d region=%q want=%q", i, out[i].ComputeRegion, want)
		}
	}
	if workloads[2].ComputeRegion != "" {
		t.Fatal("resolver mutated source input")
	}
}
