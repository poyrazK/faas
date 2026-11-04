package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type deferredBuildStore = contractTestStore

func TestDeferredBuildMem(t *testing.T) {
	runDeferredBuildContract(t, state.NewMemStore(), context.Background())
}

func TestDeferredBuildPg(t *testing.T) {
	s, ctx := pgStore(t)
	runDeferredBuildContract(t, s, ctx)
}

// adr: 20261021221906 — no claim path takes a deferred build before its not_before,
// the queue count leaves it out, and starting it makes it claimable now.
func runDeferredBuildContract(t *testing.T, s deferredBuildStore, ctx context.Context) {
	t.Helper()
	acct, err := s.CreateAccount(ctx, "deferred-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "deferred-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	newBuild := func(notBefore time.Time) state.Build {
		t.Helper()
		dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		build, _, err := s.CreateDeferredBuildWithID(ctx, uuid.NewString(), dep.ID, state.DeploymentKindTarball, 100, "", notBefore, nil)
		if err != nil {
			t.Fatal(err)
		}
		if build.Status != state.BuildQueued {
			t.Fatalf("deferred build status = %s, want queued", build.Status)
		}
		return build
	}
	before, err := s.QueuedBuildsCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	deferred := newBuild(time.Now().Add(time.Hour))
	if _, err := s.ClaimQueuedBuild(ctx, deferred.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("notification claim of a deferred build = %v, want ErrNotFound", err)
	}
	if b, err := s.ClaimNextQueuedBuild(ctx); err == nil && b.ID == deferred.ID {
		t.Fatal("the polling claim took a deferred build")
	}
	if b, err := s.ClaimNextQueuedBuildWithFairness(ctx, time.Minute); err == nil && b.ID == deferred.ID {
		t.Fatal("the fairness claim took a deferred build")
	}
	if n, err := s.QueuedBuildsCount(ctx); err != nil || n > before {
		t.Fatalf("queued builds = %d, %v; a deferred build is not backlog (was %d)", n, err, before)
	}
	started, found, err := s.StartDeferredBuild(ctx, deferred.DeploymentID)
	if err != nil || !found || started.ID != deferred.ID {
		t.Fatalf("StartDeferredBuild = %+v %t %v", started, found, err)
	}
	if _, found, err := s.StartDeferredBuild(ctx, deferred.DeploymentID); err != nil || found {
		t.Fatalf("second StartDeferredBuild found=%t err=%v, want nothing left to start", found, err)
	}
	if claimed, err := s.ClaimQueuedBuild(ctx, deferred.ID); err != nil || claimed.Status != state.BuildRunning {
		t.Fatalf("claim after start = %+v, %v", claimed, err)
	}
	// A not_before already in the past is due at once.
	due := newBuild(time.Now().Add(-time.Second))
	if claimed, err := s.ClaimQueuedBuild(ctx, due.ID); err != nil || claimed.ID != due.ID {
		t.Fatalf("claim of a due build = %+v, %v", claimed, err)
	}
	// Only builds still queued and deferred are listed.
	waiting := newBuild(time.Now().Add(time.Hour))
	if ids, err := s.DeferredDeployments(ctx, app.ID); err != nil || len(ids) != 1 || ids[0] != waiting.DeploymentID {
		t.Fatalf("DeferredDeployments = %v, %v; want [%s]", ids, err, waiting.DeploymentID)
	}
	if ids, err := s.DeferredDeployments(ctx, uuid.NewString()); err != nil || len(ids) != 0 {
		t.Fatalf("DeferredDeployments of another app = %v, %v", ids, err)
	}
	if _, found, err := s.StartDeferredBuild(ctx, uuid.NewString()); err != nil || found {
		t.Fatalf("StartDeferredBuild of an unknown deployment found=%t err=%v", found, err)
	}
}
