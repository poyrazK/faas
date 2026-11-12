package release_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func rollbackWindowFixture(t *testing.T, s state.Store) (state.Account, state.Project, state.App, state.Deployment, state.Deployment) {
	t.Helper()
	a, err := s.CreateAccount(t.Context(), uuid.NewString()+"@rollback-window.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(t.Context(), state.Project{AccountID: a.ID, Slug: "rollback-window-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, ProjectID: p.ID, Slug: "app-" + uuid.NewString()[:8], WorkloadName: "api", RAMMB: 256, MaxConcurrency: 5, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	old := rollbackSafetyCreate(t, s, app, "fallback", true, "production")
	next, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:rollback-window-dark", TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetDeploymentRootfs(t.Context(), next.ID, "/test/"+next.ID, "test/"+next.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err = s.(state.ProjectPromotionDeploymentStore).MarkDeploymentLiveDark(t.Context(), next.ID); err != nil {
		t.Fatal(err)
	}
	return a, p, app, old, next
}

func prepareRollbackWindowMigration(t *testing.T, s state.Store, a state.Account, app state.App) state.AppTask {
	t.Helper()
	d, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:rollback-window-migration", ReleaseCommand: []string{"bin/migrate"}})
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
	claimed, err := tasks.ClaimNextAppTask(t.Context(), "rollback-window", now, time.Minute)
	if err != nil || claimed.ID != task.ID {
		t.Fatalf("claim %+v %v", claimed, err)
	}
	return claimed
}

func TestRollbackWindowFallbackRepublishDoesNotEraseMigration(t *testing.T) {
	for _, republish := range []bool{false, true} {
		name := "control_single_graph"
		if republish {
			name = "republished_same_members"
		}
		t.Run(name, func(t *testing.T) {
			rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
				a, p, app, old, next := rollbackWindowFixture(t, s)
				pub := s.(state.ProjectReleaseSetStore)
				members := []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: next.ID}}
				active, err := pub.PublishProjectReleaseSet(t.Context(), a.ID, p.ID, "production", 1800, members)
				if err != nil {
					t.Fatal(err)
				}
				startGraphRollbackMigration(t, s, a, app)
				if republish {
					active, err = pub.PublishProjectReleaseSet(t.Context(), a.ID, p.ID, "production", 1800, members)
					if err != nil {
						t.Fatal(err)
					}
				}
				err = s.(state.ProjectReleaseSetPromotionStore).DeactivateProjectReleaseSetIfActive(t.Context(), a.ID, p.ID, "production", active.ID, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: old.ID}})
				_, route, readErr := pub.ResolveProjectRelease(t.Context(), app.ID, "production", "")
				if readErr != nil {
					t.Fatal(readErr)
				}
				if route == "" {
					fallback, e := s.LiveDeploymentForScope(t.Context(), app.ID, "production")
					if e != nil {
						t.Fatal(e)
					}
					route = fallback.ID
				}
				t.Logf("republished=%v rollback_error=%v route_is_old=%v", republish, err, route == old.ID)
				if !errors.Is(err, state.ErrRollbackCrossesRelease) || route != next.ID {
					t.Errorf("unsafe fallback restoration after migration: error=%v restored_old=%v", err, route == old.ID)
				}
			})
		})
	}
}

