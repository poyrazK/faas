//go:build !no_pg

// adr: 685
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type dependencyGateFixture struct {
	store                           state.Store
	ctx                             context.Context
	reader                          state.DeploymentDependencyGateStore
	pool                            *pgxpool.Pool
	apiApp, web                     state.App
	dependency, candidate, previous state.Deployment
}

func newDependencyGateFixture(t *testing.T, backend string, initialStage ...state.DeploymentStatus) dependencyGateFixture {
	t.Helper()
	var store state.Store = state.NewMemStore()
	var pool *pgxpool.Pool
	ctx := t.Context()
	if backend == "postgres" {
		store, pool, ctx = pgStoreWithPool(t)
	}
	account, err := store.CreateAccount(ctx, "dependency-gate@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "dependency-gate"})
	if err != nil {
		t.Fatal(err)
	}
	createApp := func(name string) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, WorkloadName: name, Slug: name, Type: state.AppTypeApp})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	apiApp, web := createApp("backend"), createApp("web")
	createDeployment := func(app state.App) state.Deployment {
		t.Helper()
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "docker.io/library/nginx:1.27"})
		if err != nil {
			t.Fatal(err)
		}
		return dep
	}
	oldAPI := createDeployment(apiApp)
	if err := store.MarkDeploymentLive(ctx, oldAPI.ID); err != nil {
		t.Fatal(err)
	}
	previous := createDeployment(web)
	if err := store.MarkDeploymentLive(ctx, previous.ID); err != nil {
		t.Fatal(err)
	}
	manifest := web.Manifest
	manifest.ServiceBindings = []api.AppServiceBinding{{Binding: api.ServiceBindingEnvKey("backend"), Service: "backend"}}
	manifest.ProjectDependencyConditions = map[string]string{"backend": api.ComposeDependencyHealthy}
	web, err = store.UpdateApp(ctx, web.ID, state.UpdateAppParams{Manifest: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	dependency, candidate := createDeployment(apiApp), createDeployment(web)
	stage := state.DeploySnapshotting
	if len(initialStage) > 0 {
		stage = initialStage[0]
	}
	if err := store.UpdateDeploymentStatus(ctx, candidate.ID, stage, ""); err != nil {
		t.Fatal(err)
	}
	return dependencyGateFixture{
		store: store, ctx: ctx, reader: store.(state.DeploymentDependencyGateStore), pool: pool,
		apiApp: apiApp, web: web, dependency: dependency, candidate: candidate, previous: previous,
	}
}

func assertDependencyGateError(t *testing.T, err error, code string) {
	t.Helper()
	var blocker *state.DependencyGateError
	if !errors.As(err, &blocker) || blocker.Code != code {
		t.Fatalf("gate error = %v, want %s", err, code)
	}
}

func assertPreviousDependencyTraffic(t *testing.T, f dependencyGateFixture) {
	t.Helper()
	live, err := f.store.LiveDeploymentForScope(f.ctx, f.web.ID, state.DefaultEnvScope)
	if err != nil || live.ID != f.previous.ID || live.TrafficPercent != 100 {
		t.Fatalf("serving traffic changed: %+v, %v", live, err)
	}
}

func TestProjectDependencyGatePromotionAndTraffic(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newDependencyGateFixture(t, backend)
			gate, err := f.reader.CheckDeploymentDependencies(f.ctx, f.candidate.ID, time.Now())
			assertDependencyGateError(t, err, state.CodeDependencyNotReady)
			if gate.Dependencies[0].DeploymentID != f.dependency.ID || gate.DeadlineAt == nil {
				t.Fatalf("wrong captured dependency: %+v", gate)
			}
			assertDependencyGateError(t, f.store.MarkDeploymentLiveIfLatest(f.ctx, f.candidate.ID), state.CodeDependencyNotReady)
			if err := f.store.UpdateDeploymentStatus(f.ctx, f.candidate.ID, state.DeployLive, ""); err == nil {
				t.Fatal("generic status write bypassed gate")
			}
			assertPreviousDependencyTraffic(t, f)
			// Mutating a returned observation cannot change the admission pins.
			gate.Dependencies[0].DeploymentID = f.previous.ID
			*gate.DeadlineAt = time.Now().Add(-time.Hour)
			if err := f.store.MarkDeploymentLiveIfLatest(f.ctx, f.dependency.ID); err != nil {
				t.Fatal(err)
			}
			gate, err = f.reader.CheckDeploymentDependencies(f.ctx, f.candidate.ID, time.Now())
			if err != nil || gate.Status != "ready" || gate.Dependencies[0].DeploymentID != f.dependency.ID {
				t.Fatalf("ready gate = %+v, %v", gate, err)
			}
			if err := f.store.MarkDeploymentLiveIfLatest(f.ctx, f.candidate.ID); err != nil {
				t.Fatal(err)
			}
			live, err := f.store.LiveDeploymentForScope(f.ctx, f.web.ID, state.DefaultEnvScope)
			if err != nil || live.ID != f.candidate.ID || live.TrafficPercent != 100 {
				t.Fatalf("candidate was not promoted: %+v, %v", live, err)
			}
		})
	}
}

