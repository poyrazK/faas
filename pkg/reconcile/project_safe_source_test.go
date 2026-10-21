package reconcile

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestProjectSafeSourceRejectsConfigurationAndMembershipChanges(t *testing.T) {
	workload := reposcan.Workload{Name: "web", RootDir: "web", Class: reposcan.ClassHTTP, SourceSHA256: "new", Tier: reposcan.TierCompose}
	app := workloadToDraftApp(state.Project{}, workload, "", api.PlanPro, workloadNameSet([]reposcan.Workload{workload}))
	app.Manifest.ProjectSourceSHA256 = "old"
	for _, tc := range []struct {
		name   string
		change func(*reposcan.Workload, *state.App)
		valid  bool
	}{
		{"source", func(*reposcan.Workload, *state.App) {}, true},
		{"command", func(w *reposcan.Workload, _ *state.App) { w.Command = []string{"new-command"} }, false},
		{"membership", func(w *reposcan.Workload, _ *state.App) { w.Name = "other" }, false},
		{"image", func(w *reposcan.Workload, _ *state.App) { w.Image = "registry/image" }, false},
		{"release command", func(w *reposcan.Workload, _ *state.App) { w.ReleaseCommand = []string{"migrate"} }, false},
		{"service", func(_ *reposcan.Workload, a *state.App) { a.Manifest.ExecutionMode = api.ExecutionModeService }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, a := workload, app
			tc.change(&w, &a)
			_, err := ProjectSafeSourceApps([]state.App{a}, reposcan.Result{Workloads: []reposcan.Workload{w}})
			if (err == nil) != tc.valid {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestProjectSafeSourceWorkerRequiresExplicitAdmission(t *testing.T) {
	w := reposcan.Workload{Name: "jobs", RootDir: "jobs", Class: reposcan.ClassHTTP, SourceSHA256: "new", Tier: reposcan.TierCompose}
	app := workloadToDraftApp(state.Project{}, w, "", api.PlanPro, workloadNameSet([]reposcan.Workload{w}))
	app.Manifest.ExecutionMode = api.ExecutionModeWorker
	app.Manifest.ProjectSourceSHA256 = "old"
	scan := reposcan.Result{Workloads: []reposcan.Workload{w}}
	if _, err := ProjectSafeSourceApps([]state.App{app}, scan); err == nil {
		t.Fatal("ordinary atomic source deploy admitted a worker")
	}
	if _, err := ProjectSafeSourceApps([]state.App{app}, scan, false, true); err != nil {
		t.Fatalf("explicit worker admission rejected unchanged source workload: %v", err)
	}
}