func TestRollbackWindowReleaseStartUsesAdmissionOrder(t *testing.T) {
	for _, early := range []bool{false, true} {
		name := "control_fresh_start_stamp"
		if early {
			name = "scheduler_stamp_before_cutover"
		}
		t.Run(name, func(t *testing.T) {
			rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
				a, p, app, old, next := rollbackWindowFixture(t, s)
				pub := s.(state.ProjectReleaseSetStore)
				previous := []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: old.ID}}
				if _, err := pub.PublishProjectReleaseSet(t.Context(), a.ID, p.ID, "production", 1800, previous); err != nil {
					t.Fatal(err)
				}
				claimed := prepareRollbackWindowMigration(t, s, a, app)
				captured := time.Now().UTC()
				active, err := pub.PublishProjectReleaseSet(t.Context(), a.ID, p.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: next.ID}})
				if err != nil {
					t.Fatal(err)
				}
				start := time.Now().UTC()
				if early {
					start = captured
				}
				running, err := s.(state.AppTaskStore).MarkAppTaskRunning(t.Context(), claimed.ID, *claimed.LeaseToken, start)
				if err != nil {
					t.Fatal(err)
				}
				_, err = s.(state.ProjectReleaseSetPromotionStore).PublishProjectReleaseSetIfActive(t.Context(), a.ID, p.ID, "production", active.ID, nil, 1800, previous)
				_, route, readErr := pub.ResolveProjectRelease(t.Context(), app.ID, "production", "")
				if readErr != nil {
					t.Fatal(readErr)
				}
				t.Logf("early_stamp=%v recorded_start=%s graph_created=%s task_running=%v rollback_error=%v restored_old=%v", early, running.StartedAt, active.CreatedAt, running.Status == state.AppTaskRunning, err, route == old.ID)
				if !errors.Is(err, state.ErrRollbackCrossesRelease) || route != next.ID {
					t.Errorf("release admitted after cutover was omitted: error=%v restored_old=%v", err, route == old.ID)
				}
			})
		})
	}
}

func TestRollbackWindowCheckedRollbackPreservesRetainedPin(t *testing.T) {
	rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
		a, app, _, _ := rollbackSafetyFixture(t, s)
		manifest := app.Manifest
		manifest.RevisionPinTTLSeconds = 3600
		var err error
		app, err = s.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest})
		if err != nil {
			t.Fatal(err)
		}
		target := rollbackSafetyCreate(t, s, app, "retained-target", true)
		current := rollbackSafetyCreate(t, s, app, "current", true)
		pins := s.(state.RevisionPinStore)
		if _, err = pins.ResolveRevisionPin(t.Context(), app.ID, "default", target.ID); err != nil {
			t.Fatalf("pin control: %v", err)
		}
		checked := s.(state.CheckedRollbackStore)
		op, err := checked.CreateCheckedRollback(t.Context(), a.ID, app.ID, target.ID, current.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		_, pinDuring := pins.ResolveRevisionPin(t.Context(), app.ID, "default", target.ID)
		t.Logf("pin during preparation: %v", pinDuring)
		if op.Status != "ready" {
			t.Fatalf("retained live target lost its readiness: %+v", op)
		}
		op, err = checked.GetCheckedRollback(t.Context(), a.ID, app.ID, op.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pins.ResolveRevisionPin(t.Context(), app.ID, "default", target.ID); err != nil {
			t.Fatalf("ready pin control: %v", err)
		}
		_ = rollbackSafetyCreate(t, s, app, "replacement", true)
		if _, err = pins.ResolveRevisionPin(t.Context(), app.ID, "default", target.ID); err != nil {
			t.Fatalf("replacement pin control: %v", err)
		}
		if err = checked.UpdateCheckedRollback(t.Context(), op, "failed", "rollback_deployment_changed", nil); err != nil {
			t.Fatal(err)
		}
		targetNow, err := s.DeploymentByID(t.Context(), target.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, pinAfter := pins.ResolveRevisionPin(t.Context(), app.ID, "default", target.ID)
		t.Logf("pin after failed receipt: %v target_status=%s", pinAfter, targetNow.Status)
		if pinDuring != nil || pinAfter != nil {
			t.Errorf("rollback disrupted retained pin: preparation=%v after_failure=%v", pinDuring, pinAfter)
		}
		nextCurrent, err := s.LiveDeploymentForScope(t.Context(), app.ID, "default")
		if err != nil {
			t.Fatal(err)
		}
		retry, err := checked.CreateCheckedRollback(t.Context(), a.ID, app.ID, target.ID, nextCurrent.ID, "retry after failure")
		if err != nil || retry.Status != "ready" {
			t.Fatalf("retained retry lost readiness: %+v %v", retry, err)
		}
		if _, err = pins.ResolveRevisionPin(t.Context(), app.ID, "default", target.ID); err != nil {
			t.Fatalf("retry disrupted existing pin: %v", err)
		}
	})
}

