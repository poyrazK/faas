package environmentgitops_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeObservationBackend struct {
	Backend environmentgitops.Backend
	Calls   int
	Error   error
}

func (b *runtimeObservationBackend) Observe(ctx context.Context, lease state.EnvironmentGitOpsLease, desired environmentsync.DesiredState) (environmentgitops.Observation, error) {
	if b.Calls != 1 {
		return environmentgitops.Observation{}, errors.New("runtime effects were not observed before planning")
	}
	return b.Backend.Observe(ctx, lease, desired)
}

func (b *runtimeObservationBackend) Apply(ctx context.Context, lease state.EnvironmentGitOpsLease, plan environmentsync.Plan) ([]environmentgitops.Step, error) {
	return b.Backend.Apply(ctx, lease, plan)
}

func (b *runtimeObservationBackend) ObserveRuntimeEffects(context.Context, state.EnvironmentGitOpsLease) error {
	b.Calls++
	return b.Error
}

func TestReportObservesRuntimeEffectsBeforeDriftOrBlockedPlanning(t *testing.T) {
	for _, outcome := range []string{"drifted", "blocked", "error"} {
		t.Run(outcome, func(t *testing.T) {
			store, source, _, backend, worker := setup(t, "report")
			backend.observation.State.Fields[1].Value = []byte(`"manual-change"`)
			observer := &runtimeObservationBackend{Backend: backend}
			if outcome == "blocked" {
				backend.observation.Owners[1].Manager = "another-manager"
			} else if outcome == "error" {
				observer.Error = errors.New("private transport failure")
			}
			worker.Backend = observer
			if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
				t.Fatalf("report: %t %v", worked, err)
			}
			run := lastRun(t, store, source)
			want := outcome
			if outcome == "error" {
				want = "partial"
				if run.ErrorCode != "environment_runtime_verification_failed" {
					t.Fatal("observation failure lost its sanitized error code", run.ErrorCode)
				}
			}
			if observer.Calls != 1 || backend.applied != 0 || run.Status != want {
				t.Fatalf("report observation: calls=%d applies=%d status=%s", observer.Calls, backend.applied, run.Status)
			}
		})
	}
}
