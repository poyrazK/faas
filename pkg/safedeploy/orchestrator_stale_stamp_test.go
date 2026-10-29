package safedeploy

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// abortAfterList aborts every rollout the orchestrator lists, after the list
// is read and before any stamp, as APID would in between.
type abortAfterList struct {
	mem *state.MemStore
}

func (a abortAfterList) SafedeployListPendingRollouts(ctx context.Context) ([]state.Deployment, error) {
	rows, err := a.mem.SafedeployListPendingRollouts(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range rows {
		at := time.Now()
		if _, err := a.mem.SafedeployStampRollout(ctx, d.ID, "aborted", d.RolloutStartedAt, nil, &at, "operator abort"); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func (a abortAfterList) SafedeployStampRollout(ctx context.Context, id, rolloutState string, startedAt, completedAt, abortedAt *time.Time, abortedReason string) (state.Deployment, error) {
	return a.mem.SafedeployStampRollout(ctx, id, rolloutState, startedAt, completedAt, abortedAt, abortedReason)
}

func (a abortAfterList) AppendDeploymentAudit(ctx context.Context, entry state.DeploymentAudit) (int64, error) {
	return a.mem.AppendDeploymentAudit(ctx, entry)
}

// Hunt 20 H20-2: a rollout aborted after the orchestrator listed it stays
// aborted. The orchestrator used to stamp it complete (losing the abort) or
// rolling_out (where the canary worker raised its traffic again).
func TestOrchestratorLeavesARolloutAbortedAfterTheList(t *testing.T) {
	for _, tc := range []struct {
		name  string
		step  int
		state string
	}{
		{name: "a finished ladder would complete", step: 4, state: "rolling_out"},
		{name: "a pending ladder would start", step: 0, state: "pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			mem := state.NewMemStore()
			acct, err := mem.CreateAccount(ctx, "stale-stamp-"+uuid.NewString()+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "stale-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5, IdleTimeoutS: 30})
			if err != nil {
				t.Fatal(err)
			}
			id := uuid.NewString()
			if _, err := mem.CreateDeployment(ctx, state.Deployment{ID: id, AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + id,
				RootfsKey: "apps/x/" + id + ".ext4", Status: state.DeployBuilding, TrafficPercent: 1, TrafficPercentExplicit: true,
				CanaryPreset: "balanced", CanaryTotalSteps: 4, RolloutState: "pending"}); err != nil {
				t.Fatal(err)
			}
			if err := mem.MarkDeploymentLive(ctx, id); err != nil {
				t.Fatal(err)
			}
			if err := mem.SetDeploymentCanaryState(ctx, id, "balanced", tc.step, 4, time.Now(), tc.state); err != nil {
				t.Fatal(err)
			}

			o := NewOrchestrator(abortAfterList{mem: mem}, slog.New(slog.NewTextHandler(io.Discard, nil)), "meterd:safedeploy", "")
			stats, listed, err := o.Once(ctx)
			if err != nil || listed != 1 {
				t.Fatalf("Once = %d listed, err %v", listed, err)
			}
			if stats.Started != 0 || stats.Completed != 0 || stats.AuditEmitFailed != 0 {
				t.Fatalf("stats = %+v, want nothing stamped", stats)
			}
			d, err := mem.DeploymentByID(ctx, id)
			if err != nil || d.RolloutState != "aborted" || d.RolloutAbortedAt == nil || d.RolloutAbortedReason != "operator abort" {
				t.Fatalf("rollout = %+v (err %v), want it still aborted with its reason", d, err)
			}
		})
	}
}