func TestRollbackWindowNewRequestPreservesPreviousSuccess(t *testing.T) {
	for _, swept := range []bool{true, false} {
		name := "control_receipt_reconciled"
		if !swept {
			name = "second_request_before_reconcile"
		}
		t.Run(name, func(t *testing.T) {
			rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
				a, app, target, current := rollbackSafetyFixture(t, s)
				if _, err := s.PrepareDeploymentRollback(t.Context(), app.ID, target.ID); err != nil {
					t.Fatal(err)
				}
				first := rollbackSafetyRecord(t, s, a, app, target, current)
				if err := s.MarkDeploymentLive(state.WithPreparedRollbackPromotion(t.Context()), target.ID); err != nil {
					t.Fatal(err)
				}
				served, err := s.DeploymentByID(t.Context(), target.ID)
				if err != nil {
					t.Fatal(err)
				}
				completed, ok := state.RollbackCompletedAt(first.RequestedAt, served)
				if !ok {
					t.Fatalf("first rollback did not complete: %+v", served)
				}
				if swept {
					if err = rollbackSafetySweeper(s).Sweep(t.Context(), time.Now().UTC()); err != nil {
						t.Fatal(err)
					}
				}
				next := rollbackSafetyCreate(t, s, app, "next-release", true)
				if _, err = s.PrepareDeploymentRollback(t.Context(), app.ID, target.ID); err != nil {
					t.Fatal(err)
				}
				_ = rollbackSafetyRecord(t, s, a, app, target, next)
				actual, err := s.(state.RollbackOutcomeStore).RollbackRecordByID(t.Context(), a.ID, app.ID, first.ID)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("first_completed_at=%s reconciled_before_retry=%v first_receipt_status=%s code=%s", completed, swept, actual.Status, actual.ErrorCode)
				if actual.Status != state.RollbackStatusComplete || actual.FinishedAt == nil || !actual.FinishedAt.Equal(completed) || actual.AuditID <= 0 {
					t.Errorf("successful rollback was overwritten by next request: %+v", actual)
				}
			})
		})
	}
}

func TestRollbackWindowCheckedFallbackRepublishDoesNotEraseMigration(t *testing.T) {
	for _, republish := range []bool{false, true} {
		name := "control_single_graph"
		if republish {
			name = "republished_same_members"
		}
		t.Run(name, func(t *testing.T) {
			rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
				a, p, app, old, next := rollbackWindowFixture(t, s)
				pub := s.(state.ProjectReleaseSetStore)
				members := []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: next.ID}}
				active, err := pub.PublishProjectReleaseSet(t.Context(), a.ID, p.ID, "production", 1800, members)
				if err != nil {
					t.Fatal(err)
				}
				startGraphRollbackMigration(t, s, a, app)
				if republish {
					active, err = pub.PublishProjectReleaseSet(t.Context(), a.ID, p.ID, "production", 1800, members)
					if err != nil {
						t.Fatal(err)
					}
				}
				zero := int64(0)
				policy, e := s.(state.BindingReleasePolicyStore).SetBindingReleasePolicy(t.Context(), a.ID, app.ID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero})
				if e != nil {
					t.Fatal(e)
				}
				revision, e := s.(state.BindingPromotionStore).ReadBindingPromotionRevision(t.Context(), a.ID, app.ID)
				if e != nil {
					t.Fatal(e)
				}
				age, e := time.ParseDuration(policy.MaxVerificationAge)
				if e != nil {
					t.Fatal(e)
				}
				_, err = s.(state.CheckedProjectReleaseStore).DeactivateProjectReleaseWithBindings(t.Context(), a.ID, p.ID, "production", active.ID, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: old.ID}}, []state.BindingPromotionFence{{AccountID: a.ID, AppID: app.ID, DeploymentID: old.ID, Scope: "production", Revision: revision, PolicyRevision: policy.Revision, MaxVerificationAge: age, ValidUntil: time.Now().Add(time.Minute)}})
				_, route, readErr := pub.ResolveProjectRelease(t.Context(), app.ID, "production", "")
				if readErr != nil {
					t.Fatal(readErr)
				}
				if route == "" {
					fallback, e := s.LiveDeploymentForScope(t.Context(), app.ID, "production")
					if e != nil {
						t.Fatal(e)
					}
					route = fallback.ID
				}
				t.Logf("republished=%v rollback_error=%v route_is_old=%v", republish, err, route == old.ID)
				if !errors.Is(err, state.ErrRollbackCrossesRelease) || route != next.ID {
					t.Errorf("unsafe fallback restoration after migration: error=%v restored_old=%v", err, route == old.ID)
				}
			})
		})
	}
}

