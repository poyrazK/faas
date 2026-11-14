// adr: 20261028110044 — amendment 7: a canary rollout audits starting and completing.
package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// The live path starts a canary and the advance path completes it without the
// safe-deploy orchestrator, which audited only its own transitions; each must
// record the lifecycle row under the app's account, once.
func TestPg_CanaryRolloutAuditsStartAndCompletion(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	accountID, appID, priorID := seedLiveDeploy(t, s, ctx, "rollout-lifecycle-audit", "rollout-lifecycle-audit")
	candidate, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:rollout-lifecycle-audit",
		Status: state.DeployPending, Scope: "default", CanaryTotalSteps: 1, TrafficPercent: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, candidate.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	if _, err := pool.Exec(ctx, `update deployments set traffic_percent = 80 where id = $1`, priorID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AdvanceCanary(ctx, candidate.ID, state.CanaryAdvanceParams{
		ExpectedStep: 0, TrafficPercent: 100,
		Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "test"},
	}); err != nil {
		t.Fatalf("terminal advance: %v", err)
	}
	for _, kind := range []state.DeploymentAuditKind{state.DeployRolloutStarted, state.DeployRolloutCompleted} {
		var rows, scoped int
		if err := pool.QueryRow(ctx,
			`select count(*), count(*) filter (where account_id = $3::uuid)
			   from deployment_audit where deployment_id = $1::uuid and kind = $2`,
			candidate.ID, string(kind), accountID).Scan(&rows, &scoped); err != nil {
			t.Fatal(err)
		}
		if rows != 1 || scoped != 1 {
			t.Fatalf("%s rows=%d with account=%d, want exactly one under the app's account", kind, rows, scoped)
		}
	}
}
