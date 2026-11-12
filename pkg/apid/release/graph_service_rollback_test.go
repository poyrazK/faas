package release_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/promotion"
	"github.com/onebox-faas/faas/pkg/apid/release"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
)

func graphRollbackFixture(t *testing.T, s state.Store) (state.Account, state.Project, state.App, state.Deployment, state.Deployment, state.ProjectReleaseSet, state.ProjectReleaseSet) {
	t.Helper()
	a, err := s.CreateAccount(t.Context(), uuid.NewString()+"@graph-audit.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(t.Context(), state.Project{AccountID: a.ID, Slug: "audit-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, ProjectID: p.ID, Slug: "app-" + uuid.NewString()[:8], WorkloadName: "api", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	old := rollbackSafetyCreate(t, s, app, "previous", true, "production")
	first, err := s.(state.ProjectReleaseSetStore).PublishProjectReleaseSet(t.Context(), a.ID, p.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: old.ID}})
	if err != nil {
		t.Fatal(err)
	}
	next := rollbackSafetyCreate(t, s, app, "promoted", true, "production")
	second, err := s.(state.ProjectReleaseSetStore).PublishProjectReleaseSet(t.Context(), a.ID, p.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: next.ID}})
	if err != nil {
		t.Fatal(err)
	}
	return a, p, app, old, next, first, second
}

func TestGraphRollbackChecksRetainedContracts(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "1")
	rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
		_, _, app, old, next, first, second := graphRollbackFixture(t, s)
		rollbackSafetySnapshot(t, s, old, `{"openapi":"3.1.0","paths":{"/old":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
		rollbackSafetySnapshot(t, s, next, `{"openapi":"3.1.0","paths":{"/old":{"get":{"responses":{"200":{"description":"ok"}}}},"/new":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
		check, err := openapidiff.CheckDeploymentRollback(t.Context(), s, app.ID, old.ID, next.ID, "production")
		if err != nil || !check.Diff.Blocking() {
			t.Fatalf("contract control: %+v %v", check.Diff, err)
		}
		err = promotion.CheckRollbackContracts(t.Context(), s, "production", second.Members, first.Members)
		var gate *openapidiff.GateError
		if !errors.As(err, &gate) {
			t.Fatalf("breaking graph rollback was not refused: %v", err)
		}
		_, route, readErr := s.(state.ProjectReleaseSetStore).ResolveProjectRelease(t.Context(), app.ID, "production", "")
		if readErr != nil || route != next.ID {
			t.Fatalf("contract refusal changed graph: %s %v", route, readErr)
		}

	})
}