func TestRollbackWindowRetainedServiceRechecksCapacity(t *testing.T) {
	rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
		a, app, _, _ := rollbackSafetyFixture(t, s)
		manifest := app.Manifest
		manifest.RevisionPinTTLSeconds = 3600
		var err error
		app, err = s.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest})
		if err != nil {
			t.Fatal(err)
		}
		target := rollbackSafetyCreate(t, s, app, "retained-service", true)
		current := rollbackSafetyCreate(t, s, app, "current-service", true)
		manifest.ExecutionMode = api.ExecutionModeService
		manifest.ServiceReplicas = &state.ServiceReplicas{Min: 1, Max: 2, Desired: 1}
		app, err = s.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest})
		if err != nil {
			t.Fatal(err)
		}
		checked := s.(state.CheckedRollbackStore)
		op, err := checked.CreateCheckedRollback(t.Context(), a.ID, app.ID, target.ID, current.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if op.Status != "ready" {
			t.Fatalf("live retained service lost readiness: %+v", op)
		}
		_, err = checked.CommitCheckedRollback(rollbackServiceFence(t, s, a, app, target), op)
		if !errors.Is(err, state.ErrServiceRolloutNotReady) {
			t.Fatalf("reused artifact bypassed fresh capacity: %v", err)
		}
		pins := s.(state.RevisionPinStore)
		before, err := pins.ResolveRevisionPin(t.Context(), app.ID, "default", target.ID)
		if err != nil || before.TrafficPercent != 0 {
			t.Fatalf("blocked operation disrupted alias: %+v %v", before, err)
		}
		node := state.DefaultLocalNodeName
		if pg, ok := s.(*state.PgStore); ok {
			n, e := pg.ComputeNodeByName(t.Context(), node)
			if e != nil {
				t.Fatal(e)
			}
			node = n.ID
		}
		if _, err = s.CreateInstanceWithMode(t.Context(), app.ID, target.ID, "running", 256, node, uuid.NewString(), "service"); err != nil {
			t.Fatal(err)
		}
		routed, err := checked.CommitCheckedRollback(rollbackServiceFence(t, s, a, app, target), op)
		if err != nil || routed.Status != "routing" {
			t.Fatalf("qualified retained service did not route: %+v %v", routed, err)
		}
	})
}

func TestRollbackWindowNeverServedTargetStillPrepares(t *testing.T) {
	rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
		a, app, _, current := rollbackSafetyFixture(t, s)
		target, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "default", Kind: state.DeploymentKindImage, ImageDigest: "sha256:never-served", TrafficPercentExplicit: true})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.SetDeploymentRootfs(t.Context(), target.ID, "/test/"+target.ID, "test/"+target.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if err = s.MarkDeploymentLive(t.Context(), target.ID); err != nil {
			t.Fatal(err)
		}
		op, err := s.(state.CheckedRollbackStore).CreateCheckedRollback(t.Context(), a.ID, app.ID, target.ID, current.ID, "")
		if err != nil || op.Status != "preparing" {
			t.Fatalf("never-served target skipped readiness: %+v %v", op, err)
		}
	})
}

