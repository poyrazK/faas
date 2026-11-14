package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPlainRollbackReleaseReviewSurvivesRestart(t *testing.T) {
	rollbackRegressionStores(t, func(t *testing.T, s state.Store) {
		a, app, target, current := checkedRollbackFixture(t, s, false)
		startCheckedRollbackRelease(t, s, a, app)
		runs, err := s.(state.ReleaseRunStore).ReleaseRunsCrossedBy(t.Context(), app.ID, target.ID)
		if err != nil || len(runs) != 1 {
			t.Fatalf("release history: %+v %v", runs, err)
		}
		if _, err = s.PrepareDeploymentRollback(state.WithRollbackReleaseReview(t.Context(), runs), app.ID, target.ID); err != nil {
			t.Fatal(err)
		}
		if pg, ok := s.(*state.PgStore); ok {
			s = state.NewPgStore((*pg.Pool()).Pool)
		}
		if err = s.MarkDeploymentLive(state.WithPreparedRollbackPromotion(t.Context()), target.ID); err != nil {
			t.Fatalf("acknowledged rollback failed: %v", err)
		}
		got, err := s.DeploymentByID(t.Context(), target.ID)
		if err != nil || got.TrafficPercent != 100 {
			t.Fatalf("target: %+v %v", got, err)
		}
		old, err := s.DeploymentByID(t.Context(), current.ID)
		if err != nil || old.TrafficPercent != 0 {
			t.Fatalf("previous: %+v %v", old, err)
		}
	})
}

