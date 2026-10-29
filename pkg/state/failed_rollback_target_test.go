package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type failedRollbackStore interface {
	CreateDeployment(context.Context, state.Deployment) (state.Deployment, error)
	MarkDeploymentLive(context.Context, string) error
	PrepareDeploymentRollback(context.Context, string, string) (state.Deployment, error)
	SetDeploymentFailedEx(context.Context, string, string, string, string, string, string, []api.LogExcerpt) (state.Deployment, error)
	UpdateDeploymentStatus(context.Context, string, state.DeploymentStatus, string) error
	DeploymentByID(context.Context, string) (state.Deployment, error)
	LatestSupersededDeployment(context.Context, string, string) (state.Deployment, error)
	UpdateDeploymentTraffic(context.Context, string, int, ...string) (state.Deployment, error)
}

// runFailedRollbackScenario replays production-us finding 14: a rollback
// target failed hosting verification (a transient 429 storm) and was marked
// failed for good, so the release could never be rolled back to again. A
// release re-primed by a rollback now returns to superseded with its error
// kept. A deployment that never served still fails normally.
func runFailedRollbackScenario(t *testing.T, ctx context.Context, s failedRollbackStore, appID string) {
	t.Helper()
	deploy := func(name string) state.Deployment {
		t.Helper()
		d, err := s.CreateDeployment(ctx, state.Deployment{
			ID: uuid.NewString(), AppID: appID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:" + name, CreatedAt: time.Now(), RootfsKey: "apps/x/" + name + ".ext4",
		})
		if err != nil {
			t.Fatalf("CreateDeployment %s: %v", name, err)
		}
		return d
	}
	v1 := deploy("v1")
	if err := s.MarkDeploymentLive(ctx, v1.ID); err != nil {
		t.Fatal(err)
	}
	v2 := deploy("v2")
	if err := s.MarkDeploymentLive(ctx, v2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareDeploymentRollback(ctx, appID, v1.ID); err != nil {
		t.Fatalf("PrepareDeploymentRollback: %v", err)
	}
	if _, err := s.SetDeploymentFailedEx(ctx, v1.ID, api.CodeDeploymentVerificationUnavailable, "hosting verification unavailable", "", "", "", nil); err != nil {
		t.Fatalf("SetDeploymentFailedEx: %v", err)
	}
	got, err := s.DeploymentByID(ctx, v1.ID)
	if err != nil || got.Status != state.DeploySuperseded {
		t.Fatalf("rollback target after a failed re-prime = %q err=%v, want superseded", got.Status, err)
	}
	if target, err := s.LatestSupersededDeployment(ctx, appID, state.DefaultEnvScope); err != nil || target.ID != v1.ID {
		t.Fatalf("rollback target = %+v err=%v, want v1 still eligible", target.ID, err)
	}

	v3 := deploy("v3")
	if err := s.UpdateDeploymentStatus(ctx, v3.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetDeploymentFailedEx(ctx, v3.ID, api.CodeAppStartupTimeout, "app did not become ready", "", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if got, err := s.DeploymentByID(ctx, v3.ID); err != nil || got.Status != state.DeployFailed {
		t.Fatalf("first deploy failure = %q err=%v, want failed", got.Status, err)
	}
}

func TestMem_FailedRollbackKeepsTarget(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "rbfail-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "rbfail", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	runFailedRollbackScenario(t, ctx, m, app.ID)
	split, err := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "rbfail-split", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	runFailedRollbackDuringSplitScenario(t, ctx, m, split.ID)
}

func TestPg_FailedRollbackKeepsTarget(t *testing.T) {
	s, ctx := pgStore(t)
	_, app := seedPgAccountAndApp(t, s, ctx)
	runFailedRollbackScenario(t, ctx, s, app.ID)
}

func TestPg_FailedRollbackDuringSplitKeepsTraffic(t *testing.T) {
	s, ctx := pgStore(t)
	_, app := seedPgAccountAndApp(t, s, ctx)
	runFailedRollbackDuringSplitScenario(t, ctx, s, app.ID)
}

// runFailedRollbackDuringSplitScenario replays hunt #10 H10-11: with v2 and
// v3 splitting traffic 50/50, a rollback to v1 failed hosting verification
// and the failure rebalance moved all traffic to v3. A failure of a row that
// was not serving must leave a whole split as it was.
func runFailedRollbackDuringSplitScenario(t *testing.T, ctx context.Context, s failedRollbackStore, appID string) {
	t.Helper()
	deploy := func(name string, explicitZero bool) state.Deployment {
		t.Helper()
		d, err := s.CreateDeployment(ctx, state.Deployment{
			ID: uuid.NewString(), AppID: appID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:" + name, CreatedAt: time.Now(), RootfsKey: "apps/x/" + name + ".ext4",
			TrafficPercentExplicit: explicitZero,
		})
		if err != nil {
			t.Fatalf("CreateDeployment %s: %v", name, err)
		}
		return d
	}
	v1 := deploy("v1", false)
	if err := s.MarkDeploymentLive(ctx, v1.ID); err != nil {
		t.Fatal(err)
	}
	v2 := deploy("v2", false)
	if err := s.MarkDeploymentLive(ctx, v2.ID); err != nil {
		t.Fatal(err)
	}
	v3 := deploy("v3", true)
	if err := s.MarkDeploymentLive(ctx, v3.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateDeploymentTraffic(ctx, v3.ID, 50); err != nil {
		t.Fatalf("split v3 to 50%%: %v", err)
	}
	if _, err := s.PrepareDeploymentRollback(ctx, appID, v1.ID); err != nil {
		t.Fatalf("PrepareDeploymentRollback: %v", err)
	}
	if _, err := s.SetDeploymentFailedEx(ctx, v1.ID, api.CodeDeploymentVerificationUnavailable, "hosting verification unavailable", "", "", "", nil); err != nil {
		t.Fatalf("SetDeploymentFailedEx: %v", err)
	}
	for _, d := range []state.Deployment{v2, v3} {
		got, err := s.DeploymentByID(ctx, d.ID)
		if err != nil || got.Status != state.DeployLive || got.TrafficPercent != 50 {
			t.Fatalf("%s after the failed rollback = %s at %d%% err=%v, want live at 50%%", d.ImageDigest, got.Status, got.TrafficPercent, err)
		}
	}
}