func TestProjectDependencyGatePinsSurviveEditsAndRetries(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newDependencyGateFixture(t, backend, state.DeployImaging)
			manifest := f.web.Manifest
			manifest.ProjectDependencyConditions = nil
			if _, err := f.store.UpdateApp(f.ctx, f.web.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
				t.Fatal(err)
			}
			if err := f.store.SetDeploymentRuntimeProfile(f.ctx, f.candidate.ID, []byte(`{"port":80}`)); err != nil {
				t.Fatalf("runtime profile update failed: %v", err)
			}
			_, err := f.reader.CheckDeploymentDependencies(f.ctx, f.candidate.ID, time.Now())
			assertDependencyGateError(t, err, state.CodeDependencyNotReady)
			if _, err := f.store.SetDeploymentFailed(f.ctx, f.candidate.ID, state.CodeDependencyTimeout, "fixture timeout"); err != nil {
				t.Fatal(err)
			}
			retry, err := f.store.RetryDeploymentFromStage(f.ctx, f.candidate.ID, state.StageImageBuild)
			if err != nil {
				t.Fatal(err)
			}
			gate, err := f.reader.CheckDeploymentDependencies(f.ctx, retry.ID, time.Now())
			assertDependencyGateError(t, err, state.CodeDependencyNotReady)
			if gate.Dependencies[0].DeploymentID != f.dependency.ID {
				t.Fatal("retry followed changed app intent")
			}
			// A later healthy release cannot certify the pinned pending row.
			newer, err := f.store.CreateDeployment(f.ctx, state.Deployment{AppID: f.apiApp.ID, Kind: state.DeploymentKindImage, ImageDigest: "docker.io/library/nginx:1.28"})
			if err != nil {
				t.Fatal(err)
			}
			if err := f.store.MarkDeploymentLiveIfLatest(f.ctx, newer.ID); err != nil {
				t.Fatal(err)
			}
			_, err = f.reader.CheckDeploymentDependencies(f.ctx, retry.ID, time.Now())
			assertDependencyGateError(t, err, state.CodeDependencyFailed)
			assertPreviousDependencyTraffic(t, f)
		})
	}
}

func TestProjectDependencyGateTimeoutAndPersistedProgress(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newDependencyGateFixture(t, backend)
			now := time.Now().UTC()
			first, err := f.reader.CheckDeploymentDependencies(f.ctx, f.candidate.ID, now)
			assertDependencyGateError(t, err, state.CodeDependencyNotReady)
			second, err := f.reader.CheckDeploymentDependencies(f.ctx, f.candidate.ID, now.Add(time.Minute))
			assertDependencyGateError(t, err, state.CodeDependencyNotReady)
			if !first.DeadlineAt.Equal(*second.DeadlineAt) {
				t.Fatal("redelivery reset the deadline")
			}
			_, err = f.reader.CheckDeploymentDependencies(f.ctx, f.candidate.ID, now.Add(api.ProjectDependencyGateTimeout))
			assertDependencyGateError(t, err, state.CodeDependencyTimeout)
			dep, err := f.store.DeploymentByID(f.ctx, f.candidate.ID)
			if err != nil {
				t.Fatal(err)
			}
			var stages state.StageState
			if err := json.Unmarshal(dep.StageState, &stages); err != nil {
				t.Fatal(err)
			}
			if stages.DependencyGate == nil || stages.DependencyGate.Status != "failed" || stages.DependencyGate.Blocker == "" {
				t.Fatalf("missing progress: %s", dep.StageState)
			}
			if err := f.store.MarkDeploymentLive(f.ctx, f.dependency.ID); err != nil {
				t.Fatal(err)
			}
			if err := f.store.MarkDeploymentLiveIfLatest(f.ctx, f.candidate.ID); err == nil {
				t.Fatal("expired candidate became live")
			}
			assertPreviousDependencyTraffic(t, f)
		})
	}
}

