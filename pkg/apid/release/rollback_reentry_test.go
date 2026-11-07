package release_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func rollbackReentryFixture(t *testing.T, s state.Store, ttl int) (state.Account, state.App, state.Deployment, state.Deployment) {
	t.Helper()
	a, err := s.CreateAccount(t.Context(), uuid.NewString()+"@rollback-more.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, Slug: "more-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5, Manifest: state.AppManifest{RevisionPinTTLSeconds: ttl}})
	if err != nil {
		t.Fatal(err)
	}
	target := rollbackSafetyCreate(t, s, app, "target", true)
	current := rollbackSafetyCreate(t, s, app, "current", true)
	return a, app, target, current
}
func readyReentryRollback(t *testing.T, s state.Store, a state.Account, app state.App, target, current state.Deployment) api.RollbackOperation {
	t.Helper()
	c := s.(state.CheckedRollbackStore)
	op, err := c.CreateCheckedRollback(t.Context(), a.ID, app.ID, target.ID, current.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if op.Status == "preparing" {
		if err = s.MarkDeploymentLive(t.Context(), target.ID); err != nil {
			t.Fatal(err)
		}
	}
	op, err = c.GetCheckedRollback(t.Context(), a.ID, app.ID, op.ID)
	if err != nil || op.Status != "ready" {
		t.Fatalf("ready: %+v %v", op, err)
	}
	return op
}
func TestRollbackGraphRejectsAppRecovery(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "0")
	for _, checked := range []bool{false, true} {
		for _, phase := range []string{"no_graph", "admission", "cutover"} {
			name := "plain/" + phase
			if checked {
				name = "checked/" + phase
			}
			t.Run(name, func(t *testing.T) {
				rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
					a, p, app, target, _ := rollbackWindowFixture(t, s)
					current := rollbackSafetyCreate(t, s, app, "current", true, "production")
					publish := func() {
						if _, err := s.(state.ProjectReleaseSetStore).PublishProjectReleaseSet(t.Context(), a.ID, p.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: current.ID}}); err != nil {
							t.Fatal(err)
						}
					}
					if phase == "admission" {
						publish()
					}
					var op api.RollbackOperation
					var err error
					if checked {
						op, err = s.(state.CheckedRollbackStore).CreateCheckedRollback(t.Context(), a.ID, app.ID, target.ID, current.ID, "")
					} else {
						_, err = s.PrepareDeploymentRollback(t.Context(), app.ID, target.ID)
					}
					if phase == "admission" {
						if !errors.Is(err, state.ErrProjectReleaseRollbackRequired) {
							t.Fatalf("graph-owned admission: %v", err)
						}
						unchanged, e := s.DeploymentByID(t.Context(), target.ID)
						if e != nil || unchanged.Status != state.DeployLive || unchanged.TrafficPercent != 0 {
							t.Fatalf("rejected admission changed target: %+v %v", unchanged, e)
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						if checked {
							if op.Status == "preparing" {
								if err = s.MarkDeploymentLive(t.Context(), target.ID); err != nil {
									t.Fatal(err)
								}
							}
							op, err = s.(state.CheckedRollbackStore).GetCheckedRollback(t.Context(), a.ID, app.ID, op.ID)
							if err != nil {
								t.Fatal(err)
							}
						}
						if phase == "cutover" {
							publish()
						}
						if checked {
							op, err = s.(state.CheckedRollbackStore).CommitCheckedRollback(t.Context(), op)
						} else {
							err = s.MarkDeploymentLive(state.WithPreparedRollbackPromotion(t.Context()), target.ID)
						}
						if phase == "cutover" {
							if !errors.Is(err, state.ErrProjectReleaseRollbackRequired) {
								t.Fatalf("graph appeared during readiness: %v", err)
							}
						} else if err != nil || checked && op.Status != "complete" {
							t.Fatalf("ordinary rollback: %+v %v", op, err)
						}
					}
					actual, err := state.ResolveProductionDeployment(t.Context(), s, app.ID)
					if err != nil {
						t.Fatal(err)
					}
					want := target.ID
					if phase != "no_graph" {
						want = current.ID
					}
					if actual.ID != want {
						t.Fatalf("production selected %s, want %s", actual.ID, want)
					}
				})
			})
		}
	}
}
func TestRollbackExecutionModeChangeRequiresNewIntent(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "0")
	for _, scenario := range []string{"service_before_intent", "request_to_service", "service_to_request"} {
		t.Run(scenario, func(t *testing.T) {
			rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
				a, app, target, current := rollbackReentryFixture(t, s, 3600)
				change := func(service bool) {
					m := app.Manifest
					m.ExecutionMode = api.ExecutionModeRequest
					m.ServiceReplicas = nil
					if service {
						m.ExecutionMode = api.ExecutionModeService
						m.ServiceReplicas = &state.ServiceReplicas{Min: 1, Max: 2, Desired: 1}
					}
					var err error
					app, err = s.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &m})
					if err != nil {
						t.Fatal(err)
					}
				}
				if scenario != "request_to_service" {
					change(true)
				}
				op := readyReentryRollback(t, s, a, app, target, current)
				if scenario != "service_before_intent" {
					change(scenario == "request_to_service")
				}
				_, err := s.(state.CheckedRollbackStore).CommitCheckedRollback(t.Context(), op)
				actual, e := s.DeploymentByID(t.Context(), target.ID)
				if e != nil {
					t.Fatal(e)
				}
				if actual.TrafficPercent != 0 {
					t.Fatalf("mode change published unchecked traffic: %+v", actual)
				}
				want := state.ErrCheckedRollbackChanged
				if scenario == "service_before_intent" {
					want = state.ErrServiceRolloutNotReady
				}
				if !errors.Is(err, want) {
					t.Fatalf("commit=%v want=%v", err, want)
				}
			})
		})
	}
}
func TestRollbackRetirementStartsFreshPinWindow(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "0")
	for _, checked := range []bool{false, true} {
		for _, cleanup := range []bool{true, false} {
			name := fmt.Sprintf("checked=%t/cleanup=%t", checked, cleanup)
			t.Run(name, func(t *testing.T) {
				rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
					a, app, old, current := rollbackReentryFixture(t, s, 2)
					activate := func(target, current state.Deployment) {
						t.Helper()
						if checked {
							op := readyReentryRollback(t, s, a, app, target, current)
							if _, err := s.(state.CheckedRollbackStore).CommitCheckedRollback(t.Context(), op); err != nil {
								t.Fatal(err)
							}
						} else {
							if _, err := s.PrepareDeploymentRollback(t.Context(), app.ID, target.ID); err != nil {
								t.Fatal(err)
							}
							if err := s.MarkDeploymentLive(state.WithPreparedRollbackPromotion(t.Context()), target.ID); err != nil {
								t.Fatal(err)
							}
						}
					}
					activate(old, current)
					time.Sleep(2200 * time.Millisecond)
					if cleanup {
						if _, err := s.(state.RevisionPinStore).ExpireRevisionPins(t.Context()); err != nil {
							t.Fatal(err)
						}
					}
					activate(current, old)
					if _, err := s.(state.RevisionPinStore).ResolveRevisionPin(t.Context(), app.ID, "default", old.ID); err != nil {
						t.Fatalf("new serving period lost its pin window: %v", err)
					}
				})
			})
		}
	}
}
func TestRollbackLargeReleaseReview(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "0")
	for _, count := range []int{60, 160} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
				a, app, target, current := rollbackReentryFixture(t, s, 3600)
				tasks := s.(state.AppTaskStore)
				for i := 0; i < count; i++ {
					d := rollbackSafetyCreate(t, s, app, "migration", false)
					for _, status := range []state.DeploymentStatus{state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting} {
						if err := s.UpdateDeploymentStatus(t.Context(), d.ID, status, ""); err != nil {
							t.Fatal(err)
						}
					}
					at := time.Now().UTC()
					task, err := tasks.CreateAppTask(t.Context(), state.CreateAppTaskParams{AccountID: a.ID, AppID: app.ID, DeploymentID: d.ID, Kind: state.AppTaskKindRelease, Command: []string{"bin/migrate"}, CreatedAt: at})
					if err != nil {
						t.Fatal(err)
					}
					claimed, err := tasks.ClaimNextAppTask(t.Context(), "audit-more", at, time.Minute)
					if err != nil || claimed.ID != task.ID {
						t.Fatalf("claim: %+v %v", claimed, err)
					}
					running, err := tasks.MarkAppTaskRunning(t.Context(), task.ID, *claimed.LeaseToken, time.Now().UTC())
					if err != nil {
						t.Fatal(err)
					}
					exit := 0
					if _, err = tasks.CompleteAppTask(t.Context(), state.CompleteAppTaskParams{ID: task.ID, LeaseToken: *claimed.LeaseToken, Status: state.AppTaskSucceeded, ExitCode: &exit, FinishedAt: running.StartedAt.Add(time.Millisecond)}); err != nil {
						t.Fatal(err)
					}
				}
				runs, err := s.(state.ReleaseRunStore).ReleaseRunsCrossedBy(t.Context(), app.ID, target.ID)
				if err != nil || len(runs) != count {
					t.Fatalf("history count=%d want=%d err=%v", len(runs), count, err)
				}
				raw, err := json.Marshal(runs)
				if err != nil {
					t.Fatal(err)
				}
				op, err := s.(state.CheckedRollbackStore).CreateCheckedRollback(state.WithCheckedRollbackReleaseReview(t.Context(), runs), a.ID, app.ID, target.ID, current.ID, "reviewed migrations")
				t.Logf("actual_migrations=%d serialized_review_bytes=%d accepted_status=%s error=%v", count, len(raw), op.Status, err)
				if err != nil {
					t.Errorf("acknowledged historical rollback failed as history grew: %v", err)
					return
				}
				if err = s.(state.CheckedRollbackStore).UpdateCheckedRollback(t.Context(), op, "blocked", "awaiting_recheck", nil); err != nil {
					t.Fatal(err)
				}
				op, err = s.(state.CheckedRollbackStore).GetCheckedRollback(t.Context(), a.ID, app.ID, op.ID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.(state.CheckedRollbackStore).CommitCheckedRollback(t.Context(), op); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestRollbackReceiptKeepsPublicBudget(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	a, app, target, current := rollbackReentryFixture(t, s, 3600)
	op := readyReentryRollback(t, s, a, app, target, current)
	for _, extra := range []map[string]any{{"public_padding": strings.Repeat("x", 17000)}, {"_release_review": map[string]any{}}} {
		raw, err := json.Marshal(extra)
		if err != nil {
			t.Fatal(err)
		}
		_, err = pool.Exec(t.Context(), "UPDATE deployment_rollback_operations SET receipt=receipt || $2::jsonb WHERE id=$1", op.ID, raw)
		var check *pgconn.PgError
		if !errors.As(err, &check) || check.Code != "23514" || check.ConstraintName != "deployment_rollback_operations_public_receipt_check" {
			t.Fatalf("invalid public receipt accepted: %v", err)
		}
	}
	if err := s.UpdateCheckedRollback(t.Context(), op, "blocked", "retry", nil); err != nil {
		t.Fatal(err)
	}
}
func TestRollbackGraphRejectsLegacyProductionLane(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "0")
	rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
		a, p, app, _, _ := rollbackWindowFixture(t, s)
		production := rollbackSafetyCreate(t, s, app, "production", true, "production")
		target := rollbackSafetyCreate(t, s, app, "legacy target", true)
		current := rollbackSafetyCreate(t, s, app, "legacy current", true)
		if _, err := s.(state.ProjectReleaseSetStore).PublishProjectReleaseSet(t.Context(), a.ID, p.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: production.ID}}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.PrepareDeploymentRollback(t.Context(), app.ID, target.ID); !errors.Is(err, state.ErrProjectReleaseRollbackRequired) {
			t.Fatalf("legacy plain rollback bypassed production graph: %v", err)
		}
		if _, err := s.(state.CheckedRollbackStore).CreateCheckedRollback(t.Context(), a.ID, app.ID, target.ID, current.ID, ""); !errors.Is(err, state.ErrProjectReleaseRollbackRequired) {
			t.Fatalf("legacy checked rollback bypassed production graph: %v", err)
		}
	})
}

