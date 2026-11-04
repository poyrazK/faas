package reconcile

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkloadDiffExclusionsPreserveAllocatedApps(t *testing.T) {
	existing := []state.App{{ID: "api", Slug: "shop-api", WorkloadName: "api", RootDir: "services/api"}}
	for _, selector := range []string{"api", "shop-api", "services/api"} {
		for _, omit := range []bool{false, true} {
			t.Run(selector+map[bool]string{true: "/omitted", false: "/present"}[omit], func(t *testing.T) {
				scan := reposcan.Result{}
				if !omit {
					// A direct reconciler may receive a scan without AppSlug allocation.
					scan.Workloads = []reposcan.Workload{{Name: "api", RootDir: "services/api", Command: []string{"changed"}}}
				}
				if actions := workloadDiff(scan, state.Project{}, existing, map[string]bool{selector: true}); len(actions) != 0 {
					t.Fatalf("excluded app changed: %+v", actions)
				}
			})
		}
	}
}
