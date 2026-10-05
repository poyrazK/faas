package state_test

// adr: 598

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func runtimeUpgradeBaselineFixture(t *testing.T, s state.Store, releases state.RuntimeReleaseStore, edits ...func(*state.Deployment)) (state.App, state.Deployment, state.Deployment) {
	t.Helper()
	app, serving, current := runtimeUpgradeFixture(t, s, releases)
	key := "apps/" + app.Slug + "/serving.ext4"
	if err := s.SetDeploymentRootfs(t.Context(), serving.ID, "/serving", key, 20); err != nil {
		t.Fatal(err)
	}
	if err := releases.BindDeploymentRuntimeRelease(t.Context(), serving.ID, key, current.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(t.Context(), serving.ID); err != nil {
		t.Fatal(err)
	}
	serving, err := s.DeploymentByID(t.Context(), serving.ID)
	if err != nil {
		t.Fatal(err)
	}
	input := state.Deployment{AppID: app.ID, Kind: serving.Kind, SourceSHA256: serving.SourceSHA256,
		SourceBytes: serving.SourceBytes, SourceRoot: serving.SourceRoot, Handler: serving.Handler,
		TrafficPercent: 0, TrafficPercentExplicit: true}
	for _, edit := range edits {
		edit(&input)
	}
	candidate, err := s.CreateDeployment(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	target, err := releases.PublishRuntimeRelease(t.Context(), runtimeReleaseFixture("2"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.(state.RuntimeUpgradeTargetStore).PinDeploymentRuntimeUpgradeTarget(t.Context(), candidate.ID, target.ID, candidate.SourceSHA256); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAppEnv(t.Context(), app.AccountID, app.ID, "REGION", "region-one"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAppSecret(t.Context(), app.AccountID, app.ID, "TOKEN", []byte("sealed-fixture")); err != nil {
		t.Fatal(err)
	}
	return app, serving, candidate
}

func TestRuntimeUpgradeBaselineRequiresSameSourceAndHeldTraffic(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		for _, tc := range []struct {
			name string
			edit func(*state.Deployment)
		}{
			{"source changed", func(d *state.Deployment) {
				d.SourceSHA256 = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
			}},
			{"build root changed", func(d *state.Deployment) { d.SourceRoot = "another-function" }},
			{"handler changed", func(d *state.Deployment) { d.Handler = "other.handler" }},
			{"scope changed", func(d *state.Deployment) { d.Scope = "staging" }},
			{"implicit traffic", func(d *state.Deployment) { d.TrafficPercentExplicit = false }},
			{"positive traffic", func(d *state.Deployment) { d.TrafficPercent = 10 }},
			{"sidecars", func(d *state.Deployment) { d.Sidecars = []byte(`[{"name":"worker","image":"example/worker"}]`) }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, serving, candidate := runtimeUpgradeBaselineFixture(t, s, releases, tc.edit)
				if _, err := s.(state.RuntimeUpgradeBaselineStore).CaptureDeploymentRuntimeUpgradeBaseline(t.Context(), candidate.ID, serving.ID); !errors.Is(err, state.ErrConflict) {
					t.Fatal("unreviewed runtime update candidate admitted", err)
				}
			})
		}
	})
}

func TestRuntimeUpgradeBaselineImmutableAndRetainedOnRetry(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		_, serving, candidate := runtimeUpgradeBaselineFixture(t, s, releases)
		baselines := s.(state.RuntimeUpgradeBaselineStore)
		first, err := baselines.CaptureDeploymentRuntimeUpgradeBaseline(t.Context(), candidate.ID, serving.ID)
		if err != nil || first.ServingRootfsKey != serving.RootfsKey || first.CapturedAt.IsZero() || len(first.SecretFingerprint) != 64 {
			t.Fatal(first, err)
		}
		second, err := baselines.CaptureDeploymentRuntimeUpgradeBaseline(t.Context(), candidate.ID, serving.ID)
		if err != nil || first != second {
			t.Fatal("changed reviewed baseline", first, second, err)
		}
		if _, err := baselines.CaptureDeploymentRuntimeUpgradeBaseline(t.Context(), candidate.ID, uuid.NewString()); !errors.Is(err, state.ErrConflict) {
			t.Fatal("replaced serving baseline", err)
		}
		if _, err := s.CreateBuild(t.Context(), candidate.ID, candidate.Kind, candidate.SourceBytes, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := baselines.CaptureDeploymentRuntimeUpgradeBaseline(t.Context(), candidate.ID, serving.ID); !errors.Is(err, state.ErrConflict) {
			t.Fatal("captured after queue admission", err)
		}
		if err := s.SetDeploymentRootfs(t.Context(), candidate.ID, "/candidate", "apps/candidate/layer.ext4", 50); err != nil {
			t.Fatal(err)
		}
		if err := s.SetDeploymentSecretReloadSignal(t.Context(), candidate.ID, "SIGHUP"); err != nil {
			t.Fatal(err)
		}
		if err := baselines.ValidateDeploymentRuntimeUpgradeBaseline(t.Context(), candidate.ID); err != nil {
			t.Fatal("normal image outputs invalidated inputs", err)
		}
		if err := s.UpdateDeploymentStatus(t.Context(), candidate.ID, state.DeployFailed, "build failed"); err != nil {
			t.Fatal(err)
		}
		retry, err := s.RetryDeploymentFromStage(t.Context(), candidate.ID, state.StageSourceDownload)
		if err != nil {
			t.Fatal(err)
		}
		copied, err := baselines.DeploymentRuntimeUpgradeBaseline(t.Context(), retry.ID)
		first.DeploymentID = retry.ID
		if err != nil || copied != first {
			t.Fatal("retry recaptured newer inputs", copied, first, err)
		}
		if err := baselines.ValidateDeploymentRuntimeUpgradeBaseline(t.Context(), retry.ID); err != nil {
			t.Fatal("retry lost baseline", err)
		}
	})
}

func TestRuntimeUpgradeBaselineRejectsDrift(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		for _, tc := range []struct {
			name string
			edit func(*testing.T, state.Store, state.App, state.Deployment)
		}{
			{"env edit", func(t *testing.T, s state.Store, a state.App, _ state.Deployment) {
				if err := s.UpsertAppEnv(t.Context(), a.AccountID, a.ID, "REGION", "region-two"); err != nil {
					t.Fatal(err)
				}
			}},
			{"secret rewrite", func(t *testing.T, s state.Store, a state.App, _ state.Deployment) {
				if err := s.UpsertAppSecret(t.Context(), a.AccountID, a.ID, "TOKEN", []byte("sealed-fixture")); err != nil {
					t.Fatal(err)
				}
			}},
			{"secret revocation", func(t *testing.T, s state.Store, a state.App, _ state.Deployment) {
				if err := s.DeleteAppSecret(t.Context(), a.AccountID, a.ID, "TOKEN"); err != nil {
					t.Fatal(err)
				}
			}},
			{"app resources", func(t *testing.T, s state.Store, a state.App, _ state.Deployment) {
				ram := 256
				if _, err := s.UpdateApp(t.Context(), a.ID, state.UpdateAppParams{RAMMB: &ram}); err != nil {
					t.Fatal(err)
				}
			}},
			{"serving artifact", func(t *testing.T, s state.Store, _ state.App, d state.Deployment) {
				if err := s.SetDeploymentRootfs(t.Context(), d.ID, "/other", "apps/other/layer.ext4", 20); err != nil {
					t.Fatal(err)
				}
			}},
			{"serving replaced", func(t *testing.T, s state.Store, a state.App, _ state.Deployment) {
				d, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: a.ID, Kind: state.DeploymentKindTarball})
				if err != nil {
					t.Fatal(err)
				}
				if err := s.MarkDeploymentLive(t.Context(), d.ID); err != nil {
					t.Fatal(err)
				}
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				app, serving, candidate := runtimeUpgradeBaselineFixture(t, s, releases)
				baselines := s.(state.RuntimeUpgradeBaselineStore)
				if _, err := baselines.CaptureDeploymentRuntimeUpgradeBaseline(t.Context(), candidate.ID, serving.ID); err != nil {
					t.Fatal(err)
				}
				tc.edit(t, s, app, serving)
				if err := state.CheckDeploymentRuntimeUpgradeBaseline(t.Context(), s, candidate.ID); !errors.Is(err, state.ErrConflict) {
					t.Fatal("stale baseline admitted", err)
				}
				if _, err := baselines.CaptureDeploymentRuntimeUpgradeBaseline(t.Context(), candidate.ID, serving.ID); !errors.Is(err, state.ErrConflict) {
					t.Fatal("silently recaptured drift", err)
				}
			})
		}
	})
}

