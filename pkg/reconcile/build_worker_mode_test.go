package reconcile

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestBuiltWorkerRunsInWorkerMode pins production-us hunt #9, H9-170. A
// compose `build:` service without ports was stored as workload_class=worker
// with no execution mode. Request wakes refuse a worker-class app and the
// worker reconciler owns only worker-mode apps, so once h9p-front/back/rogue
// were parked by a node failure nothing ever started them again.
func TestBuiltWorkerRunsInWorkerMode(t *testing.T) {
	worker := reposcan.Workload{Name: "worker", Class: reposcan.ClassWorker, Source: "compose.yaml: worker", Tier: reposcan.TierCompose}
	web := reposcan.Workload{Name: "web", Class: reposcan.ClassHTTP, Source: "compose.yaml: web", Tier: reposcan.TierCompose}
	job := reposcan.Workload{Name: "migrate", Class: reposcan.ClassJob, Source: "compose.yaml: migrate", Tier: reposcan.TierCompose}

	t.Run("new apps", func(t *testing.T) {
		for _, tc := range []struct {
			w    reposcan.Workload
			want string
		}{{worker, api.ExecutionModeWorker}, {web, ""}, {job, ""}} {
			app := workloadToDraftApp(state.Project{}, tc.w, "", api.PlanPro)
			if app.Manifest.ExecutionMode != tc.want {
				t.Errorf("%s: execution_mode = %q, want %q", tc.w.Name, app.Manifest.ExecutionMode, tc.want)
			}
		}
	})

	t.Run("an existing worker without the mode is repaired", func(t *testing.T) {
		store := newFakeStore()
		_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")
		stale, err := store.CreateApp(context.Background(), state.App{
			AccountID: proj.AccountID, ProjectID: proj.ID, Slug: "worker", WorkloadName: "worker",
			WorkloadClass: state.WorkloadClassWorker, Status: state.AppActive,
		})
		if err != nil {
			t.Fatal(err)
		}
		if changed := diffFieldsChanged(stale, worker, ""); !slices.Contains(changed, "image_execution_mode") {
			t.Fatalf("fields changed = %v; want the missing execution mode", changed)
		}
		repaired := ApplyScannedWorkloadToApp(stale, worker, nil)
		if repaired.Manifest.ExecutionMode != api.ExecutionModeWorker {
			t.Fatalf("execution_mode = %q, want worker", repaired.Manifest.ExecutionMode)
		}
	})

	t.Run("a declared mode is kept", func(t *testing.T) {
		declared := state.App{WorkloadClass: state.WorkloadClassWorker, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService}}
		if got := ApplyScannedWorkloadToApp(declared, worker, nil).Manifest.ExecutionMode; got != api.ExecutionModeService {
			t.Fatalf("execution_mode = %q, want the declared service mode", got)
		}
	})

	t.Run("Free cannot run a built worker", func(t *testing.T) {
		if reasons := ProjectImageLifecycleAdmissionReasons(api.PlanFree, []reposcan.Workload{web, worker}); len(reasons) != 1 {
			t.Fatalf("Free reasons = %v; want the worker refused", reasons)
		}
		if reasons := ProjectImageLifecycleAdmissionReasons(api.PlanPro, []reposcan.Workload{web, worker}); len(reasons) != 0 {
			t.Fatalf("Pro reasons = %v; want none", reasons)
		}
		if err := validatePlatformTenantPolicy(api.PlanFree, []reposcan.Workload{worker}); !errors.Is(err, ErrInvalidWorkloadPlan) {
			t.Fatalf("validatePlatformTenantPolicy = %v; want ErrInvalidWorkloadPlan", err)
		}
	})
}
