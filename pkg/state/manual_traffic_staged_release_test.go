package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMem_ManualTrafficStagedReleasesAndReleaseLimit(t *testing.T) {
	manualTrafficStagedReleaseSuite(t, state.NewMemStore())
}

func TestPg_ManualTrafficStagedReleasesAndReleaseLimit(t *testing.T) {
	s, _ := pgStore(t)
	manualTrafficStagedReleaseSuite(t, s)
}

// Lowering the only serving release spread the residual evenly over every
// release staged with no traffic, none of them named; and a split could name
// more releases than the app can run, so one release could never wake.
func manualTrafficStagedReleaseSuite(t *testing.T, s state.Store) {
	t.Helper()
	ctx := context.Background()
	stagedApp := func(t *testing.T) (state.Deployment, state.Deployment, state.Deployment) {
		t.Helper()
		app, stable := abortedCanaryTrafficApp(ctx, t, s)
		return stable, liveTrafficDeployment(ctx, t, s, app, 0), liveTrafficDeployment(ctx, t, s, app, 0)
	}
	t.Run("lowering the serving release holds staged releases at zero", func(t *testing.T) {
		stable, first, second := stagedApp(t)
		if _, err := s.UpdateDeploymentTraffic(ctx, stable.ID, 40); !errors.Is(err, state.ErrTrafficResidualUnassigned) {
			t.Fatalf("UpdateDeploymentTraffic(stable, 40) = %v, want ErrTrafficResidualUnassigned", err)
		}
		requireTrafficWeights(ctx, t, s, map[string]int{stable.ID: 100, first.ID: 0, second.ID: 0})
	})
	t.Run("an operator may name a staged release", func(t *testing.T) {
		stable, first, second := stagedApp(t)
		if _, err := s.UpdateDeploymentTraffic(ctx, first.ID, 30); err != nil {
			t.Fatalf("UpdateDeploymentTraffic(first, 30): %v", err)
		}
		requireTrafficWeights(ctx, t, s, map[string]int{stable.ID: 70, first.ID: 30, second.ID: 0})
	})
	t.Run("a split past the release limit is refused", func(t *testing.T) {
		stable, first, second := stagedApp(t)
		limited := state.WithTrafficSplitReleaseLimit(ctx, 2)
		if _, err := s.UpdateDeploymentTraffic(limited, first.ID, 30); err != nil {
			t.Fatalf("two releases within the limit: %v", err)
		}
		_, err := s.UpdateDeploymentTraffic(limited, second.ID, 20)
		var limitErr *state.TrafficSplitReleaseLimitError
		if !errors.As(err, &limitErr) || limitErr.Limit != 2 || limitErr.Requested != 3 {
			t.Fatalf("third release = %v, want TrafficSplitReleaseLimitError{2, 3}", err)
		}
		requireTrafficWeights(ctx, t, s, map[string]int{stable.ID: 70, first.ID: 30, second.ID: 0})
	})
	t.Run("a split already past the limit may still narrow", func(t *testing.T) {
		stable, first, second := stagedApp(t)
		for _, step := range []struct {
			id      string
			percent int
		}{{first.ID, 30}, {second.ID, 20}} {
			if _, err := s.UpdateDeploymentTraffic(ctx, step.id, step.percent); err != nil {
				t.Fatalf("build a three-way split without a limit: %v", err)
			}
		}
		limited := state.WithTrafficSplitReleaseLimit(ctx, 2)
		if _, err := s.UpdateDeploymentTraffic(limited, second.ID, 0); err != nil {
			t.Fatalf("narrowing to two releases: %v", err)
		}
		if d, err := s.DeploymentByID(ctx, second.ID); err != nil || d.TrafficPercent != 0 {
			t.Fatalf("narrowed release = %+v, %v; want 0%%", d.TrafficPercent, err)
		}
		if a, err := s.DeploymentByID(ctx, stable.ID); err != nil || a.TrafficPercent == 0 {
			t.Fatalf("stable after narrowing = %d%%, %v; want serving", a.TrafficPercent, err)
		}
	})
}
