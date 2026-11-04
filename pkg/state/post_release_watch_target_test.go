package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type postReleaseTargetStore interface {
	CreateDeployment(context.Context, state.Deployment) (state.Deployment, error)
	MarkDeploymentLive(context.Context, string) error
	MarkDeploymentSuperseded(context.Context, string) error
	SafedeployStampRollout(context.Context, string, string, *time.Time, *time.Time, *time.Time, string) (state.Deployment, error)
	ListPostReleaseWatchCandidates(context.Context, time.Duration, int) ([]state.PostReleaseWatchCandidate, error)
}

// adr: 1445 amendment 1 — the watch's predecessor is the plain rollback's
// target. A canary aborted between v1 and v3 is superseded once v3 completes;
// ordered by creation it was v3's predecessor, so the watch compared v3 with a
// release that never completed and rolled back to it.
func runPostReleaseTargetScenario(t *testing.T, ctx context.Context, s postReleaseTargetStore, appID string) {
	t.Helper()
	base := time.Now().UTC().Add(-time.Minute)
	deploy := func(name string, offset time.Duration, d state.Deployment) state.Deployment {
		t.Helper()
		d.ID, d.AppID, d.Kind, d.ImageDigest = uuid.NewString(), appID, state.DeploymentKindImage, "sha256:"+name
		d.CreatedAt, d.RootfsKey = base.Add(offset), "apps/x/"+name+".ext4"
		created, err := s.CreateDeployment(ctx, d)
		if err != nil {
			t.Fatalf("CreateDeployment %s: %v", name, err)
		}
		return created
	}
	v1 := deploy("v1", 0, state.Deployment{})
	if err := s.MarkDeploymentLive(ctx, v1.ID); err != nil {
		t.Fatal(err)
	}
	aborted := deploy("v2", time.Second, state.Deployment{CanaryPreset: "balanced", CanaryTotalSteps: 4, RolloutState: "pending"})
	now := time.Now().UTC()
	if _, err := s.SafedeployStampRollout(ctx, aborted.ID, "aborted", &now, nil, &now, "circuit breaker"); err != nil {
		t.Fatalf("abort v2: %v", err)
	}
	if err := s.MarkDeploymentSuperseded(ctx, aborted.ID); err != nil {
		t.Fatal(err)
	}
	v3 := deploy("v3", 2*time.Second, state.Deployment{RollbackOn5xx: true})
	if err := s.MarkDeploymentLive(ctx, v3.ID); err != nil {
		t.Fatal(err)
	}
	started, completed := now.Add(-time.Minute), now
	if _, err := s.SafedeployStampRollout(ctx, v3.ID, "complete", &started, &completed, nil, ""); err != nil {
		t.Fatalf("complete v3: %v", err)
	}
	rows, err := s.ListPostReleaseWatchCandidates(ctx, time.Hour, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.DeploymentID == v3.ID {
			if row.PredecessorID != v1.ID {
				t.Fatalf("v3's predecessor = %s, want v1 %s (aborted canary %s)", row.PredecessorID, v1.ID, aborted.ID)
			}
			return
		}
	}
	t.Fatalf("v3 is not a watch candidate: %+v", rows)
}

func TestMem_PostReleaseWatchSkipsAnAbortedCanary(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "prw-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "prw-target", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	runPostReleaseTargetScenario(t, ctx, m, app.ID)
}

func TestPg_PostReleaseWatchSkipsAnAbortedCanary(t *testing.T) {
	s, ctx := pgStore(t)
	_, app := seedPgAccountAndApp(t, s, ctx)
	runPostReleaseTargetScenario(t, ctx, s, app.ID)
}