func TestRollbackWindowGraphEndUsesPublicationClock(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	a, p, app, old, _ := rollbackWindowFixture(t, s)
	graph, err := s.PublishProjectReleaseSet(t.Context(), a.ID, p.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: old.ID}})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	var opened time.Time
	if err = tx.QueryRow(t.Context(), "SELECT now()").Scan(&opened); err != nil {
		t.Fatal(err)
	}
	task := prepareRollbackWindowMigration(t, s, a, app)
	running, err := s.MarkAppTaskRunning(t.Context(), task.ID, *task.LeaseToken, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	q := sqlc.New()
	u := func(id string) pgtype.UUID { return pgtype.UUID{Bytes: uuid.MustParse(id), Valid: true} }
	if _, err = q.LockCheckedRollbackApp(t.Context(), tx, sqlc.LockCheckedRollbackAppParams{AppID: u(app.ID), AccountID: u(a.ID)}); err != nil {
		t.Fatal(err)
	}
	if err = q.DeactivateProjectReleaseSets(t.Context(), tx, sqlc.DeactivateProjectReleaseSetsParams{ProjectID: u(p.ID), Environment: "production", ReleaseID: u(graph.ID)}); err != nil {
		t.Fatal(err)
	}
	ended, err := q.LastGraphMemberServingEnd(t.Context(), tx, sqlc.LastGraphMemberServingEndParams{ProjectID: u(p.ID), Scope: "production", AppID: u(app.ID), DeploymentID: u(old.ID)})
	if err != nil || !ended.Valid || ended.Time.Before(*running.StartedAt) {
		t.Fatalf("transaction opened at %s erased served migration at %s: end=%+v err=%v", opened, running.StartedAt, ended, err)
	}
}

func TestRollbackWindowLegacyGraphCannotInventHistory(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	a, p, app, old, next := rollbackWindowFixture(t, s)
	members := []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: next.ID}}
	active, err := s.PublishProjectReleaseSet(t.Context(), a.ID, p.ID, "production", 1800, members)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a head published by a writer predating the additive column.
	if _, err = pool.Exec(t.Context(), "UPDATE project_release_sets SET fallback_hidden_at=NULL WHERE id=$1", active.ID); err != nil {
		t.Fatal(err)
	}
	startGraphRollbackMigration(t, s, a, app)
	active, err = s.PublishProjectReleaseSet(t.Context(), a.ID, p.ID, "production", 1800, members)
	if err != nil {
		t.Fatal(err)
	}
	err = s.DeactivateProjectReleaseSetIfActive(t.Context(), a.ID, p.ID, "production", active.ID, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: old.ID}})
	if !errors.Is(err, state.ErrRollbackCrossesRelease) {
		t.Fatalf("legacy history acquired implicit migration review: %v", err)
	}
}

func TestRollbackWindowNewGraphIntervalAfterFallbackServed(t *testing.T) {
	rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
		a, p, app, old, next := rollbackWindowFixture(t, s)
		pub := s.(state.ProjectReleaseSetStore)
		mutate := s.(state.ProjectReleaseSetPromotionStore)
		fallback := []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: old.ID}}
		active, err := pub.PublishProjectReleaseSet(t.Context(), a.ID, p.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: next.ID}})
		if err != nil {
			t.Fatal(err)
		}
		if err = mutate.DeactivateProjectReleaseSetIfActive(t.Context(), a.ID, p.ID, "production", active.ID, fallback); err != nil {
			t.Fatal(err)
		}
		startGraphRollbackMigration(t, s, a, app)
		candidate, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:new-window", TrafficPercentExplicit: true})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.SetDeploymentRootfs(t.Context(), candidate.ID, "/test/"+candidate.ID, "test/"+candidate.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if err = s.(state.ProjectPromotionDeploymentStore).MarkDeploymentLiveDark(t.Context(), candidate.ID); err != nil {
			t.Fatal(err)
		}
		active, err = pub.PublishProjectReleaseSet(t.Context(), a.ID, p.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: candidate.ID}})
		if err != nil {
			t.Fatal(err)
		}
		if err = mutate.DeactivateProjectReleaseSetIfActive(t.Context(), a.ID, p.ID, "production", active.ID, fallback); err != nil {
			t.Fatalf("old fallback served this migration before the new interval: %v", err)
		}
	})
}
