package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMem_ManualTrafficKeepsAbortedCanaryAtZero(t *testing.T) {
	manualTrafficAbortedCanarySuite(t, state.NewMemStore())
}

func TestPg_ManualTrafficKeepsAbortedCanaryAtZero(t *testing.T) {
	s, _ := pgStore(t)
	manualTrafficAbortedCanarySuite(t, s)
}

// PulseWatch F1: after `deploy --rollout safe` aborted a canary on 5xx, the
// candidate stayed live at 0%. `release traffic set <stable> --percent 50`
// then split the remaining 50% evenly across every 0% live sibling, routing
// production traffic back to the regression the canary gate had rolled back.
func manualTrafficAbortedCanarySuite(t *testing.T, s state.Store) {
	t.Helper()
	ctx := context.Background()
	t.Run("only the aborted canary could take the residual", func(t *testing.T) {
		app, stable := abortedCanaryTrafficApp(ctx, t, s)
		candidate := abortCanaryBeside(ctx, t, s, app, stable)
		_, err := s.UpdateDeploymentTraffic(ctx, stable.ID, 50)
		if !errors.Is(err, state.ErrTrafficResidualUnassigned) || !errors.Is(err, state.ErrTrafficPercentSumInvalid) {
			t.Fatalf("UpdateDeploymentTraffic(stable, 50) = %v, want ErrTrafficResidualUnassigned", err)
		}
		requireTrafficWeights(ctx, t, s, map[string]int{stable.ID: 100, candidate.ID: 0})
	})
	t.Run("the residual skips the aborted canary", func(t *testing.T) {
		app, stable := abortedCanaryTrafficApp(ctx, t, s)
		previous := liveTrafficDeployment(ctx, t, s, app, 0)
		candidate := abortCanaryBeside(ctx, t, s, app, stable)
		if _, err := s.UpdateDeploymentTraffic(ctx, stable.ID, 50); err != nil {
			t.Fatalf("UpdateDeploymentTraffic(stable, 50): %v", err)
		}
		requireTrafficWeights(ctx, t, s, map[string]int{stable.ID: 50, previous.ID: 50, candidate.ID: 0})
	})
	t.Run("an operator may still name the aborted canary", func(t *testing.T) {
		app, stable := abortedCanaryTrafficApp(ctx, t, s)
		candidate := abortCanaryBeside(ctx, t, s, app, stable)
		if _, err := s.UpdateDeploymentTraffic(ctx, candidate.ID, 10); err != nil {
			t.Fatalf("UpdateDeploymentTraffic(candidate, 10): %v", err)
		}
		requireTrafficWeights(ctx, t, s, map[string]int{stable.ID: 90, candidate.ID: 10})
	})
}

func abortedCanaryTrafficApp(ctx context.Context, t *testing.T, s state.Store) (state.App, state.Deployment) {
	t.Helper()
	acct, err := s.CreateAccount(ctx, uuid.NewString()+"@aborted-canary.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "aborted-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	return app, liveTrafficDeployment(ctx, t, s, app, 100)
}

func liveTrafficDeployment(ctx context.Context, t *testing.T, s state.Store, app state.App, percent int) state.Deployment {
	t.Helper()
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, Scope: "default", TrafficPercent: percent, TrafficPercentExplicit: true, ImageDigest: "sha256:" + strings.Repeat("1", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, dep.ID, "/test/"+dep.ID, "test/"+dep.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	return dep
}

// abortCanaryBeside admits a canary candidate at its 25% step and aborts it,
// as the canary gate does on abort_5xx: the candidate stays live at 0%.
func abortCanaryBeside(ctx context.Context, t *testing.T, s state.Store, app state.App, stable state.Deployment) state.Deployment {
	t.Helper()
	started := time.Now().Add(-time.Minute)
	candidate, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "default", TrafficPercent: 25, TrafficPercentExplicit: true, ImageDigest: stable.ImageDigest, CanaryPreset: "balanced", CanaryStep: 1, CanaryTotalSteps: 4, CanaryStepStartedAt: &started, RolloutState: "rolling_out"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, candidate.ID, "/test/"+candidate.ID, "test/"+candidate.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDeploymentStatus(ctx, candidate.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.(state.CanaryAdvancer).AdvanceCanary(ctx, candidate.ID, state.CanaryAdvanceParams{ExpectedStep: 1, TrafficPercent: 25, Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "test:canary"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RecoverRollout(ctx, app.ID, "abort", "abort_5xx"); err != nil {
		t.Fatalf("abort canary: %v", err)
	}
	aborted, err := s.DeploymentByID(ctx, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if aborted.Status != state.DeployLive || aborted.RolloutState != "aborted" || aborted.TrafficPercent != 0 {
		t.Fatalf("aborted canary = status %s rollout %s traffic %d, want live aborted 0%%", aborted.Status, aborted.RolloutState, aborted.TrafficPercent)
	}
	return aborted
}

func requireTrafficWeights(ctx context.Context, t *testing.T, s state.Store, want map[string]int) {
	t.Helper()
	for id, percent := range want {
		d, err := s.DeploymentByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if d.TrafficPercent != percent {
			t.Errorf("deployment %s traffic = %d%%, want %d%%", id, d.TrafficPercent, percent)
		}
	}
}
