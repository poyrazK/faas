package state_test

// adr: 20261028090000 — a claimed build whose deployment a newer revision
// already outpaces is cancelled and its deployment superseded, fenced on the
// claim; the newest revision's build always runs.

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type outpacedBuildTestStore interface {
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateApp(context.Context, state.App) (state.App, error)
	CreateDeployment(context.Context, state.Deployment) (state.Deployment, error)
	UpdateDeploymentStatus(context.Context, string, state.DeploymentStatus, string) error
	CreateBuild(context.Context, string, state.DeploymentKind, int64, string) (state.Build, error)
	ClaimQueuedBuild(context.Context, string) (state.Build, error)
	BuildByID(context.Context, string) (state.Build, error)
	DeploymentByID(context.Context, string) (state.Deployment, error)
	state.OutpacedBuildStore
}

func TestMemSupersedeOutpacedBuild(t *testing.T) {
	exerciseSupersedeOutpacedBuild(t, state.NewMemStore())
}

func TestPgSupersedeOutpacedBuild(t *testing.T) {
	s, _ := pgStore(t)
	exerciseSupersedeOutpacedBuild(t, s)
}

func exerciseSupersedeOutpacedBuild(t *testing.T, s outpacedBuildTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, err := s.CreateAccount(ctx, "outpaced@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "outpaced", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	queue := func(sha string) (state.Deployment, state.Build) {
		t.Helper()
		d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployPending, SourcePath: "/spool/" + sha + ".tar.gz"})
		if err != nil {
			t.Fatalf("CreateDeployment: %v", err)
		}
		b, err := s.CreateBuild(ctx, d.ID, state.DeploymentKindTarball, 100, "")
		if err != nil {
			t.Fatalf("CreateBuild: %v", err)
		}
		return d, b
	}
	claim := func(b state.Build) state.Build {
		t.Helper()
		c, err := s.ClaimQueuedBuild(ctx, b.ID)
		if err != nil {
			t.Fatalf("claim %s: %v", b.ID, err)
		}
		return c
	}
	// a is superseded while pending when b arrives, which leaves its build
	// queued; b is building when c arrives, so CreateDeployment leaves it.
	a, aBuild := queue("sha-1")
	b, bBuild := queue("sha-2")
	if err := s.UpdateDeploymentStatus(ctx, b.ID, state.DeployBuilding, ""); err != nil {
		t.Fatalf("mark building: %v", err)
	}
	c, cBuild := queue("sha-3")

	if done, err := s.SupersedeOutpacedBuild(ctx, claim(cBuild)); err != nil || done {
		t.Fatalf("newest revision: superseded=%v err=%v; want it built", done, err)
	}
	bClaim := claim(bBuild)
	stale := bClaim
	stale.StartedAt = bClaim.StartedAt.Add(-time.Minute)
	if done, err := s.SupersedeOutpacedBuild(ctx, stale); err != nil || done {
		t.Fatalf("stale claim: superseded=%v err=%v; want the fence to refuse", done, err)
	}
	for _, tc := range []struct {
		dep   state.Deployment
		claim state.Build
	}{{b, bClaim}, {a, claim(aBuild)}} {
		if done, err := s.SupersedeOutpacedBuild(ctx, tc.claim); err != nil || !done {
			t.Fatalf("outpaced build of %s: superseded=%v err=%v; want it stopped", tc.dep.ID, done, err)
		}
		build, err := s.BuildByID(ctx, tc.claim.ID)
		if err != nil || build.Status != state.BuildCancelled {
			t.Fatalf("outpaced build = %+v (%v); want cancelled", build, err)
		}
		dep, err := s.DeploymentByID(ctx, tc.dep.ID)
		if err != nil || dep.Status != state.DeploySuperseded {
			t.Fatalf("outpaced deployment = %s (%v); want superseded", dep.Status, err)
		}
		if done, err := s.SupersedeOutpacedBuild(ctx, tc.claim); err != nil || done {
			t.Fatalf("repeat: superseded=%v err=%v; want no second transition", done, err)
		}
	}
	if dep, err := s.DeploymentByID(ctx, c.ID); err != nil || dep.Status == state.DeploySuperseded {
		t.Fatalf("newest deployment = %s (%v); want it untouched", dep.Status, err)
	}
}
