package state_test

// adr: 596

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func runtimeReleaseStores(t *testing.T, run func(*testing.T, state.Store, state.RuntimeReleaseStore)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { s := state.NewMemStore(); run(t, s, s) })
	t.Run("postgres", func(t *testing.T) { s, _ := pgStore(t); run(t, s, s) })
}
func runtimeReleaseFixture(n string) state.RuntimeRelease {
	r := state.RuntimeRelease{Runtime: "node22", Architecture: "amd64", SourceRef: "ghcr.io/test/node@sha256:" + strings.Repeat(n, 64), GuestInitSHA256: strings.Repeat("a", 64), LayoutVersion: "test-layout", BaseSHA256: strings.Repeat("b", 64)}
	r.ID = r.Identity()
	return r
}
func TestRuntimeReleaseCanonicalPublicationAndArtifactFences(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		ctx := t.Context()
		acct, err := s.CreateAccount(ctx, uuid.NewString()+"@runtime.test", api.PlanFree)
		if err != nil {
			t.Fatal(err)
		}
		app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "runtime-" + uuid.NewString()[:8], Type: state.AppTypeFunction, Runtime: "node22"})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployImaging})
		if err != nil {
			t.Fatal(err)
		}
		key := "apps/" + app.Slug + "/" + dep.ID + ".ext4"
		if err := s.SetDeploymentRootfs(ctx, dep.ID, "/test/layer", key, 10); err != nil {
			t.Fatal(err)
		}
		first, err := releases.PublishRuntimeRelease(ctx, runtimeReleaseFixture("1"))
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				other := runtimeReleaseFixture("1")
				other.BaseSHA256 = strings.Repeat("c", 64)
				other.ID = other.Identity()
				got, err := releases.PublishRuntimeRelease(ctx, other)
				if err != nil || got.ID != first.ID || !got.CreatedAt.Equal(first.CreatedAt) {
					t.Errorf("publication changed canonical generation: %+v %v", got, err)
				}
			}()
		}
		wg.Wait()
		target, err := releases.PublishRuntimeRelease(ctx, runtimeReleaseFixture("2"))
		if err != nil {
			t.Fatal(err)
		}
		if err := releases.BindDeploymentRuntimeRelease(ctx, dep.ID, key, first.ID); err != nil {
			t.Fatal(err)
		}
		if err := releases.BindDeploymentRuntimeRelease(ctx, dep.ID, key, target.ID); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("mutable binding: %v", err)
		}
		if err := releases.BindDeploymentRuntimeRelease(ctx, dep.ID, key+"-wrong", first.ID); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("missing artifact fence: %v", err)
		}
		// A promotion/clone reusing physical bytes reads the same generation.
		cloneApp, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "clone-" + uuid.NewString()[:8], Type: state.AppTypeFunction, Runtime: "node22"})
		if err != nil {
			t.Fatal(err)
		}
		clone, err := s.CreateDeployment(ctx, state.Deployment{AppID: cloneApp.ID, Kind: state.DeploymentKindTarball, Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetDeploymentRootfs(ctx, clone.ID, "/test/layer", key, 10); err != nil {
			t.Fatal(err)
		}
		got, err := releases.RuntimeReleaseForArtifact(ctx, acct.ID, key)
		if err != nil || got.ID != first.ID {
			t.Fatal(got, err)
		}
		if _, err := releases.RuntimeReleaseForArtifact(ctx, uuid.NewString(), key); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("cross-account binding: %v", err)
		}
		if err := s.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
			t.Fatal(err)
		}
		if err := releases.BindDeploymentRuntimeRelease(ctx, dep.ID, key, first.ID); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("terminal publication: %v", err)
		}
		list, err := releases.ListRuntimeReleases(ctx, "node22", "amd64")
		if err != nil || len(list) != 2 {
			t.Fatal(list, err)
		}
		wrong := runtimeReleaseFixture("3")
		wrong.ID = strings.Repeat("0", 64)
		if _, err := releases.PublishRuntimeRelease(ctx, wrong); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatal("forged identity accepted", err)
		}
	})
}
func TestRuntimeReleaseBuildRefCommitsWithClaim(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		ctx := t.Context()
		acct, err := s.CreateAccount(ctx, uuid.NewString()+"@build-runtime.test", api.PlanFree)
		if err != nil {
			t.Fatal(err)
		}
		app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "build-" + uuid.NewString()[:8]})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployBuilding})
		if err != nil {
			t.Fatal(err)
		}
		build, err := s.CreateBuild(ctx, dep.ID, state.DeploymentKindTarball, 10, "")
		if err != nil {
			t.Fatal(err)
		}
		claim, err := s.ClaimQueuedBuild(ctx, build.ID)
		if err != nil {
			t.Fatal(err)
		}
		ref := runtimeReleaseFixture("1").SourceRef
		prov := state.BuildProvenance{BuildID: build.ID, RuntimeBaseRef: ref, SourceSHA256: strings.Repeat("a", 64), StartedAt: claim.StartedAt, FinishedAt: time.Now(), Plan: "free"}
		stale := claim
		stale.StartedAt = stale.StartedAt.Add(-time.Second)
		if err := s.CompleteBuild(ctx, stale, "/test/export", "apps/layer.ext4", 10, prov); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("stale claim", err)
		}
		if _, err := releases.BuildRuntimeBaseRef(ctx, build.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("stale build published ref", err)
		}
		if err := s.CompleteBuild(ctx, claim, "/test/export", "apps/layer.ext4", 10, prov); err != nil {
			t.Fatal(err)
		}
		if got, err := releases.BuildRuntimeBaseRef(ctx, build.ID); err != nil || got != ref {
			t.Fatal("lost host runtime ref", got, err)
		}
	})
}
