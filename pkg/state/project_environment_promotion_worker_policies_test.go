package state

// adr: 20261021130000

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPromotionWorkerWorkPolicies(t *testing.T) {
	lane := func(revision int64, perKey int) *ProjectEnvironmentWorkPolicySettings {
		return &ProjectEnvironmentWorkPolicySettings{Revision: revision, Policies: []ProjectEnvironmentCloneWorkPolicy{{Name: "orders", Revision: revision, MaxRunningPerKey: perKey, PendingUpdates: "all"}}}
	}
	worker := func(policies *ProjectEnvironmentWorkPolicySettings) ProjectEnvironmentWorkloadSettings {
		return ProjectEnvironmentWorkloadSettings{WorkloadClass: WorkloadClassWorker, Manifest: AppManifest{ExecutionMode: api.ExecutionModeWorker}, WorkPolicies: policies}
	}
	for _, tc := range []struct {
		name           string
		source, target *ProjectEnvironmentWorkPolicySettings
		want           bool
	}{
		{"no policies", nil, nil, true},
		{"same lanes, different revisions", lane(1, 1), lane(7, 1), true},
		{"different concurrency", lane(1, 1), lane(1, 2), false},
		{"only the source has lanes", lane(1, 1), nil, false},
		{"only the destination has lanes", nil, lane(1, 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &promotionReleaseWorkload{SourceSettings: worker(tc.source), TargetSettings: worker(tc.target)}
			if got := promotionSnapshotWorkload(w, ProjectEnvironmentPromotion{Workers: true}); got != tc.want {
				t.Fatalf("eligible=%v, want %v", got, tc.want)
			}
		})
	}
}