func TestRollbackGraphRejectsStaleSnapshotPG(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	a, p, app, target, _ := rollbackWindowFixture(t, s)
	current := rollbackSafetyCreate(t, s, app, "current", true, "production")
	tx, err := pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	u := func(id string) pgtype.UUID { return pgtype.UUID{Bytes: uuid.MustParse(id), Valid: true} }
	q := sqlc.New()
	blocked, err := q.ActiveGraphBlocksAppRollback(t.Context(), tx, sqlc.ActiveGraphBlocksAppRollbackParams{AppID: u(app.ID), Scope: "production", TargetID: u(target.ID)})
	if err != nil || blocked {
		t.Fatalf("snapshot before graph: blocked=%v err=%v", blocked, err)
	}
	if _, err = s.PublishProjectReleaseSet(t.Context(), a.ID, p.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: current.ID}}); err != nil {
		t.Fatal(err)
	}
	_, err = q.LockCheckedRollbackApp(t.Context(), tx, sqlc.LockCheckedRollbackAppParams{AppID: u(app.ID), AccountID: u(a.ID)})
	if err == nil {
		err = q.GuardWorkflowAdmissionSnapshot(t.Context(), tx, app.ID)
	}
	var conflict *pgconn.PgError
	if !errors.As(err, &conflict) || conflict.Code != "40001" {
		t.Fatalf("stale graph snapshot was not retried after app-lock acquisition: %v", err)
	}
}

