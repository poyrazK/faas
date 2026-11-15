package apidserver_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261021221906 — a run of live-patched developer syncs builds once: each new
// sync cancels the app's still-deferred builds, a published patch keeps its
// own build deferred and says until when, and a patch that did not reach
// the instance starts the build at once.
func TestDevBuildCoalescing(t *testing.T) {
	f := newDevPatchFixture(t, devPatchBaseSource, true)
	t.Setenv(apidserver.DevPatchDeliveryEnv, "1")
	ctx := context.Background()
	srv := *f.e.S()
	acct := *f.e.Acct()
	notBefore := time.Now().Add(time.Hour).UTC()
	deferredSync := func() (state.Deployment, state.Build) {
		t.Helper()
		dep, err := f.store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		build, _, err := f.store.CreateDeferredBuildWithID(ctx, uuid.NewString(), dep.ID, state.DeploymentKindTarball, 10, "", notBefore, nil)
		if err != nil {
			t.Fatal(err)
		}
		return dep, build
	}

	first, firstBuild := deferredSync()
	second, secondBuild := deferredSync()
	srv.CancelSupersededDevBuilds(ctx, acct, f.app, second.ID)
	if dep, err := f.store.DeploymentByID(ctx, first.ID); err != nil || dep.Status != state.DeployCancelled {
		t.Fatalf("superseded deployment = %s, %v; want cancelled", dep.Status, err)
	}
	if build, err := f.store.BuildByID(ctx, firstBuild.ID); err != nil || build.Status != state.BuildCancelled {
		t.Fatalf("superseded build = %s, %v; want cancelled before it ran", build.Status, err)
	}
	if ids, err := f.store.DeferredDeployments(ctx, f.app.ID); err != nil || len(ids) != 1 || ids[0] != second.ID {
		t.Fatalf("deferred deployments = %v, %v; want only the newest sync", ids, err)
	}

	// The newest sync's patch is published: its build keeps waiting and the
	// response carries the deadline.
	sourcePath := writeDevSourceArchive(t, map[string]string{"package.json": "{}", "src/app.js": "v2", "src/old.js": "old"})
	plan := srv.PlanDevPatch(ctx, f.app, sourcePath, "", f.limits)
	preview := *plan.Preview()
	if preview == nil || !preview.Eligible {
		t.Fatalf("preview = %+v, want an eligible live patch", preview)
	}
	published := srv.RecordDevPatchSource(ctx, f.app, second.ID, sourcePath, "", plan)
	srv.SettleDeferredDevBuild(ctx, f.app, second.ID, notBefore, published, preview)
	if !published || preview.BuildDeferredUntil == nil || !preview.BuildDeferredUntil.Equal(notBefore) {
		t.Fatalf("published=%t deferred until %v, want %v", published, preview.BuildDeferredUntil, notBefore)
	}
	if _, err := f.store.ClaimQueuedBuild(ctx, secondBuild.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deferred build claimable before its window: %v", err)
	}

	// A failed patch starts the build now.
	srv.StartDeferredDevBuilds(ctx, f.app)
	if build, err := f.store.ClaimQueuedBuild(ctx, secondBuild.ID); err != nil || build.Status != state.BuildRunning {
		t.Fatalf("build after a failed patch = %+v, %v; want claimable", build, err)
	}

	// A sync whose patch was not published builds at once too.
	third, thirdBuild := deferredSync()
	unpublished := &api.DevPatchPreview{Eligible: true, ChangedPaths: 1}
	srv.SettleDeferredDevBuild(ctx, f.app, third.ID, notBefore, false, unpublished)
	if unpublished.BuildDeferredUntil != nil {
		t.Fatal("an unpublished patch reported a deferred build")
	}
	if build, err := f.store.ClaimQueuedBuild(ctx, thirdBuild.ID); err != nil || build.Status != state.BuildRunning {
		t.Fatalf("build of an unpublished patch = %+v, %v; want claimable", build, err)
	}
}