func TestGraphRollbackRejectsLaterMigration(t *testing.T) {
	rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
		a, p, app, old, _, first, second := graphRollbackFixture(t, s)
		late := rollbackSafetyCreate(t, s, app, "late migration", false, "production")
		for _, status := range []state.DeploymentStatus{state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting} {
			if err := s.UpdateDeploymentStatus(t.Context(), late.ID, status, ""); err != nil {
				t.Fatal(err)
			}
		}
		tasks := s.(state.AppTaskStore)
		now := time.Now().UTC()
		task, err := tasks.CreateAppTask(t.Context(), state.CreateAppTaskParams{AccountID: a.ID, AppID: app.ID, DeploymentID: late.ID, Kind: state.AppTaskKindRelease, Command: []string{"bin/migrate"}, CreatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := tasks.ClaimNextAppTask(t.Context(), "audit5", now, time.Minute)
		if err != nil || claimed.ID != task.ID {
			t.Fatalf("claim: %+v %v", claimed, err)
		}
		if _, err = tasks.MarkAppTaskRunning(t.Context(), task.ID, *claimed.LeaseToken, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		runs, problem, err := release.RollbackGuard(t.Context(), s, app.ID, old, false)
		if err != nil || problem == nil || problem.Code != api.CodeRollbackCrossesRelease || len(runs) != 1 {
			t.Fatalf("migration control: %+v %+v %v", runs, problem, err)
		}
		restored, err := s.(state.ProjectReleaseSetPromotionStore).PublishProjectReleaseSetIfActive(t.Context(), a.ID, p.ID, "production", second.ID, nil, 1800, first.Members)
		_, route, readErr := s.(state.ProjectReleaseSetStore).ResolveProjectRelease(t.Context(), app.ID, "production", "")
		if readErr != nil {
			t.Fatal(readErr)
		}
		if err == nil && route == old.ID {
			t.Fatalf("graph crossed a running migration: graph=%s deployment=%s", restored.ID, route)
		}
	})
}

func completedCheckedServiceRollback(t *testing.T, s state.Store) (state.Account, state.App, state.Deployment, api.RollbackOperation) {
	t.Helper()
	a, app, target, current := rollbackSafetyFixture(t, s)
	manifest := app.Manifest
	manifest.ExecutionMode = api.ExecutionModeService
	manifest.ServiceReplicas = &state.ServiceReplicas{Min: 1, Max: 2, Desired: 1}
	var err error
	app, err = s.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	checked := s.(state.CheckedRollbackStore)
	op, err := checked.CreateCheckedRollback(t.Context(), a.ID, app.ID, target.ID, current.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(t.Context(), target.ID); err != nil {
		t.Fatal(err)
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
	op, err = checked.GetCheckedRollback(t.Context(), a.ID, app.ID, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := s.(state.BindingPromotionStore).ReadBindingPromotionRevision(t.Context(), a.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	fence := state.BindingPromotionFence{AccountID: a.ID, AppID: app.ID, DeploymentID: target.ID, Scope: "default", Revision: revision, ValidUntil: time.Now().Add(time.Minute)}
	op, err = checked.CommitCheckedRollback(state.WithBindingReleaseFences(t.Context(), []state.BindingPromotionFence{fence}), op)
	if err != nil {
		t.Fatal(err)
	}
	if op.Status != "routing" {
		t.Fatalf("expected service routing: %+v", op)
	}
	if _, err = s.FinalizeServiceRollout(t.Context(), target.ID); err != nil {
		t.Fatal(err)
	}
	done, err := s.DeploymentByID(t.Context(), target.ID)
	if err != nil || done.ServiceRolloutHandoff.Phase != state.ServiceRolloutPhaseComplete || done.TrafficPercent != 100 {
		t.Fatalf("handoff control: %+v %v", done, err)
	}
	return a, app, target, op
}

func TestCheckedServiceRollbackReceiptAfterReplacement(t *testing.T) {
	rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
		a, app, target, op := completedCheckedServiceRollback(t, s)
		next := createRollbackServiceCandidate(t, s, app)
		node := state.DefaultLocalNodeName
		if pg, ok := s.(*state.PgStore); ok {
			n, e := pg.ComputeNodeByName(t.Context(), node)
			if e != nil {
				t.Fatal(e)
			}
			node = n.ID
		}
		if _, err := s.CreateInstanceWithMode(t.Context(), app.ID, next.ID, "running", 256, node, uuid.NewString(), "service"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.FinalizeServiceRollout(t.Context(), next.ID); err != nil {
			t.Fatal(err)
		}
		old, err := s.DeploymentByID(t.Context(), target.ID)
		if err != nil {
			t.Fatal(err)
		}
		err = s.(state.CheckedRollbackStore).UpdateCheckedRollback(t.Context(), op, "complete", "", nil)
		got, readErr := s.(state.CheckedRollbackStore).GetCheckedRollback(t.Context(), a.ID, app.ID, op.ID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if err != nil || got.Status != "complete" {
			t.Fatalf("completed rollback could not reconcile after normal next release: old_status=%s handoff=%s weight=%d receipt=%s error=%v", old.Status, old.ServiceRolloutHandoff.Phase, old.TrafficPercent, got.Status, err)
		}
	})
}

func TestGraphAndServiceRollbackControls(t *testing.T) {
	rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
		t.Run("normal_graph_rollback", func(t *testing.T) {
			a, p, app, old, _, first, second := graphRollbackFixture(t, s)
			if _, err := s.(state.ProjectReleaseSetPromotionStore).PublishProjectReleaseSetIfActive(t.Context(), a.ID, p.ID, "production", second.ID, nil, 1800, first.Members); err != nil {
				t.Fatal(err)
			}
			if _, route, err := s.(state.ProjectReleaseSetStore).ResolveProjectRelease(t.Context(), app.ID, "production", ""); err != nil || route != old.ID {
				t.Fatalf("normal rollback: %s %v", route, err)
			}
		})
		t.Run("normal_service_receipt", func(t *testing.T) {
			a, app, _, op := completedCheckedServiceRollback(t, s)
			if err := s.(state.CheckedRollbackStore).UpdateCheckedRollback(t.Context(), op, "complete", "", nil); err != nil {
				t.Fatal(err)
			}
			got, err := s.(state.CheckedRollbackStore).GetCheckedRollback(t.Context(), a.ID, app.ID, op.ID)
			if err != nil || got.Status != "complete" {
				t.Fatalf("normal service receipt: %+v %v", got, err)
			}
		})
	})
}

func createRollbackServiceCandidate(t *testing.T, s state.Store, app state.App) state.Deployment {
	t.Helper()
	now := time.Now().UTC()
	d, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("4", 64), Scope: "default", Status: state.DeployPending, TrafficPercent: 0, RolloutState: "rolling_out", RolloutStartedAt: &now})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetDeploymentRootfs(t.Context(), d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(t.Context(), d.ID); err != nil {
		t.Fatal(err)
	}
	return d
}
func rollbackServiceFence(t *testing.T, s state.Store, a state.Account, app state.App, d state.Deployment) context.Context {
	t.Helper()
	revision, err := s.(state.BindingPromotionStore).ReadBindingPromotionRevision(t.Context(), a.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	return state.WithBindingReleaseFences(t.Context(), []state.BindingPromotionFence{{AccountID: a.ID, AppID: app.ID, DeploymentID: d.ID, Scope: "default", Revision: revision, ValidUntil: time.Now().Add(time.Minute)}})
}
func TestServiceAbortRejectsMigrationAfterAcceptance(t *testing.T) {
	for _, late := range []bool{false, true} {
		name := "control"
		if late {
			name = "late_migration"
		}
		t.Run(name, func(t *testing.T) {
			rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
				a, app, _, current := rollbackSafetyFixture(t, s)
				manifest := app.Manifest
				manifest.ExecutionMode = api.ExecutionModeService
				manifest.ServiceReplicas = &state.ServiceReplicas{Min: 1, Max: 2, Desired: 1}
				var err error
				app, err = s.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest})
				if err != nil {
					t.Fatal(err)
				}
				candidate := createRollbackServiceCandidate(t, s, app)
				node := state.DefaultLocalNodeName
				if pg, ok := s.(*state.PgStore); ok {
					n, e := pg.ComputeNodeByName(t.Context(), node)
					if e != nil {
						t.Fatal(e)
					}
					node = n.ID
				}
				for _, d := range []state.Deployment{current, candidate} {
					if _, err = s.CreateInstanceWithMode(t.Context(), app.ID, d.ID, "running", 256, node, uuid.NewString(), "service"); err != nil {
						t.Fatal(err)
					}
				}
				if _, err = s.BeginServiceRolloutCutover(rollbackServiceFence(t, s, a, app, candidate), candidate.ID); err != nil {
					t.Fatal(err)
				}
				request, _, requestErr := s.(state.ServiceRolloutBindingStore).RequestServiceRolloutAbort(t.Context(), app.ID, candidate.ID, current.ID, "rollback requested")
				if requestErr != nil {
					t.Fatal(requestErr)
				}
				var waiting state.AppTask
				if !late {
					waiting = prepareRollbackServiceMigration(t, s, a, app)
				}
				if late {
					startRollbackServiceMigration(t, s, a, app)
					if _, problem, e := release.RollbackGuard(t.Context(), s, app.ID, current, false); e != nil || problem == nil || problem.Code != api.CodeRollbackCrossesRelease {
						t.Fatalf("migration control: %+v %v", problem, e)
					}
				}
				writeCtx := state.WithServiceRolloutBindingRequest(rollbackServiceFence(t, s, a, app, current), request.ServiceRolloutHandoff.BindingsCheck.RequestID)
				_, err = s.BeginServiceRolloutAbort(writeCtx, candidate.ID)
				restored, readErr := s.DeploymentByID(t.Context(), current.ID)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if late && (!errors.Is(err, state.ErrRollbackCrossesRelease) || restored.TrafficPercent > 0) {
					t.Fatalf("service abort crossed migration: predecessor traffic=%d error=%v", restored.TrafficPercent, err)
				}
				if late {
					if _, err = s.AbortServiceRollout(writeCtx, candidate.ID, "legacy abort"); !errors.Is(err, state.ErrRollbackCrossesRelease) {
						t.Fatalf("legacy abort crossed migration: %v", err)
					}
				}
				if !late && (err != nil || restored.TrafficPercent != 100) {
					t.Fatalf("normal abort control: %+v %v", restored, err)
				}
				if !late {
					if _, err = s.(state.AppTaskStore).MarkAppTaskRunning(t.Context(), waiting.ID, *waiting.LeaseToken, time.Now().UTC()); err != nil {
						t.Fatal(err)
					}
					if _, err = s.AbortServiceRollout(t.Context(), candidate.ID, "finish restored handoff"); err != nil {
						t.Fatalf("migration already served by the restored predecessor blocked drain completion: %v", err)
					}
				}
			})
		})
	}
}

