package state_test

// adr: 683

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func runtimeUpgradeFixture(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) (state.App, state.Deployment, state.RuntimeRelease) {
	t.Helper()
	return runtimeUpgradeFixtureEdited(t, s, releases, nil)
}

func runtimeUpgradeFixtureEdited(t *testing.T, s state.Store, releases state.RuntimeReleaseStore, edit func(*state.App, *state.Deployment)) (state.App, state.Deployment, state.RuntimeRelease) {
	t.Helper()
	acct, err := s.CreateAccount(t.Context(), uuid.NewString()+"@upgrade.test", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	app := state.App{AccountID: acct.ID, Slug: "upgrade-" + uuid.NewString()[:8], Type: state.AppTypeFunction, Runtime: "node22", Status: state.AppActive}
	dep := state.Deployment{Kind: state.DeploymentKindTarball, SourceSHA256: strings.Repeat("c", 64), SourceBytes: 20, SourceRoot: "function", Handler: "index.handler"}
	if edit != nil {
		edit(&app, &dep)
	}
	app, err = s.CreateApp(t.Context(), app)
	if err != nil {
		t.Fatal(err)
	}
	dep.AppID = app.ID
	dep, err = s.CreateDeployment(t.Context(), dep)
	if err != nil {
		t.Fatal(err)
	}
	target, err := releases.PublishRuntimeRelease(t.Context(), runtimeReleaseFixture("1"))
	if err != nil {
		t.Fatal(err)
	}
	return app, dep, target
}

func TestRuntimeUpgradeTargetImmutableBeforeQueueAndRetainedOnRetry(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		app, dep, target := runtimeUpgradeFixture(t, s, releases)
		pins := s.(state.RuntimeUpgradeTargetStore)
		if _, err := pins.DeploymentRuntimeUpgradeTarget(t.Context(), dep.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("invented target", err)
		}
		if err := pins.PinDeploymentRuntimeUpgradeTarget(t.Context(), dep.ID, target.ID, strings.Repeat("d", 64)); !errors.Is(err, state.ErrConflict) {
			t.Fatal("accepted stale source", err)
		}
		for range 2 {
			if err := pins.PinDeploymentRuntimeUpgradeTarget(t.Context(), dep.ID, target.ID, dep.SourceSHA256); err != nil {
				t.Fatal(err)
			}
		}
		other, err := releases.PublishRuntimeRelease(t.Context(), runtimeReleaseFixture("2"))
		if err != nil {
			t.Fatal(err)
		}
		if err := pins.PinDeploymentRuntimeUpgradeTarget(t.Context(), dep.ID, other.ID, dep.SourceSHA256); !errors.Is(err, state.ErrConflict) {
			t.Fatal("retargeted a reviewed build", err)
		}
		if _, err := s.CreateBuild(t.Context(), dep.ID, dep.Kind, dep.SourceBytes, ""); err != nil {
			t.Fatal(err)
		}
		if err := pins.PinDeploymentRuntimeUpgradeTarget(t.Context(), dep.ID, target.ID, dep.SourceSHA256); !errors.Is(err, state.ErrConflict) {
			t.Fatal("changed an already queued build", err)
		}
		key := "apps/" + app.Slug + "/layer.ext4"
		if err := s.SetDeploymentRootfs(t.Context(), dep.ID, "/test/layer", key, 20); err != nil {
			t.Fatal(err)
		}
		if err := releases.BindDeploymentRuntimeRelease(t.Context(), dep.ID, key, other.ID); !errors.Is(err, state.ErrConflict) {
			t.Fatal("bound wrong runtime to update artifact", err)
		}
		if err := releases.BindDeploymentRuntimeRelease(t.Context(), dep.ID, key, target.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeployFailed, "build failed"); err != nil {
			t.Fatal(err)
		}
		retry, err := s.RetryDeploymentFromStage(t.Context(), dep.ID, state.StageSourceDownload)
		if err != nil {
			t.Fatal(err)
		}
		got, err := pins.DeploymentRuntimeUpgradeTarget(t.Context(), retry.ID)
		if err != nil || got.ID != target.ID || retry.SourceSHA256 != dep.SourceSHA256 || retry.SourceRoot != dep.SourceRoot || retry.RootfsKey != "" {
			t.Fatal("retry changed update input", got, retry, err)
		}
	})
}

