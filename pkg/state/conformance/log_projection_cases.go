package conformance

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// testLogProjectionSources pins the keyset reads the ADR-20261018083536 slice C
// projector tails: tenant resolution through the deployment's app, oldest
// first, strictly after the cursor, strictly before the upper bound, capped.
func testLogProjectionSources(t *testing.T, fx *Fixture) {
	t.Helper()
	start := time.Now().UTC().Add(-time.Minute)
	deploymentID := uuid.MustParse(fx.Deployment.ID)
	for _, kind := range []state.DeploymentAuditKind{state.DeployCreated, state.DeployRolledBack} {
		if _, err := fx.Store.AppendDeploymentAudit(fx.Ctx, state.DeploymentAudit{
			DeploymentID: deploymentID, Kind: kind, Actor: "conformance", Data: json.RawMessage(`{"trigger_kind":"error_rate"}`),
		}); err != nil {
			t.Fatalf("AppendDeploymentAudit(%s): %v", kind, err)
		}
	}
	// A row whose deployment is gone has no tenant to project into.
	if _, err := fx.Store.AppendDeploymentAudit(fx.Ctx, state.DeploymentAudit{DeploymentID: uuid.New(), Kind: state.DeployCreated, Actor: "conformance"}); err != nil {
		t.Fatalf("AppendDeploymentAudit(orphan): %v", err)
	}
	until := time.Now().UTC().Add(time.Minute)

	audit, err := fx.Store.ListDeploymentAuditForLedger(fx.Ctx, start, 0, until, 10)
	if err != nil {
		t.Fatalf("ListDeploymentAuditForLedger: %v", err)
	}
	if len(audit) != 2 || audit[0].Kind != state.DeployCreated || audit[1].Kind != state.DeployRolledBack {
		t.Fatalf("audit rows = %+v, want created then rolled_back", audit)
	}
	for _, row := range audit {
		if row.AccountID != fx.Account.ID || row.AppID != fx.App.ID || row.DeploymentID != fx.Deployment.ID ||
			row.Actor != "conformance" || !strings.Contains(string(row.Data), "error_rate") {
			t.Fatalf("audit row = %+v, want the fixture tenant and payload", row)
		}
	}
	if next, err := fx.Store.ListDeploymentAuditForLedger(fx.Ctx, audit[0].At, audit[0].ID, until, 10); err != nil || len(next) != 1 || next[0].ID != audit[1].ID {
		t.Fatalf("after first row = %+v, %v; want only the second", next, err)
	}
	if capped, err := fx.Store.ListDeploymentAuditForLedger(fx.Ctx, start, 0, until, 1); err != nil || len(capped) != 1 || capped[0].ID != audit[0].ID {
		t.Fatalf("limit 1 = %+v, %v; want the oldest row", capped, err)
	}
	if early, err := fx.Store.ListDeploymentAuditForLedger(fx.Ctx, start, 0, audit[0].At, 10); err != nil || len(early) != 0 {
		t.Fatalf("until at the first row = %+v, %v; want none (exclusive)", early, err)
	}

	dep, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: fx.App.ID, Kind: state.DeploymentKindTarball, Status: state.DeployBuilding})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	failed, err := fx.Store.CreateBuild(fx.Ctx, dep.ID, dep.Kind, 1, "")
	if err != nil {
		t.Fatalf("CreateBuild: %v", err)
	}
	queued := seedQueuedBuild(t, fx) // never finishes, so never projected
	claim, err := fx.Store.ClaimQueuedBuild(fx.Ctx, failed.ID)
	if err != nil {
		t.Fatalf("ClaimQueuedBuild: %v", err)
	}
	if err := fx.Store.FailBuild(fx.Ctx, claim, state.FailureTimeout, "build exceeded 15m"); err != nil {
		t.Fatalf("FailBuild: %v", err)
	}
	outcomes, err := fx.Store.ListBuildOutcomesForLedger(fx.Ctx, start, "", time.Now().UTC().Add(time.Minute), 10)
	if err != nil {
		t.Fatalf("ListBuildOutcomesForLedger: %v", err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("build outcomes = %+v, want only the failed build (not %s)", outcomes, queued.ID)
	}
	got := outcomes[0]
	if got.BuildID != failed.ID || got.AccountID != fx.Account.ID || got.AppID != fx.App.ID || got.DeploymentID != failed.DeploymentID ||
		got.Status != state.BuildFailed || got.FailureClass != state.FailureTimeout || got.StartedAt.IsZero() ||
		got.FinishedAt.Before(got.StartedAt) || !strings.Contains(got.DeploymentError, "build exceeded 15m") {
		t.Fatalf("build outcome = %+v", got)
	}
	if next, err := fx.Store.ListBuildOutcomesForLedger(fx.Ctx, got.FinishedAt, got.BuildID, time.Now().UTC().Add(time.Minute), 10); err != nil || len(next) != 0 {
		t.Fatalf("after the outcome = %+v, %v; want none", next, err)
	}
}