func TestRetainedServiceRollbackReceiptAllowsNextRecovery(t *testing.T) {
	rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
		a, app, target, op := completedCheckedServiceRollback(t, s)
		manifest := app.Manifest
		manifest.RevisionPinTTLSeconds = 3600
		var err error
		app, err = s.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest})
		if err != nil {
			t.Fatal(err)
		}
		next := createRollbackServiceCandidate(t, s, app)
		node := state.DefaultLocalNodeName
		if pg, ok := s.(*state.PgStore); ok {
			n, e := pg.ComputeNodeByName(t.Context(), node)
			if e != nil {
				t.Fatal(e)
			}
			node = n.ID
		}
		if _, err = s.CreateInstanceWithMode(t.Context(), app.ID, next.ID, "running", 256, node, uuid.NewString(), "service"); err != nil {
			t.Fatal(err)
		}
		if _, err = s.FinalizeServiceRollout(t.Context(), next.ID); err != nil {
			t.Fatal(err)
		}
		old, err := s.DeploymentByID(t.Context(), target.ID)
		if err != nil || old.Status != state.DeployLive || old.TrafficPercent != 0 {
			t.Fatalf("retention control: %+v %v", old, err)
		}
		checked := s.(state.CheckedRollbackStore)
		finishErr := checked.UpdateCheckedRollback(t.Context(), op, "complete", "", nil)
		_, retryErr := checked.CreateCheckedRollback(t.Context(), a.ID, app.ID, target.ID, next.ID, "second incident")
		if finishErr != nil || retryErr != nil {
			t.Fatalf("completed retained rollback blocks later recovery: completion_error=%v new_rollback_error=%v", finishErr, retryErr)
		}
	})
}