func TestRollbackGraphKeepsPreviewRecoveryIndependent(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "0")
	for _, checked := range []bool{false, true} {
		t.Run(fmt.Sprintf("checked=%t", checked), func(t *testing.T) {
			rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
				a, p, parent, _, _ := rollbackWindowFixture(t, s)
				production := rollbackSafetyCreate(t, s, parent, "production", true, "production")
				if _, err := s.(state.ProjectReleaseSetStore).PublishProjectReleaseSet(t.Context(), a.ID, p.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: parent.ID, DeploymentID: production.ID}}); err != nil {
					t.Fatal(err)
				}
				preview, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, ProjectID: p.ID, Slug: "preview-" + uuid.NewString()[:8], PreviewOfSlug: parent.Slug, PreviewPrNumber: 42, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
				if err != nil {
					t.Fatal(err)
				}
				target := rollbackSafetyCreate(t, s, preview, "old preview", true)
				current := rollbackSafetyCreate(t, s, preview, "new preview", true)
				if checked {
					op := readyReentryRollback(t, s, a, preview, target, current)
					if _, err = s.(state.CheckedRollbackStore).CommitCheckedRollback(t.Context(), op); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err = s.PrepareDeploymentRollback(t.Context(), preview.ID, target.ID); err != nil {
						t.Fatal(err)
					}
					if err = s.MarkDeploymentLive(state.WithPreparedRollbackPromotion(t.Context()), target.ID); err != nil {
						t.Fatal(err)
					}
				}
				actual, err := s.LiveDeploymentForScope(t.Context(), preview.ID, "default")
				if err != nil || actual.ID != target.ID {
					t.Fatalf("preview rollback: %+v %v", actual, err)
				}
				actual, err = state.ResolveProductionDeployment(t.Context(), s, parent.ID)
				if err != nil || actual.ID != production.ID {
					t.Fatalf("preview changed production: %+v %v", actual, err)
				}
			})
		})
	}
}