func TestPlainRollbackReviewDoesNotAcknowledgeLaterRuns(t *testing.T) {
	rollbackRegressionStores(t, func(t *testing.T, s state.Store) {
		a, app, target, current := checkedRollbackFixture(t, s, false)
		startCheckedRollbackRelease(t, s, a, app)
		runs, err := s.(state.ReleaseRunStore).ReleaseRunsCrossedBy(t.Context(), app.ID, target.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.PrepareDeploymentRollback(state.WithRollbackReleaseReview(t.Context(), runs), app.ID, target.ID); err != nil {
			t.Fatal(err)
		}
		startCheckedRollbackRelease(t, s, a, app)
		if err = s.MarkDeploymentLive(state.WithPreparedRollbackPromotion(t.Context()), target.ID); !errors.Is(err, state.ErrRollbackCrossesRelease) {
			t.Fatalf("later migration inherited acknowledgement: %v", err)
		}
		live, err := s.DeploymentByID(t.Context(), current.ID)
		if err != nil || live.TrafficPercent != 100 {
			t.Fatalf("refusal changed traffic: %+v %v", live, err)
		}
	})
}

func TestPlainRollbackNewAttemptDoesNotInheritReview(t *testing.T) {
	rollbackRegressionStores(t, func(t *testing.T, s state.Store) {
		a, app, target, _ := checkedRollbackFixture(t, s, false)
		startCheckedRollbackRelease(t, s, a, app)
		runs, err := s.(state.ReleaseRunStore).ReleaseRunsCrossedBy(t.Context(), app.ID, target.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.PrepareDeploymentRollback(state.WithRollbackReleaseReview(t.Context(), runs), app.ID, target.ID); err != nil {
			t.Fatal(err)
		}
		if err = s.UpdateDeploymentStatus(t.Context(), target.ID, state.DeploySuperseded, ""); err != nil {
			t.Fatal(err)
		}
		if _, err = s.PrepareDeploymentRollback(t.Context(), app.ID, target.ID); err != nil {
			t.Fatal(err)
		}
		if err = s.MarkDeploymentLive(state.WithPreparedRollbackPromotion(t.Context()), target.ID); !errors.Is(err, state.ErrRollbackCrossesRelease) {
			t.Fatalf("new attempt inherited a previous request's review: %v", err)
		}
	})
}

func TestPlainRollbackOlderWriterCannotReuseReviewPG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	a, app, target, _ := checkedRollbackFixture(t, s, false)
	startCheckedRollbackRelease(t, s, a, app)
	runs, err := s.ReleaseRunsCrossedBy(ctx, app.ID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PrepareDeploymentRollback(state.WithRollbackReleaseReview(ctx, runs), app.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	// N-1 updates the marker without knowing the new review column. The old
	// acknowledgement is bound to its original marker and cannot authorize it.
	if _, err = pool.Exec(ctx, `UPDATE deployments SET rollback_prepared_at=rollback_prepared_at+interval '1 second' WHERE id=$1`, target.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(state.WithPreparedRollbackPromotion(ctx), target.ID); !errors.Is(err, state.ErrRollbackCrossesRelease) {
		t.Fatalf("old writer reused a stale review: %v", err)
	}
}

func TestPlainRollbackWithdrawalPrecedesReleaseRefusal(t *testing.T) {
	rollbackRegressionStores(t, func(t *testing.T, s state.Store) {
		a, app, target, _ := checkedRollbackFixture(t, s, false)
		if _, err := s.PrepareDeploymentRollback(t.Context(), app.ID, target.ID); err != nil {
			t.Fatal(err)
		}
		newest := startCheckedRollbackRelease(t, s, a, app)
		tasks, err := s.(state.AppTaskStore).ListAppTasks(t.Context(), a.ID, app.ID, 10, 0)
		if err != nil || len(tasks) != 1 || tasks[0].LeaseToken == nil {
			t.Fatalf("release task: %+v %v", tasks, err)
		}
		exit := 0
		if _, err = s.(state.AppTaskStore).CompleteAppTask(t.Context(), state.CompleteAppTaskParams{ID: tasks[0].ID, LeaseToken: *tasks[0].LeaseToken, Status: state.AppTaskSucceeded, ExitCode: &exit, FinishedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		if err = s.MarkDeploymentLive(t.Context(), newest.ID); err != nil {
			t.Fatal(err)
		}
		if err = s.MarkDeploymentLive(state.WithPreparedRollbackPromotion(t.Context()), target.ID); !errors.Is(err, state.ErrDeploymentSuperseded) {
			t.Fatalf("withdrawn rollback was not superseded: %v", err)
		}
		if live, err := s.DeploymentByID(t.Context(), newest.ID); err != nil || live.TrafficPercent != 100 {
			t.Fatalf("withdrawn rollback changed traffic: %+v %v", live, err)
		}
	})
}

func TestDatabaseSwitchTrafficWritesRequireCurrentLease(t *testing.T) {
	for _, takeover := range []bool{false, true} {
		name := "valid"
		if takeover {
			name = "replaced"
		}
		t.Run(name, func(t *testing.T) {
			rollbackRegressionStores(t, func(t *testing.T, s state.Store) {
				a, app, target, current := checkedRollbackFixture(t, s, false)
				run := startCheckedRollbackRelease(t, s, a, app)
				switches := s.(state.DatabaseSwitchStore)
				now := time.Now().UTC()
				_, err := switches.CreateDatabaseSwitch(t.Context(), state.DatabaseSwitch{AccountID: a.ID, AppID: app.ID, Scope: "default", TargetDeploymentID: target.ID, FromDeploymentID: current.ID, SourceDatabaseID: uuid.NewString(), UndoneDeploymentIDs: []string{run.ID}, TargetName: "lease-copy", RestorePoint: now.Add(-time.Hour), CreatedAt: now})
				if err != nil {
					t.Fatal(err)
				}
				claim := func(token string, at time.Time) state.DatabaseSwitch {
					t.Helper()
					rows, err := switches.ClaimDueDatabaseSwitches(t.Context(), token, at, at.Add(time.Minute), 1)
					if err != nil || len(rows) != 1 {
						t.Fatalf("claim: %+v %v", rows, err)
					}
					return rows[0]
				}
				owned := claim("setup", time.Now().UTC())
				owned.Phase, owned.FencedAt, owned.PhaseStartedAt = state.DatabaseSwitchSwitching, &now, now
				if _, err = switches.SaveDatabaseSwitch(t.Context(), owned, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				owned = claim("owner-a", time.Now().UTC())
				ctx := state.WithDatabaseSwitchLease(t.Context(), owned)
				if _, err = s.PrepareDeploymentRollback(ctx, app.ID, target.ID); err != nil {
					t.Fatalf("valid lease preparation: %v", err)
				}
				if takeover {
					_ = claim("owner-b", owned.LeaseUntil.Add(time.Second))
					if _, err = s.PrepareDeploymentRollback(ctx, app.ID, target.ID); !errors.Is(err, state.ErrDatabaseSwitchLeaseLost) {
						t.Fatalf("stale lease prepared code: %v", err)
					}
					if err = s.MarkDeploymentLive(state.WithPreparedRollbackPromotion(ctx), target.ID); !errors.Is(err, state.ErrDatabaseSwitchLeaseLost) {
						t.Fatalf("stale lease published code: %v", err)
					}
					live, err := s.DeploymentByID(t.Context(), current.ID)
					if err != nil || live.TrafficPercent != 100 {
						t.Fatalf("lost lease changed traffic: %+v %v", live, err)
					}
				} else if err = s.MarkDeploymentLive(state.WithPreparedRollbackPromotion(ctx), target.ID); err != nil {
					t.Fatalf("valid switch lease could not publish its restored code: %v", err)
				}
			})
		})
	}
}
