package promotion

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type workerRecoveryStore struct {
	*healthControlStore
	state.ProjectPromotionMigrationRecoveryStore
	finalErr error
	blocked  string
	calls    int
}

func (s *workerRecoveryStore) ActiveProjectReleaseSet(context.Context, string, string, string) (state.ProjectReleaseSet, error) {
	return state.ProjectReleaseSet{ID: s.p.TargetReleaseSetID}, nil
}
func (s *workerRecoveryStore) FinalizeProjectPromotionMigrationRecovery(_ context.Context, _, _, _ string, _ state.ProjectMigrationRecoveryEvidence) (*api.ProjectPromotionMigrationRecovery, error) {
	s.calls++
	return &api.ProjectPromotionMigrationRecovery{Status: "resolved"}, s.finalErr
}
func (s *workerRecoveryStore) BlockProjectPromotionMigrationRecovery(_ context.Context, _, _, _, reason string) error {
	s.blocked = reason
	return nil
}
func TestWorkerReplacementRecoveryWaitsForOwnedEvidence(t *testing.T) {
	for _, scenario := range []string{"waiting", "changed", "resolved", "deadline"} {
		t.Run(scenario, func(t *testing.T) {
			store := &workerRecoveryStore{healthControlStore: &healthControlStore{p: state.ProjectEnvironmentPromotion{Status: "succeeded", TargetReleaseSetID: "repair"}, h: &api.ProjectPromotionHealth{Policy: api.ProjectPromotionHealthPolicy{Worker: &api.ProjectReleaseWorker{}}}}}
			switch scenario {
			case "waiting":
				store.finalErr = state.ErrPromotionWorkerNotReady
			case "changed":
				store.finalErr = state.ErrConflict
			}
			r := &api.ProjectPromotionMigrationRecovery{Status: "verifying", Deadline: time.Now().Add(time.Minute), Request: api.ProjectPromotionMigrationRecoveryRequest{Action: "resolve"}}
			if scenario == "deadline" {
				r.Deadline = time.Now().Add(-time.Second)
			}
			recorded := 0
			actions := HealthActions{RecoveryRecord: func(context.Context, state.PromotionHealthJob, *api.ProjectPromotionMigrationRecovery) { recorded++ }}
			// A real nonempty HTTP policy and successful fresh probe are required.
			probe := api.ProjectReleaseProbe{AppID: "api", Method: "GET", Path: "/health"}
			store.h.Policy.Probes = []api.ProjectReleaseProbe{probe}
			actions.RecoveryProbe = func(context.Context, state.ProjectEnvironmentPromotion, api.ProjectPromotionHealthPolicy, state.ProjectReleaseSet) ([]api.ProjectReleaseProbeEvidence, error) {
				return []api.ProjectReleaseProbeEvidence{{Probe: probe, CandidateRequests: api.RouteHealthProbeRequestsPerMinute, StableRequests: api.RouteHealthProbeRequestsPerMinute}}, nil
			}
			if err := reconcileMigrationRecovery(t.Context(), store, state.PromotionHealthJob{}, actions, r); err != nil {
				t.Fatal(err)
			}
			wantBlocked := ""
			if scenario == "changed" {
				wantBlocked = "runtime_or_destination_changed"
			}
			if scenario == "deadline" {
				wantBlocked = "recovery_deadline_expired"
			}
			if store.blocked != wantBlocked || (recorded == 1) != (scenario == "resolved") || (store.calls == 0) != (scenario == "deadline") {
				t.Fatalf("blocked=%s records=%d finalizations=%d", store.blocked, recorded, store.calls)
			}
		})
	}
}