func TestRuntimeUpgradeTargetRejectsUnsupportedOrUnrecordedBuilds(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		for _, tc := range []struct {
			name string
			edit func(*state.App, *state.Deployment)
		}{
			{"unknown source", func(_ *state.App, d *state.Deployment) { d.SourceSHA256 = "" }},
			{"empty source", func(_ *state.App, d *state.Deployment) { d.SourceBytes = 0 }},
			{"image", func(_ *state.App, d *state.Deployment) { d.Kind = state.DeploymentKindImage }},
			{"Dockerfile", func(_ *state.App, d *state.Deployment) { d.Kind = state.DeploymentKindDockerfile }},
			{"custom build", func(a *state.App, _ *state.Deployment) { a.Manifest.BuildDockerfile = "Dockerfile" }},
			{"container app", func(a *state.App, _ *state.Deployment) { a.Type = state.AppTypeApp; a.Runtime = "" }},
			{"different family", func(a *state.App, _ *state.Deployment) { a.Runtime = "node24" }},
			{"long source root", func(_ *state.App, d *state.Deployment) {
				d.SourceRoot = strings.Repeat("x", api.RuntimeUpgradeSourceFieldMaxBytes+1)
			}},
			{"long handler", func(_ *state.App, d *state.Deployment) {
				d.Handler = strings.Repeat("x", api.RuntimeUpgradeSourceFieldMaxBytes+1)
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, dep, target := runtimeUpgradeFixtureEdited(t, s, releases, tc.edit)
				pins := s.(state.RuntimeUpgradeTargetStore)
				err := pins.PinDeploymentRuntimeUpgradeTarget(t.Context(), dep.ID, target.ID, strings.Repeat("c", 64))
				if !errors.Is(err, state.ErrConflict) {
					t.Fatal("unsupported target accepted", err)
				}
			})
		}
	})
}

func TestRuntimeUpgradeTargetPinSerializesWithQueue(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		_, dep, target := runtimeUpgradeFixture(t, s, releases)
		pins := s.(state.RuntimeUpgradeTargetStore)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var pinErr, queueErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			pinErr = pins.PinDeploymentRuntimeUpgradeTarget(t.Context(), dep.ID, target.ID, dep.SourceSHA256)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, queueErr = s.CreateBuild(t.Context(), dep.ID, dep.Kind, dep.SourceBytes, "")
		}()
		close(start)
		wg.Wait()
		if queueErr != nil || (pinErr != nil && !errors.Is(pinErr, state.ErrConflict)) {
			t.Fatal(pinErr, queueErr)
		}
		got, readErr := pins.DeploymentRuntimeUpgradeTarget(t.Context(), dep.ID)
		if pinErr == nil && (readErr != nil || got.ID != target.ID) {
			t.Fatal("lost committed target", got, readErr)
		}
		if pinErr != nil && !errors.Is(readErr, state.ErrNotFound) {
			t.Fatal("pin crossed queue boundary", got, readErr)
		}
	})
}

func TestPgRuntimeUpgradeTargetSQLFences(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	_, dep, target := runtimeUpgradeFixture(t, s, s)
	if err := s.PinDeploymentRuntimeUpgradeTarget(t.Context(), dep.ID, target.ID, dep.SourceSHA256); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"UPDATE deployments SET source_root='other' WHERE id=$1",
		"UPDATE deployments SET source_sha256=repeat('d',64) WHERE id=$1",
		"UPDATE deployment_runtime_upgrade_targets SET source_bytes=100 WHERE deployment_id=$1",
		"DELETE FROM deployment_runtime_upgrade_targets WHERE deployment_id=$1",
	} {
		if _, err := pool.Exec(t.Context(), query, dep.ID); err == nil {
			t.Fatal("bypassed immutable input fence", query)
		}
	}
	if _, err := pool.Exec(t.Context(), "DELETE FROM deployments WHERE id=$1", dep.ID); err != nil {
		t.Fatal("target prevented deployment cleanup", err)
	}
	if _, err := s.DeploymentRuntimeUpgradeTarget(t.Context(), dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("orphaned target", err)
	}
}
