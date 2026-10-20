// adr: 20261019142544
package reconcile

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDockerfileBuildOptionsReconcile(t *testing.T) {
	store := newFakeStore()
	_, project := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	service := NewService(store, newFakeAuditor(store), nil)
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{{
		Name: "storefront", RootDir: "storefront", Dockerfile: "Dockerfile", Class: reposcan.ClassHTTP, SourceSHA256: "accepted",
		DockerfileBuild: &api.DockerfileBuildOptions{Target: "runtime", Args: map[string]string{"NODE_ENV": "production"}},
		Ports:           []int{3000}, Tier: reposcan.TierCompose, Source: "compose.yaml: storefront",
	}}}
	result, err := service.Reconcile(t.Context(), project, scan, "", "main", nil)
	if err != nil || len(result.Added) != 1 {
		t.Fatalf("create = %+v, %v", result, err)
	}
	app := result.Added[0]
	if !reflect.DeepEqual(app.Manifest.DockerfileBuild, scan.Workloads[0].DockerfileBuild) {
		t.Fatalf("created options = %+v", app.Manifest.DockerfileBuild)
	}
	// The stored options are a copy, not the scan's map.
	scan.Workloads[0].DockerfileBuild.Args["NODE_ENV"] = "mutated"
	if app.Manifest.DockerfileBuild.Args["NODE_ENV"] != "production" {
		t.Fatal("app manifest aliases the scanned arguments")
	}
	scan.Workloads[0].DockerfileBuild.Args["NODE_ENV"] = "production"
	app.Manifest.ProjectSourceSHA256 = "accepted"
	if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &app.Manifest}); err != nil {
		t.Fatal(err)
	}
	if result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil); err != nil || len(result.Changed) != 0 {
		t.Fatalf("unchanged reapply = %+v, %v", result, err)
	}
	for _, tc := range []struct {
		name    string
		options *api.DockerfileBuildOptions
	}{
		{"target change", &api.DockerfileBuildOptions{Target: "test", Args: map[string]string{"NODE_ENV": "production"}}},
		{"argument change", &api.DockerfileBuildOptions{Target: "test", Args: map[string]string{"NODE_ENV": "development"}}},
		{"options removed", nil},
	} {
		scan.Workloads[0].DockerfileBuild = tc.options
		result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil)
		if err != nil || len(result.Changed) != 1 || !reflect.DeepEqual(result.Changed[0].Manifest.DockerfileBuild, tc.options) {
			t.Fatalf("%s = %+v, %v", tc.name, result, err)
		}
	}
}

func TestDockerfileBuildOptionsDiffField(t *testing.T) {
	app := state.App{Manifest: state.AppManifest{DockerfileBuild: &api.DockerfileBuildOptions{Target: "runtime"}}}
	same := reposcan.Workload{DockerfileBuild: &api.DockerfileBuildOptions{Target: "runtime"}}
	if slices.Contains(diffFieldsChanged(app, same, ""), "dockerfile_build") {
		t.Fatal("identical options reported as changed")
	}
	other := reposcan.Workload{DockerfileBuild: &api.DockerfileBuildOptions{Target: "test"}}
	if !slices.Contains(diffFieldsChanged(app, other, ""), "dockerfile_build") {
		t.Fatal("a target change is not reported as dockerfile_build")
	}
	if !slices.Contains(diffFieldsChanged(app, reposcan.Workload{}, ""), "dockerfile_build") {
		t.Fatal("removed options are not reported as dockerfile_build")
	}
}

func TestDockerfileBuildOptionsAdmission(t *testing.T) {
	for _, tc := range []struct {
		name     string
		workload reposcan.Workload
		want     string
	}{
		{"invalid argument name", reposcan.Workload{Name: "shop", Class: reposcan.ClassHTTP,
			DockerfileBuild: &api.DockerfileBuildOptions{Args: map[string]string{"BAD-NAME": "x"}}}, `workload "shop" has invalid build options`},
		{"prebuilt image", reposcan.Workload{Name: "web", Class: reposcan.ClassHTTP, Image: "example.com/web:v1",
			DockerfileBuild: &api.DockerfileBuildOptions{Target: "runtime"}}, `workload "web" is a prebuilt image; build options apply only to Dockerfile builds`},
	} {
		reasons := WorkloadAdmissionReasons([]reposcan.Workload{tc.workload}, nil, "")
		if !strings.Contains(strings.Join(reasons, "; "), tc.want) {
			t.Errorf("%s: reasons = %v; want %q", tc.name, reasons, tc.want)
		}
	}
	valid := reposcan.Workload{Name: "shop", Class: reposcan.ClassHTTP, Dockerfile: "Dockerfile",
		DockerfileBuild: &api.DockerfileBuildOptions{Target: "runtime", Args: map[string]string{"A": "1"}}}
	for _, reason := range WorkloadAdmissionReasons([]reposcan.Workload{valid}, nil, "") {
		if strings.Contains(reason, "build options") {
			t.Fatalf("valid options rejected: %s", reason)
		}
	}
}