func TestProjectDependencyGateScopeIsolation(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newDependencyGateFixture(t, backend)
			_, err := f.store.CreateDeployment(f.ctx, state.Deployment{AppID: f.web.ID, Kind: state.DeploymentKindImage, Scope: "staging", ImageDigest: "docker.io/library/nginx:1.27"})
			if err == nil {
				t.Fatal("production dependency satisfied a staging admission")
			}
			stageAPI, err := f.store.CreateDeployment(f.ctx, state.Deployment{AppID: f.apiApp.ID, Kind: state.DeploymentKindImage, Scope: "staging", ImageDigest: "docker.io/library/nginx:1.27"})
			if err != nil {
				t.Fatal(err)
			}
			stageWeb, err := f.store.CreateDeployment(f.ctx, state.Deployment{AppID: f.web.ID, Kind: state.DeploymentKindImage, Scope: "staging", ImageDigest: "docker.io/library/nginx:1.27"})
			if err != nil {
				t.Fatal(err)
			}
			gate, err := f.reader.CheckDeploymentDependencies(f.ctx, stageWeb.ID, time.Now())
			assertDependencyGateError(t, err, state.CodeDependencyNotReady)
			if gate.Dependencies[0].DeploymentID != stageAPI.ID {
				t.Fatal("wrong environment pin")
			}
			assertPreviousDependencyTraffic(t, f)
		})
	}
}

func TestProjectDependencyGateWaitsForDependencyRetirementCommit(t *testing.T) {
	f := newDependencyGateFixture(t, "postgres")
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	if err := f.store.MarkDeploymentLiveIfLatest(ctx, f.dependency.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reader.CheckDeploymentDependencies(ctx, f.candidate.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, "UPDATE deployments SET status='failed', traffic_percent=0 WHERE id=$1", f.dependency.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- f.store.MarkDeploymentLiveIfLatest(ctx, f.candidate.ID) }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := f.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND (query LIKE '%ReadDependencyGateTarget%' OR query LIKE '%LockRoutePolicyAccount%'))").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("promotion bypassed uncommitted dependency retirement: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		assertDependencyGateError(t, err, state.CodeDependencyFailed)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	assertPreviousDependencyTraffic(t, f)
}

func TestProjectDependencyGateRequiresServingHistoryForRecoveryExemption(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, status := range []state.DeploymentStatus{state.DeployFailed, state.DeploySuperseded} {
			t.Run(backend+"/"+string(status), func(t *testing.T) {
				f := newDependencyGateFixture(t, backend)
				if err := f.store.UpdateDeploymentStatus(f.ctx, f.candidate.ID, status, "fixture terminal candidate"); err != nil {
					t.Fatal(err)
				}
				assertDependencyGateError(t, f.store.MarkDeploymentLive(f.ctx, f.candidate.ID), state.CodeDependencyNotReady)
				if err := f.store.UpdateDeploymentStatus(f.ctx, f.candidate.ID, state.DeployLive, ""); err == nil {
					t.Fatal("terminal candidate bypassed its initial dependency gate")
				}
				assertPreviousDependencyTraffic(t, f)
				if err := f.store.MarkDeploymentLive(f.ctx, f.dependency.ID); err != nil {
					t.Fatal(err)
				}
				if err := f.store.MarkDeploymentLive(f.ctx, f.candidate.ID); err != nil {
					t.Fatal(err)
				}
				// Once it actually served, explicit historical restoration retains
				// the existing recovery contract even after its dependency fails.
				if err := f.store.UpdateDeploymentStatus(f.ctx, f.candidate.ID, state.DeploySuperseded, ""); err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.SetDeploymentFailed(f.ctx, f.dependency.ID, "fixture", "dependency failed later"); err != nil {
					t.Fatal(err)
				}
				if err := f.store.MarkDeploymentLive(f.ctx, f.candidate.ID); err != nil {
					t.Fatalf("historical recovery lost its exemption: %v", err)
				}
			})
		}
	}
}
