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

// adr: 20261021110000
func TestProjectSafeSourceAcceptsBackingServiceDeclarations(t *testing.T) {
	w := reposcan.Workload{Name: "web", RootDir: "web", Class: reposcan.ClassHTTP, SourceSHA256: "new", Tier: reposcan.TierCompose, DependsOn: []string{"db"}}
	app := workloadToDraftApp(state.Project{}, w, "", api.PlanPro, workloadNameSet([]reposcan.Workload{w}))
	app.Manifest.ProjectSourceSHA256 = "old"
	db := reposcan.Managed{Name: "db", Kind: "postgres", EnvHint: "DATABASE_URL", Image: "postgres:16"}
	apps, err := ProjectSafeSourceApps([]state.App{app}, reposcan.Result{Workloads: []reposcan.Workload{w}, Managed: []reposcan.Managed{db}})
	if err != nil || len(apps) != 1 {
		t.Fatalf("backing service declaration refused: %v", err)
	}
	// A dependency on an undeclared service is still an invalid graph.
	if _, err := ProjectSafeSourceApps([]state.App{app}, reposcan.Result{Workloads: []reposcan.Workload{w}}); err == nil {
		t.Fatal("dependency on an undeclared service accepted")
	}
}

// adr: 20261021113000
func TestProjectSafeSourceImageWorkloads(t *testing.T) {
	base := reposcan.Workload{Name: "api", RootDir: "api", Class: reposcan.ClassHTTP, Tier: reposcan.TierCompose, Image: "ghcr.io/acme/api:1", Ports: []int{8080}, SourceSHA256: "old"}
	app := workloadToDraftApp(state.Project{}, base, "", api.PlanPro, workloadNameSet([]reposcan.Workload{base}))
	app.Manifest.ProjectSourceSHA256 = "old"
	if app.Manifest.ProjectImage != base.Image {
		t.Fatalf("fixture is not an image app: %+v", app.Manifest)
	}
	for _, tc := range []struct {
		name  string
		scan  func(reposcan.Workload) reposcan.Workload
		valid bool
	}{
		{"new reference", func(w reposcan.Workload) reposcan.Workload { w.Image = "ghcr.io/acme/api:2"; return w }, true},
		{"port", func(w reposcan.Workload) reposcan.Workload {
			w.Image = "ghcr.io/acme/api:2"
			w.Ports = []int{9090}
			return w
		}, false},
		{"to source", func(w reposcan.Workload) reposcan.Workload { w.Image = ""; return w }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := tc.scan(base)
			w.SourceSHA256 = "new"
			apps, err := ProjectSafeSourceApps([]state.App{app}, reposcan.Result{Workloads: []reposcan.Workload{w}})
			if (err == nil) != tc.valid {
				t.Fatalf("err=%v", err)
			}
			if tc.valid && apps[0].Manifest.ProjectImage != w.Image {
				t.Fatalf("candidate image=%q, want %q", apps[0].Manifest.ProjectImage, w.Image)
			}
		})
	}
	// A source workload cannot become an image workload in an atomic release.
	source := reposcan.Workload{Name: "web", RootDir: "web", Class: reposcan.ClassHTTP, SourceSHA256: "old", Tier: reposcan.TierCompose}
	sourceApp := workloadToDraftApp(state.Project{}, source, "", api.PlanPro, workloadNameSet([]reposcan.Workload{source}))
	sourceApp.Manifest.ProjectSourceSHA256 = "old"
	source.Image = "ghcr.io/acme/web:1"
	if _, err := ProjectSafeSourceApps([]state.App{sourceApp}, reposcan.Result{Workloads: []reposcan.Workload{source}}); err == nil {
		t.Fatal("source workload switched to an image")
	}
}
