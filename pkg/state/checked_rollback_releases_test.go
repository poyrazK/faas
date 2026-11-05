package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
)

const rollbackContractBefore = `{"openapi":"3.1.0","paths":{"/old":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
const rollbackContractAfter = `{"openapi":"3.1.0","paths":{"/old":{"get":{"responses":{"200":{"description":"ok"}}}},"/new":{"get":{"responses":{"200":{"description":"ok"}}}}}}`

func rollbackRegressionStores(t *testing.T, fn func(*testing.T, state.Store)) {
	t.Helper()
	t.Run("mem", func(t *testing.T) { fn(t, state.NewMemStore()) })
	t.Run("pg", func(t *testing.T) { s, _ := pgStore(t); fn(t, s) })
}
func TestCheckedRollbackRetainsHistoricalContract(t *testing.T) {
	openapidiff.RegisterStateCapture()
	t.Cleanup(func() { state.RegisterOpenAPICapture(nil) })
	rollbackRegressionStores(t, func(t *testing.T, s state.Store) {
		a, app, target, current := checkedRollbackFixture(t, s, false)
		spec, err := openapidiff.LoadBytes([]byte(rollbackContractBefore))
		if err != nil {
			t.Fatal(err)
		}
		raw, hash, err := openapidiff.MarshalSnapshot(spec)
		if err != nil {
			t.Fatal(err)
		}
		original := state.OpenAPISnapshot{DeploymentID: target.ID, AppID: app.ID, Scope: target.Scope, Snapshot: raw, SHA256: hash, SchemaVersion: openapidiff.SnapshotSchemaVersion, CapturedAt: time.Now().UTC().Truncate(time.Microsecond)}
		snapshots := s.(state.OpenAPISnapshotStore)
		if err = snapshots.UpdateDeploymentOpenAPISnapshot(t.Context(), original); err != nil {
			t.Fatal(err)
		}
		original, err = snapshots.OpenAPISnapshotByDeployment(t.Context(), target.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.UpsertAppOpenAPIDoc(t.Context(), app.ID, a.ID, []byte(rollbackContractAfter), 2, "3.1.0"); err != nil {
			t.Fatal(err)
		}
		if _, err = s.(state.CheckedRollbackStore).CreateCheckedRollback(t.Context(), a.ID, app.ID, target.ID, current.ID, "retain captured contract"); err != nil {
			t.Fatal(err)
		}
		if err = s.MarkDeploymentLive(t.Context(), target.ID); err != nil {
			t.Fatal(err)
		}
		retained, err := snapshots.OpenAPISnapshotByDeployment(t.Context(), target.ID)
		if err != nil || retained.SHA256 != original.SHA256 || string(retained.Snapshot) != string(original.Snapshot) || !retained.CapturedAt.Equal(original.CapturedAt) {
			t.Fatalf("rollback replaced captured contract: original=%+v retained=%+v error=%v", original, retained, err)
		}
	})
}

func prepareCheckedRollbackRelease(t *testing.T, s state.Store, a state.Account, app state.App) (state.Deployment, state.AppTask) {
	t.Helper()
	d, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("3", 64), Scope: "default", Status: state.DeploySnapshotting, ReleaseCommand: []string{"bin/migrate"}})
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
	claimed, err := tasks.ClaimNextAppTask(t.Context(), "rollback-release-test", now, time.Minute)
	if err != nil || claimed.ID != task.ID {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	return d, claimed
}
func startCheckedRollbackRelease(t *testing.T, s state.Store, a state.Account, app state.App) state.Deployment {
	t.Helper()
	d, claimed := prepareCheckedRollbackRelease(t, s, a, app)
	if _, err := s.(state.AppTaskStore).MarkAppTaskRunning(t.Context(), claimed.ID, *claimed.LeaseToken, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return d
}
func TestCheckedRollbackReleaseReviewSurvivesReadiness(t *testing.T) {
	rollbackRegressionStores(t, func(t *testing.T, s state.Store) {
		a, app, target, current := checkedRollbackFixture(t, s, false)
		crossed := startCheckedRollbackRelease(t, s, a, app)
		runs, err := s.(state.ReleaseRunStore).ReleaseRunsCrossedBy(t.Context(), app.ID, target.ID)
		if err != nil || len(runs) != 1 {
			t.Fatalf("crossed runs: %+v %v", runs, err)
		}
		checked := s.(state.CheckedRollbackStore)
		op, err := checked.CreateCheckedRollback(state.WithCheckedRollbackReleaseReview(t.Context(), runs), a.ID, app.ID, target.ID, current.ID, "acknowledged migration")
		if err != nil {
			t.Fatal(err)
		}
		if err = s.MarkDeploymentLive(t.Context(), target.ID); err != nil {
			t.Fatal(err)
		}
		op, err = checked.GetCheckedRollback(t.Context(), a.ID, app.ID, op.ID)
		if err != nil {
			t.Fatal(err)
		}
		// A blocker save and a replacement PgStore must retain the private review.
		if err = checked.UpdateCheckedRollback(t.Context(), op, "blocked", "probe_pending", nil); err != nil {
			t.Fatal(err)
		}
		if pg, ok := s.(*state.PgStore); ok {
			checked = state.NewPgStore((*pg.Pool()).Pool)
		}
		op, err = checked.GetCheckedRollback(t.Context(), a.ID, app.ID, op.ID)
		if err != nil {
			t.Fatal(err)
		}
		op, err = checked.CommitCheckedRollback(t.Context(), op)
		if err != nil || op.Status != "complete" {
			t.Fatalf("review lost during readiness/restart: %+v %v", op, err)
		}
		audits, err := s.ListDeploymentAudit(t.Context(), target.ID, 20)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, audit := range audits {
			var payload struct {
				Crossed []string `json:"crossed_releases"`
			}
			if json.Unmarshal(audit.Data, &payload) == nil && len(payload.Crossed) == 1 && payload.Crossed[0] == crossed.ID {
				found = true
			}
		}
		if !found {
			t.Fatal("checked cutover omitted acknowledged releases from audit")
		}
	})
}
func TestCheckedRollbackRejectsReleaseAfterAcknowledgement(t *testing.T) {
	rollbackRegressionStores(t, func(t *testing.T, s state.Store) {
		a, app, target, current := checkedRollbackFixture(t, s, false)
		startCheckedRollbackRelease(t, s, a, app)
		runs, err := s.(state.ReleaseRunStore).ReleaseRunsCrossedBy(t.Context(), app.ID, target.ID)
		if err != nil {
			t.Fatal(err)
		}
		checked := s.(state.CheckedRollbackStore)
		op, err := checked.CreateCheckedRollback(state.WithCheckedRollbackReleaseReview(t.Context(), runs), a.ID, app.ID, target.ID, current.ID, "acknowledged first migration")
		if err != nil {
			t.Fatal(err)
		}
		if err = s.MarkDeploymentLive(t.Context(), target.ID); err != nil {
			t.Fatal(err)
		}
		op, err = checked.GetCheckedRollback(t.Context(), a.ID, app.ID, op.ID)
		if err != nil {
			t.Fatal(err)
		}
		startCheckedRollbackRelease(t, s, a, app)
		if _, err = checked.CommitCheckedRollback(context.Background(), op); !errors.Is(err, state.ErrRollbackCrossesRelease) {
			t.Fatalf("late release gained acknowledgement: %v", err)
		}
		serving, err := s.DeploymentByID(t.Context(), current.ID)
		if err != nil || serving.TrafficPercent != 100 {
			t.Fatalf("blocked rollback changed current traffic: %+v %v", serving, err)
		}
	})
}

func TestCheckedRollbackSerializesReleaseStartPG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	a, app, _, _ := checkedRollbackFixture(t, s, false)
	_, task := prepareCheckedRollbackRelease(t, s, a, app)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	// This is the same app row held through checked rollback's history read
	// and traffic write. A release cannot start between those two steps.
	if _, err = tx.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, app.ID); err != nil {
		t.Fatal(err)
	}
	waiting, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	_, err = s.MarkAppTaskRunning(waiting, task.ID, *task.LeaseToken, time.Now().UTC())
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("release start did not wait for the rollback app lock: %v", err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MarkAppTaskRunning(ctx, task.ID, *task.LeaseToken, time.Now().UTC()); err != nil {
		t.Fatalf("release did not start after cutover unlocked: %v", err)
	}
}
