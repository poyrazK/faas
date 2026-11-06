package release_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func startGraphRollbackMigration(t *testing.T, s state.Store, a state.Account, app state.App) {
	t.Helper()
	d, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:graph-migration", Scope: "production", Status: state.DeployPending, ReleaseCommand: []string{"bin/migrate"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []state.DeploymentStatus{state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting} {
		if err = s.UpdateDeploymentStatus(t.Context(), d.ID, status, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.SetDeploymentRootfs(t.Context(), d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
		t.Fatal(err)
	}
	tasks := s.(state.AppTaskStore)
	now := time.Now().UTC()
	task, err := tasks.CreateAppTask(t.Context(), state.CreateAppTaskParams{AccountID: a.ID, AppID: app.ID, DeploymentID: d.ID, Kind: state.AppTaskKindRelease, Command: []string{"bin/migrate"}, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := tasks.ClaimNextAppTask(t.Context(), "graph-rollback-migration", now, time.Minute)
	if err != nil || claimed.ID != task.ID {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	if _, err = tasks.MarkAppTaskRunning(t.Context(), task.ID, *claimed.LeaseToken, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func TestGraphRollbackUsesActualServingWindow(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		name := "retained_graph"
		if fallback {
			name = "weighted_fallback"
		}
		t.Run(name, func(t *testing.T) {
			for _, late := range []bool{false, true} {
				name := "migration_while_old_graph_served"
				if late {
					name = "migration_after_graph_switch"
				}
				t.Run(name, func(t *testing.T) {
					rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
						a, err := s.CreateAccount(t.Context(), uuid.NewString()+"@graph-window.test", api.PlanPro)
						if err != nil {
							t.Fatal(err)
						}
						project, err := s.CreateProject(t.Context(), state.Project{AccountID: a.ID, Slug: "graph-" + uuid.NewString()[:8]})
						if err != nil {
							t.Fatal(err)
						}
						app, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, ProjectID: project.ID, Slug: "app-" + uuid.NewString()[:8], WorkloadName: "api", RAMMB: 256, MaxConcurrency: 5, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
						if err != nil {
							t.Fatal(err)
						}
						old := rollbackSafetyCreate(t, s, app, "fallback", true, "production")
						previous := []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: old.ID}}
						publisher := s.(state.ProjectReleaseSetStore)
						if !fallback {
							if _, err = publisher.PublishProjectReleaseSet(t.Context(), a.ID, project.ID, "production", 1800, previous); err != nil {
								t.Fatal(err)
							}
						}
						startGraphRollbackMigration(t, s, a, app)
						next, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:dark-graph", Status: state.DeployPending, TrafficPercent: 0, TrafficPercentExplicit: true})
						if err != nil {
							t.Fatal(err)
						}
						if err = s.SetDeploymentRootfs(t.Context(), next.ID, "/test/"+next.ID, "test/"+next.ID, 4096); err != nil {
							t.Fatal(err)
						}
						if err = s.(state.ProjectPromotionDeploymentStore).MarkDeploymentLiveDark(t.Context(), next.ID); err != nil {
							t.Fatal(err)
						}
						active, err := publisher.PublishProjectReleaseSet(t.Context(), a.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: next.ID}})
						if err != nil {
							t.Fatal(err)
						}
						physical, err := s.DeploymentByID(t.Context(), old.ID)
						if err != nil || physical.TrafficPercent != 100 {
							t.Fatalf("weighted fallback control: %+v %v", physical, err)
						}
						if late {
							startGraphRollbackMigration(t, s, a, app)
						}
						guarded := s.(state.ProjectReleaseSetPromotionStore)
						if fallback {
							err = guarded.DeactivateProjectReleaseSetIfActive(t.Context(), a.ID, project.ID, "production", active.ID, previous)
						} else {
							_, err = guarded.PublishProjectReleaseSetIfActive(t.Context(), a.ID, project.ID, "production", active.ID, nil, 1800, previous)
						}
						if late && !errors.Is(err, state.ErrRollbackCrossesRelease) {
							t.Fatalf("rollback crossed a late migration: %v", err)
						}
						if !late && err != nil {
							t.Fatalf("migration already served by the old graph blocked rollback: %v", err)
						}
						if late {
							_, route, e := publisher.ResolveProjectRelease(t.Context(), app.ID, "production", "")
							if e != nil || route != next.ID {
								t.Fatalf("blocked rollback changed graph: %s %v", route, e)
							}
						}
					})
				})
			}
		})
	}
}

func TestReleaseStartInvalidatesOlderRollbackSnapshot(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	a, app, _, _ := rollbackSafetyFixture(t, s)
	tx, err := pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	var count int
	if err = tx.QueryRow(t.Context(), "SELECT count(*) FROM app_tasks").Scan(&count); err != nil {
		t.Fatal(err)
	}
	startRollbackServiceMigration(t, s, a, app)
	err = sqlc.New().GuardWorkflowAdmissionSnapshot(t.Context(), tx, app.ID)
	var conflict *pgconn.PgError
	if !errors.As(err, &conflict) || conflict.Code != "40001" {
		t.Fatalf("stale repeatable-read history was accepted: %v", err)
	}
	if err = tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	fresh, err := pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fresh.Rollback(t.Context()) }()
	if err = sqlc.New().GuardWorkflowAdmissionSnapshot(t.Context(), fresh, app.ID); err != nil {
		t.Fatalf("fresh snapshot refused: %v", err)
	}
}

func TestCheckedServiceCompletionWinsStaleFailure(t *testing.T) {
	rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
		a, app, target, op := completedCheckedServiceRollback(t, s)
		d, err := s.DeploymentByID(t.Context(), target.ID)
		if err != nil {
			t.Fatal(err)
		}
		checked := s.(state.CheckedRollbackStore)
		if err = checked.UpdateCheckedRollback(t.Context(), op, "failed", "rollback_readiness_failed", nil); err != nil {
			t.Fatal(err)
		}
		result, err := checked.GetCheckedRollback(t.Context(), a.ID, app.ID, op.ID)
		if err != nil || result.Status != "complete" || result.Code != "" || result.CompletedAt == nil || !result.CompletedAt.Equal(*d.ServiceRolloutHandoff.CompletedAt) {
			t.Fatalf("stale verdict replaced authenticated completion: %+v %v", result, err)
		}
	})
}
