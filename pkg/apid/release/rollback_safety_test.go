package release_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/promotion"
	"github.com/onebox-faas/faas/pkg/apid/release"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func rollbackSafetyStores(t *testing.T, check func(*testing.T, state.Store)) {
	t.Helper()
	t.Run("mem", func(t *testing.T) { check(t, state.NewMemStore()) })
	t.Run("pg", func(t *testing.T) { check(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}
func rollbackSafetyCreate(t *testing.T, s state.Store, app state.App, reason string, live bool, scopes ...string) state.Deployment {
	t.Helper()
	scope := "default"
	if len(scopes) > 0 {
		scope = scopes[0]
	}
	d, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("1", 64), Scope: scope, Status: state.DeployPending, Reason: reason})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetDeploymentRootfs(t.Context(), d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if live {
		if err = s.MarkDeploymentLive(t.Context(), d.ID); err != nil {
			t.Fatal(err)
		}
	}
	d, err = s.DeploymentByID(t.Context(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func rollbackSafetyFixture(t *testing.T, s state.Store, scopes ...string) (state.Account, state.App, state.Deployment, state.Deployment) {
	t.Helper()
	account, err := s.CreateAccount(t.Context(), uuid.NewString()+"@audit.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "audit-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	target := rollbackSafetyCreate(t, s, app, "old release", true, scopes...)
	current := rollbackSafetyCreate(t, s, app, "environment promotion/rollbackSafety", true, scopes...)
	return account, app, target, current
}
func rollbackSafetyStartRelease(t *testing.T, s state.Store, account state.Account, app state.App) {
	t.Helper()
	d := rollbackSafetyCreate(t, s, app, "next release", false)
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
	if _, err = tasks.MarkAppTaskRunning(t.Context(), task.ID, *claimed.LeaseToken, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}
func TestPlainRollbackRejectsMigrationStartedDuringReadiness(t *testing.T) {
	rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
		account, app, target, current := rollbackSafetyFixture(t, s)
		if _, p, err := release.RollbackGuard(t.Context(), s, app.ID, target, false); err != nil || p != nil {
			t.Fatalf("admission control: %+v %v", p, err)
		}
		if _, err := s.PrepareDeploymentRollback(t.Context(), app.ID, target.ID); err != nil {
			t.Fatal(err)
		}
		rollbackSafetyStartRelease(t, s, account, app)
		if _, p, err := release.RollbackGuard(t.Context(), s, app.ID, target, false); err != nil || p == nil || p.Code != api.CodeRollbackCrossesRelease {
			t.Fatalf("fresh guard control: %+v %v", p, err)
		}
		err := s.MarkDeploymentLive(state.WithPreparedRollbackPromotion(t.Context()), target.ID)
		got, readErr := s.DeploymentByID(t.Context(), target.ID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !errors.Is(err, state.ErrRollbackCrossesRelease) || got.TrafficPercent != 0 {
			t.Fatalf("plain rollback crossed new migration: target=%s prior=%s", got.ID, current.ID)
		}
	})
}
func rollbackSafetySnapshot(t *testing.T, s state.Store, d state.Deployment, doc string) {
	t.Helper()
	spec, err := openapidiff.LoadBytes([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	raw, hash, err := openapidiff.MarshalSnapshot(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.(state.OpenAPISnapshotStore).UpdateDeploymentOpenAPISnapshot(t.Context(), state.OpenAPISnapshot{DeploymentID: d.ID, AppID: d.AppID, Scope: d.Scope, Snapshot: raw, SHA256: hash, SchemaVersion: openapidiff.SnapshotSchemaVersion, CapturedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
}
func TestProjectRollbackRejectsBreakingContract(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "1")
	rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
		_, app, target, current := rollbackSafetyFixture(t, s, "production")
		rollbackSafetySnapshot(t, s, target, `{"openapi":"3.1.0","paths":{"/old":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
		rollbackSafetySnapshot(t, s, current, `{"openapi":"3.1.0","paths":{"/old":{"get":{"responses":{"200":{"description":"ok"}}}},"/new":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
		check, err := openapidiff.CheckDeploymentRollback(t.Context(), s, app.ID, target.ID, current.ID, "production")
		if err != nil || !check.Diff.Blocking() {
			t.Fatalf("contract control: %+v %v", check.Diff, err)
		}
		result, _, err := promotion.RollbackWorkload(t.Context(), s, state.ProjectEnvironmentPromotion{ID: "rollbackSafety", ToEnvironment: "production"}, state.ProjectEnvironmentPromotionWorkload{PreviousTargetDeploymentID: target.ID, TargetDeploymentID: current.ID}, app.ID)
		got, readErr := s.DeploymentByID(t.Context(), target.ID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var gate *openapidiff.GateError
		if !errors.As(err, &gate) || got.TrafficPercent != 0 {
			t.Fatalf("project rollback bypassed breaking contract gate: result=%s traffic=%d error=%v", result, got.TrafficPercent, err)
		}
	})
}
func rollbackSafetyRecord(t *testing.T, s state.Store, account state.Account, app state.App, target, current state.Deployment) state.RollbackRecord {
	t.Helper()
	r, err := s.(state.RollbackOutcomeStore).RecordRollbackRequest(t.Context(), state.RollbackRecord{AccountID: account.ID, AppID: app.ID, Scope: "default", TargetDeploymentID: target.ID, FromDeploymentID: current.ID, Trigger: state.RollbackTriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func rollbackSafetySweeper(s state.Store) *release.Rollbacks {
	return &release.Rollbacks{Store: s, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}
func TestRollbackOutcomesVisitNewerCompletedRequests(t *testing.T) {
	rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
		for range api.RollbackOutcomeBatchSize {
			account, app, target, current := rollbackSafetyFixture(t, s)
			if _, err := s.PrepareDeploymentRollback(t.Context(), app.ID, target.ID); err != nil {
				t.Fatal(err)
			}
			rollbackSafetyRecord(t, s, account, app, target, current)
		}
		account, app, target, current := rollbackSafetyFixture(t, s)
		if _, err := s.PrepareDeploymentRollback(t.Context(), app.ID, target.ID); err != nil {
			t.Fatal(err)
		}
		r := rollbackSafetyRecord(t, s, account, app, target, current)
		if err := s.MarkDeploymentLive(state.WithPreparedRollbackPromotion(t.Context()), target.ID); err != nil {
			t.Fatal(err)
		}
		sweeper := rollbackSafetySweeper(s)
		for range 3 {
			if err := sweeper.Sweep(t.Context(), time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
		}
		actual, err := s.(state.RollbackOutcomeStore).RollbackRecordByID(t.Context(), account.ID, app.ID, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if actual.Status != state.RollbackStatusComplete {
			t.Fatalf("completed rollback hidden behind %d pending rows after three sweeps: status=%s", api.RollbackOutcomeBatchSize, actual.Status)
		}
	})
}

type rollbackSafetyReadHook struct {
	target string
	fire   func()
	fired  bool
}

func (h *rollbackSafetyReadHook) after(d state.Deployment, err error) (state.Deployment, error) {
	if err == nil && d.ID == h.target && !h.fired {
		h.fired = true
		h.fire()
	}
	return d, err
}

type rollbackSafetyMemRead struct {
	MemStore *state.MemStore
	hook     *rollbackSafetyReadHook
}

func (f *rollbackSafetyMemRead) DeploymentByID(ctx context.Context, id string) (state.Deployment, error) {
	return f.hook.after(f.MemStore.DeploymentByID(ctx, id))
}

type rollbackSafetyPgRead struct {
	PgStore *state.PgStore
	hook    *rollbackSafetyReadHook
}

func (f *rollbackSafetyPgRead) DeploymentByID(ctx context.Context, id string) (state.Deployment, error) {
	return f.hook.after(f.PgStore.DeploymentByID(ctx, id))
}
func TestRollbackActivationWinsConcurrentTimeout(t *testing.T) {
	rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
		account, app, target, current := rollbackSafetyFixture(t, s)
		if _, err := s.PrepareDeploymentRollback(t.Context(), app.ID, target.ID); err != nil {
			t.Fatal(err)
		}
		r := rollbackSafetyRecord(t, s, account, app, target, current)
		hook := &rollbackSafetyReadHook{target: target.ID, fire: func() {
			if err := s.MarkDeploymentLive(state.WithPreparedRollbackPromotion(t.Context()), target.ID); err != nil {
				t.Fatal(err)
			}
		}}
		var wrapped state.Store
		switch original := s.(type) {
		case *state.MemStore:
			wrapped = statetest.NewMem(&rollbackSafetyMemRead{MemStore: original, hook: hook})
		case *state.PgStore:
			wrapped = statetest.NewPg(&rollbackSafetyPgRead{PgStore: original, hook: hook})
		}
		sweeper := rollbackSafetySweeper(wrapped)
		for range 2 {
			if err := sweeper.Sweep(t.Context(), r.RequestedAt.Add(api.RollbackOutcomeTimeout+time.Second)); err != nil {
				t.Fatal(err)
			}
		}
		actual, err := s.(state.RollbackOutcomeStore).RollbackRecordByID(t.Context(), account.ID, app.ID, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		serving, err := s.DeploymentByID(t.Context(), target.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !hook.fired {
			t.Fatal("interleaving hook did not run")
		}
		if actual.Status != state.RollbackStatusComplete || serving.TrafficPercent != 100 {
			t.Fatalf("receipt reports %s (%s) although rollback target serves 100%%", actual.Status, actual.ErrorCode)
		}
	})
}

func TestRollbackSafetyControls(t *testing.T) {
	rollbackSafetyStores(t, func(t *testing.T, s state.Store) {
		t.Run("plain_completion_without_queue_or_race", func(t *testing.T) {
			a, app, target, current := rollbackSafetyFixture(t, s)
			if _, err := s.PrepareDeploymentRollback(t.Context(), app.ID, target.ID); err != nil {
				t.Fatal(err)
			}
			r := rollbackSafetyRecord(t, s, a, app, target, current)
			if err := s.MarkDeploymentLive(state.WithPreparedRollbackPromotion(t.Context()), target.ID); err != nil {
				t.Fatal(err)
			}
			if err := rollbackSafetySweeper(s).Sweep(t.Context(), time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			actual, err := s.(state.RollbackOutcomeStore).RollbackRecordByID(t.Context(), a.ID, app.ID, r.ID)
			if err != nil || actual.Status != state.RollbackStatusComplete {
				t.Fatalf("completion control: %+v %v", actual, err)
			}
		})
		t.Run("timeout_without_activation_race", func(t *testing.T) {
			a, app, target, current := rollbackSafetyFixture(t, s)
			if _, err := s.PrepareDeploymentRollback(t.Context(), app.ID, target.ID); err != nil {
				t.Fatal(err)
			}
			r := rollbackSafetyRecord(t, s, a, app, target, current)
			if err := rollbackSafetySweeper(s).Sweep(t.Context(), r.RequestedAt.Add(api.RollbackOutcomeTimeout+time.Second)); err != nil {
				t.Fatal(err)
			}
			actual, err := s.(state.RollbackOutcomeStore).RollbackRecordByID(t.Context(), a.ID, app.ID, r.ID)
			if err != nil || actual.Status != state.RollbackStatusFailed {
				t.Fatalf("timeout control: %+v %v", actual, err)
			}
		})
		t.Run("compatible_project_rollback", func(t *testing.T) {
			t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "1")
			_, app, target, current := rollbackSafetyFixture(t, s, "production")
			doc := `{"openapi":"3.1.0","paths":{"/old":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
			rollbackSafetySnapshot(t, s, target, doc)
			rollbackSafetySnapshot(t, s, current, doc)
			check, err := openapidiff.CheckDeploymentRollback(t.Context(), s, app.ID, target.ID, current.ID, "production")
			if err != nil || check.Diff.Blocking() {
				t.Fatalf("compatible contract control: %+v %v", check.Diff, err)
			}
			result, _, err := promotion.RollbackWorkload(t.Context(), s, state.ProjectEnvironmentPromotion{ID: "rollbackSafety", ToEnvironment: "production"}, state.ProjectEnvironmentPromotionWorkload{PreviousTargetDeploymentID: target.ID, TargetDeploymentID: current.ID}, app.ID)
			if err != nil || result != "restored" {
				t.Fatalf("project control: %s %v", result, err)
			}
		})
	})
}
