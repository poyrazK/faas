package state_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestRolloutRecoveryAuditNamesTheActor pins Hunt 32 H32-58: Safe Deploy's
// automatic 5xx abort was audited as operator:cli:recover_rollout, so the
// release timeline blamed an operator command nobody ran.
func TestRolloutRecoveryAuditNamesTheActor(t *testing.T) {
	for name, tc := range map[string]struct {
		ctx  func(context.Context) context.Context
		want string
	}{
		"operator": {func(ctx context.Context) context.Context { return ctx }, state.RolloutRecoveryActorOperator},
		"safe deploy": {func(ctx context.Context) context.Context {
			return state.WithRolloutRecoveryActor(ctx, state.RolloutRecoveryActorSafeDeploy)
		}, state.RolloutRecoveryActorSafeDeploy},
	} {
		t.Run(name, func(t *testing.T) {
			m, ctx, _, app := state.WebhookFixture(t)
			stable, err := m.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:stable"})
			if err != nil {
				t.Fatal(err)
			}
			if err := m.MarkDeploymentLive(ctx, stable.ID); err != nil {
				t.Fatal(err)
			}
			canary, err := m.CreateDeployment(ctx, state.Deployment{
				AppID: app.ID, ImageDigest: "sha256:canary", CanaryPreset: "balanced",
				CanaryTotalSteps: 2, TrafficPercent: 10,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := m.MarkDeploymentLive(ctx, canary.ID); err != nil {
				t.Fatal(err)
			}
			if _, _, err := m.RecoverRollout(tc.ctx(ctx), app.ID, "abort", "5xx regression"); err != nil {
				t.Fatal(err)
			}
			rows, err := m.ListDeploymentAudit(ctx, canary.ID, 10)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if row.Kind == state.DeployRolledBack {
					if row.Actor != tc.want {
						t.Fatalf("rolled_back actor = %q, want %q", row.Actor, tc.want)
					}
					return
				}
			}
			t.Fatalf("no rolled_back audit row in %+v", rows)
		})
	}
}
