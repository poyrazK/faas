// adr: 950
package reconcile

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReconcile_DraftAppPersistsServiceWakeAhead(t *testing.T) {
	_, proj := seedProject(t, newFakeStore(), state.ProjectScanSourceCompose, "main")
	got := workloadToDraftApp(proj, reposcan.Workload{
		Name:             "api",
		DependsOn:        []string{"billing"},
		ServiceWakeAhead: api.ServiceWakeAheadDeclared,
	}, "", api.PlanFree, map[string]struct{}{"billing": {}})
	if got.Manifest.ServiceWakeAhead != api.ServiceWakeAheadDeclared {
		t.Fatalf("service wake-ahead = %q, want declared", got.Manifest.ServiceWakeAhead)
	}
}

// Wake-ahead is source-owned: deleting the extension turns it off on the next
// reconcile instead of preserving a stale opt-in that keeps spending RAM.
func TestReconcile_ServiceWakeAheadIsSourceOwned(t *testing.T) {
	app := state.App{
		WorkloadName: "api",
		Manifest:     state.AppManifest{ServiceWakeAhead: api.ServiceWakeAheadDeclared},
	}
	w := reposcan.Workload{Name: "api"}
	if changed := diffFieldsChanged(app, w, ""); !slices.Contains(changed, "service_wake_ahead") {
		t.Fatalf("changed = %v, want service_wake_ahead when the extension is removed", changed)
	}
	applied := ApplyScannedWorkloadToApp(app, w, nil)
	if applied.Manifest.EffectiveServiceWakeAhead() != api.ServiceWakeAheadOff {
		t.Fatalf("applied wake-ahead = %q, want off", applied.Manifest.ServiceWakeAhead)
	}

	w.ServiceWakeAhead = api.ServiceWakeAheadDeclared
	if changed := diffFieldsChanged(app, w, ""); slices.Contains(changed, "service_wake_ahead") {
		t.Fatalf("changed = %v, want no wake-ahead change when source matches", changed)
	}
}
