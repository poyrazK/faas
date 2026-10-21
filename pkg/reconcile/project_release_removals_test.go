package reconcile

// adr: 20261021120000

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func removalFixture(name string, deps ...string) (reposcan.Workload, state.App) {
	w := reposcan.Workload{Name: name, RootDir: name, Class: reposcan.ClassHTTP, SourceSHA256: "new", Tier: reposcan.TierCompose, DependsOn: deps}
	app := workloadToDraftApp(state.Project{}, w, "", api.PlanPro, workloadNameSet([]reposcan.Workload{w}))
	app.ID, app.Manifest.ProjectSourceSHA256 = name+"-id", "old"
	return w, app
}

func TestProjectSourceRemovals(t *testing.T) {
	webW, web := removalFixture("web")
	_, legacy := removalFixture("legacy")
	remaining, removals, err := ProjectSourceRemovals([]state.App{web, legacy}, reposcan.Result{Workloads: []reposcan.Workload{webW}})
	if err != nil || len(remaining) != 1 || remaining[0].ID != web.ID || len(removals) != 1 || removals[0].AppID != legacy.ID || removals[0].Fingerprint != state.ProjectReleaseAdditionFingerprint(legacy) {
		t.Fatalf("remaining=%+v removals=%+v err=%v", remaining, removals, err)
	}
	if _, _, err := ProjectSourceRemovals([]state.App{web}, reposcan.Result{Workloads: []reposcan.Workload{webW}}); err == nil {
		t.Fatal("a release without a missing service planned a removal")
	}
	if _, _, err := ProjectSourceRemovals([]state.App{legacy}, reposcan.Result{}); err == nil {
		t.Fatal("a removal emptied the project")
	}
	worker := legacy
	worker.Manifest.ExecutionMode = api.ExecutionModeWorker
	if _, _, err := ProjectSourceRemovals([]state.App{web, worker}, reposcan.Result{Workloads: []reposcan.Workload{webW}}); err == nil {
		t.Fatal("a worker was retired")
	}
	// A remaining service that depended on the retired one would have to change.
	apiW, apiApp := removalFixture("api", "legacy")
	if _, _, err := ProjectSourceRemovals([]state.App{apiApp, legacy}, reposcan.Result{Workloads: []reposcan.Workload{apiW}}); err == nil {
		t.Fatal("retired a service another one still depends on")
	}
}