func TestRuntimeUpgradeBaselineCaptureSerializesWithQueue(t *testing.T) {
	runtimeReleaseStores(t, func(t *testing.T, s state.Store, releases state.RuntimeReleaseStore) {
		_, serving, candidate := runtimeUpgradeBaselineFixture(t, s, releases)
		baselines := s.(state.RuntimeUpgradeBaselineStore)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var captureErr, queueErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, captureErr = baselines.CaptureDeploymentRuntimeUpgradeBaseline(t.Context(), candidate.ID, serving.ID)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, queueErr = s.CreateBuild(t.Context(), candidate.ID, candidate.Kind, candidate.SourceBytes, "")
		}()
		close(start)
		wg.Wait()
		if queueErr != nil || (captureErr != nil && !errors.Is(captureErr, state.ErrConflict)) {
			t.Fatal(captureErr, queueErr)
		}
		_, readErr := baselines.DeploymentRuntimeUpgradeBaseline(t.Context(), candidate.ID)
		if captureErr == nil && readErr != nil || captureErr != nil && !errors.Is(readErr, state.ErrNotFound) {
			t.Fatal(captureErr, readErr)
		}
	})
}

func TestPgRuntimeUpgradeBaselineSQLIntegrity(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	_, serving, candidate := runtimeUpgradeBaselineFixture(t, s, s)
	_, err := s.CaptureDeploymentRuntimeUpgradeBaseline(t.Context(), candidate.ID, serving.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"UPDATE deployment_runtime_upgrade_baselines SET secret_fingerprint=repeat('a',64) WHERE deployment_id=$1",
		"DELETE FROM deployment_runtime_upgrade_baselines WHERE deployment_id=$1",
	} {
		if _, err := pool.Exec(t.Context(), query, candidate.ID); err == nil {
			t.Fatal("mutable baseline", query)
		}
	}
	if _, err := pool.Exec(t.Context(), "UPDATE deployments SET override_env='{}' WHERE id=$1", candidate.ID); err != nil {
		t.Fatal(err)
	}
	if err := state.CheckDeploymentRuntimeUpgradeBaseline(t.Context(), s, candidate.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("candidate override drift admitted", err)
	}
	if _, err := pool.Exec(t.Context(), "DELETE FROM deployments WHERE id=$1", serving.ID); err == nil {
		t.Fatal("deleted retained serving deployment")
	}
	if _, err := pool.Exec(t.Context(), "DELETE FROM deployments WHERE id=$1", candidate.ID); err != nil {
		t.Fatal("baseline prevented parent cleanup", err)
	}
	if _, err := s.DeploymentRuntimeUpgradeBaseline(t.Context(), candidate.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("orphan baseline", err)
	}
}