func startRollbackServiceMigration(t *testing.T, s state.Store, account state.Account, app state.App) {
	t.Helper()
	claimed := prepareRollbackServiceMigration(t, s, account, app)
	if _, err := s.(state.AppTaskStore).MarkAppTaskRunning(t.Context(), claimed.ID, *claimed.LeaseToken, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func prepareRollbackServiceMigration(t *testing.T, s state.Store, account state.Account, app state.App) state.AppTask {
	t.Helper()
	d, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("6", 64), Scope: "default", Status: state.DeployPending, ReleaseCommand: []string{"bin/migrate"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetDeploymentRootfs(t.Context(), d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
		t.Fatal(err)
	}
	for _, status := range []state.DeploymentStatus{state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting} {
		if err := s.UpdateDeploymentStatus(t.Context(), d.ID, status, ""); err != nil {
			t.Fatal(err)
		}
	}
	tasks := s.(state.AppTaskStore)
	now := time.Now().UTC()
	task, err := tasks.CreateAppTask(t.Context(), state.CreateAppTaskParams{AccountID: account.ID, AppID: app.ID, DeploymentID: d.ID, Kind: state.AppTaskKindRelease, Command: []string{"bin/migrate"}, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := tasks.ClaimNextAppTask(t.Context(), "rollbackSafety", now, time.Minute)
	if err != nil || claimed.ID != task.ID {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	return claimed
}
