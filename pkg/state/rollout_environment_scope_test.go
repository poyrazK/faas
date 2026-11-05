package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Weights, rollback targets and rollout recovery are per environment. With
// production and staging both live on one app, these paths used to read and
// rewrite every live row of the app: a production canary stole staging's
// weight, a staging build failure zeroed production, and `rollouts recover`
// acted on whichever environment's rollout was newest.

func TestRolloutEnvironmentScopeMem(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "rollout-scope-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	newApp := func(slug string) string {
		t.Helper()
		app, err := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5, IdleTimeoutS: 30})
		if err != nil {
			t.Fatal(err)
		}
		return app.ID
	}
	runRolloutEnvironmentScope(t, ctx, m, newApp)
}

func TestRolloutEnvironmentScopePG(t *testing.T) {
	s, ctx := pgStore(t)
	acct, _ := seedPgAccountAndApp(t, s, ctx)
	newApp := func(slug string) string {
		t.Helper()
		app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5, IdleTimeoutS: 30})
		if err != nil {
			t.Fatal(err)
		}
		return app.ID
	}
	runRolloutEnvironmentScope(t, ctx, s, newApp)
}

func runRolloutEnvironmentScope(t *testing.T, ctx context.Context, s state.Store, newApp func(string) string) {
	t.Run("canary weights stay in their environment", func(t *testing.T) {
		appID := newApp("scope-canary")
		production := scopedLive(t, ctx, s, appID, state.DefaultEnvScope, "p1")
		staging := scopedLive(t, ctx, s, appID, "staging", "s1")
		weights := func(want map[string]int) {
			t.Helper()
			for id, percent := range want {
				d, err := s.DeploymentByID(ctx, id)
				if err != nil || d.Status != state.DeployLive || d.TrafficPercent != percent {
					t.Fatalf("%s = %s at %d%% (err %v), want live at %d%%", id, d.Status, d.TrafficPercent, err, percent)
				}
			}
		}
		weights(map[string]int{production.ID: 100, staging.ID: 100})

		canary := scopedCanary(t, ctx, s, appID, "c1")
		weights(map[string]int{canary.ID: 1, production.ID: 99, staging.ID: 100})
		if _, _, err := s.(state.CanaryAdvancer).AdvanceCanary(ctx, canary.ID, state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10,
			Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "test:canary"}}); err != nil {
			t.Fatalf("AdvanceCanary: %v", err)
		}
		weights(map[string]int{canary.ID: 10, production.ID: 90, staging.ID: 100})
		if _, _, err := s.RecoverRollout(ctx, appID, "promote", "test promote"); err != nil {
			t.Fatalf("RecoverRollout promote: %v", err)
		}
		weights(map[string]int{canary.ID: 100, production.ID: 0, staging.ID: 100})

		aborted := scopedCanary(t, ctx, s, appID, "c2")
		weights(map[string]int{aborted.ID: 1, canary.ID: 99, staging.ID: 100})
		if _, _, err := s.RecoverRollout(ctx, appID, "abort", "test abort"); err != nil {
			t.Fatalf("RecoverRollout abort: %v", err)
		}
		weights(map[string]int{canary.ID: 100, staging.ID: 100})
		if d, err := s.DeploymentByID(ctx, aborted.ID); err != nil || d.TrafficPercent != 0 || d.RolloutState != "aborted" {
			t.Fatalf("aborted canary = %+v err=%v, want aborted at 0%%", d, err)
		}
	})

	t.Run("a failure in one environment leaves the other's weights", func(t *testing.T) {
		appID := newApp("scope-failure")
		production := scopedLive(t, ctx, s, appID, state.DefaultEnvScope, "p1")
		staging := scopedLive(t, ctx, s, appID, "staging", "s1")
		failed, err := s.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: appID, Kind: state.DeploymentKindImage,
			Scope: "staging", ImageDigest: "sha256:s2", RootfsKey: "apps/x/s2.ext4", Status: state.DeployBuilding})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateDeploymentStatus(ctx, failed.ID, state.DeployFailed, "build failed"); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{production.ID, staging.ID} {
			if d, err := s.DeploymentByID(ctx, id); err != nil || d.Status != state.DeployLive || d.TrafficPercent != 100 {
				t.Fatalf("%s after a staging build failure = %s at %d%% (err %v), want live at 100%%", id, d.Status, d.TrafficPercent, err)
			}
		}
	})

	t.Run("app-level recovery refuses rollouts in two environments", func(t *testing.T) {
		appID := newApp("scope-ambiguous")
		scopedLive(t, ctx, s, appID, state.DefaultEnvScope, "p1")
		scopedLive(t, ctx, s, appID, "staging", "s1")
		production := scopedCanary(t, ctx, s, appID, "pc")
		stagingCanary := scopedCanaryIn(t, ctx, s, appID, "staging", "sc")
		if _, _, err := s.RecoverRollout(ctx, appID, "abort", "which one?"); !errors.Is(err, state.ErrRolloutAmbiguous) {
			t.Fatalf("ambiguous recovery = %v, want ErrRolloutAmbiguous", err)
		}
		for _, id := range []string{production.ID, stagingCanary.ID} {
			if d, err := s.DeploymentByID(ctx, id); err != nil || d.RolloutState != "rolling_out" || d.TrafficPercent != 1 {
				t.Fatalf("%s after refused recovery = %+v err=%v, want untouched", id, d, err)
			}
		}
	})

	t.Run("rollback target comes from the requested environment", func(t *testing.T) {
		appID := newApp("scope-rollback")
		p1 := scopedLive(t, ctx, s, appID, state.DefaultEnvScope, "p1")
		scopedLive(t, ctx, s, appID, state.DefaultEnvScope, "p2")
		s1 := scopedLive(t, ctx, s, appID, "staging", "s1")
		scopedLive(t, ctx, s, appID, "staging", "s2")
		if got, err := s.LatestSupersededDeployment(ctx, appID, state.DefaultEnvScope); err != nil || got.ID != p1.ID {
			t.Fatalf("production rollback target = %s err=%v, want p1 %s (staging stopped serving more recently)", got.ID, err, p1.ID)
		}
		if got, err := s.LatestSupersededDeployment(ctx, appID, "staging"); err != nil || got.ID != s1.ID {
			t.Fatalf("staging rollback target = %s err=%v, want s1 %s", got.ID, err, s1.ID)
		}
	})

	t.Run("a prepared rollback yields to a release that went live meanwhile", func(t *testing.T) {
		appID := newApp("scope-outpaced")
		v1 := scopedLive(t, ctx, s, appID, state.DefaultEnvScope, "v1")
		scopedLive(t, ctx, s, appID, state.DefaultEnvScope, "v2")
		if _, err := s.PrepareDeploymentRollback(ctx, appID, v1.ID); err != nil {
			t.Fatalf("PrepareDeploymentRollback: %v", err)
		}
		// Serving-end stamps use the database clock; keep them strictly after
		// the preparation on a coarse clock.
		time.Sleep(5 * time.Millisecond)
		v3 := scopedLive(t, ctx, s, appID, state.DefaultEnvScope, "v3")
		if err := s.MarkDeploymentLive(ctx, v1.ID); !errors.Is(err, state.ErrDeploymentSuperseded) {
			t.Fatalf("outpaced rollback promotion = %v, want ErrDeploymentSuperseded", err)
		}
		got, err := s.DeploymentByID(ctx, v1.ID)
		if err != nil || got.Status != state.DeploySuperseded || got.ErrorCode != state.RollbackOutpacedCode {
			t.Fatalf("outpaced rollback target = %s/%s err=%v, want superseded/%s", got.Status, got.ErrorCode, err, state.RollbackOutpacedCode)
		}
		if serving, err := s.DeploymentByID(ctx, v3.ID); err != nil || serving.Status != state.DeployLive || serving.TrafficPercent != 100 {
			t.Fatalf("newer release after outpaced rollback = %s at %d%% err=%v, want live at 100%%", serving.Status, serving.TrafficPercent, err)
		}
		if prepared, err := s.(state.RollbackPreparedStore).DeploymentRollbackPrepared(ctx, v1.ID); err != nil || prepared {
			t.Fatalf("rollback marker after withdrawal = %v err=%v, want cleared", prepared, err)
		}

		// Without an intervening cutover the rollback still activates.
		if _, err := s.PrepareDeploymentRollback(ctx, appID, v1.ID); err != nil {
			t.Fatalf("PrepareDeploymentRollback again: %v", err)
		}
		if err := s.MarkDeploymentLive(ctx, v1.ID); err != nil {
			t.Fatalf("rollback promotion: %v", err)
		}
		if got, err := s.DeploymentByID(ctx, v1.ID); err != nil || got.Status != state.DeployLive || got.TrafficPercent != 100 {
			t.Fatalf("rollback target = %s at %d%% err=%v, want live at 100%%", got.Status, got.TrafficPercent, err)
		}
	})

	t.Run("a rollback reported failed never cuts over later", func(t *testing.T) {
		appID := newApp("scope-timeout")
		v1 := scopedLive(t, ctx, s, appID, state.DefaultEnvScope, "v1")
		v2 := scopedLive(t, ctx, s, appID, state.DefaultEnvScope, "v2")
		target, err := s.PrepareDeploymentRollback(ctx, appID, v1.ID)
		if err != nil {
			t.Fatalf("PrepareDeploymentRollback: %v", err)
		}
		outcomes := s.(state.RollbackOutcomeStore)
		app, err := s.AppByID(ctx, appID)
		if err != nil {
			t.Fatal(err)
		}
		record, err := outcomes.RecordRollbackRequest(ctx, state.RollbackRecord{AccountID: app.AccountID, AppID: appID, Scope: target.Scope,
			TargetDeploymentID: v1.ID, FromDeploymentID: v2.ID, Trigger: state.RollbackTriggerManual})
		if err != nil {
			t.Fatal(err)
		}
		if _, finished, err := outcomes.FinishRollback(ctx, record.ID, state.RollbackOutcome{Status: state.RollbackStatusFailed,
			FinishedAt: time.Now().UTC(), ErrorCode: "rollback_timeout", Error: "timed out",
			Audit: state.DeploymentAudit{DeploymentID: uuid.MustParse(v1.ID), Kind: state.DeployRolledBack, Actor: "test", At: time.Now().UTC(), Data: []byte(`{}`)}}); err != nil || !finished {
			t.Fatalf("FinishRollback = %v, %v", finished, err)
		}
		if got, err := s.DeploymentByID(ctx, v1.ID); err != nil || got.Status != state.DeploySuperseded {
			t.Fatalf("timed-out rollback target = %s err=%v, want superseded", got.Status, err)
		}
		if prepared, err := s.(state.RollbackPreparedStore).DeploymentRollbackPrepared(ctx, v1.ID); err != nil || prepared {
			t.Fatalf("rollback marker after a failed receipt = %v err=%v, want cleared", prepared, err)
		}
		// imaged read the marker before the receipt failed: the store's recheck
		// under its lock refuses the stale prepared-rollback promotion.
		if err := s.MarkDeploymentLive(state.WithPreparedRollbackPromotion(ctx), v1.ID); !errors.Is(err, state.ErrDeploymentSuperseded) {
			t.Fatalf("stale prepared-rollback promotion = %v, want ErrDeploymentSuperseded", err)
		}
		// imaged promotes an unprepared target through the latest-revision fence.
		if err := s.MarkDeploymentLiveIfLatest(ctx, v1.ID); !errors.Is(err, state.ErrDeploymentSuperseded) {
			t.Fatalf("late promotion of a failed rollback = %v, want ErrDeploymentSuperseded", err)
		}
		if serving, err := s.DeploymentByID(ctx, v2.ID); err != nil || serving.Status != state.DeployLive || serving.TrafficPercent != 100 {
			t.Fatalf("current release = %s at %d%% err=%v, want live at 100%%", serving.Status, serving.TrafficPercent, err)
		}
	})

	t.Run("an older CLI deploy never replaces a newer one", func(t *testing.T) {
		for _, kind := range []state.DeploymentKind{state.DeploymentKindTarball, state.DeploymentKindDockerfile} {
			appID := newApp("scope-order-" + string(kind))
			stable := scopedLive(t, ctx, s, appID, state.DefaultEnvScope, "stable")
			older := scopedBuilding(t, ctx, s, appID, kind, "older")
			newer := scopedBuilding(t, ctx, s, appID, kind, "newer")
			if err := s.MarkDeploymentLiveIfLatest(ctx, newer.ID); err != nil {
				t.Fatalf("%s newer promotion: %v", kind, err)
			}
			if err := s.MarkDeploymentLiveIfLatest(ctx, older.ID); !errors.Is(err, state.ErrDeploymentSuperseded) {
				t.Fatalf("%s older promotion after newer = %v, want ErrDeploymentSuperseded", kind, err)
			}
			if got, err := s.DeploymentByID(ctx, newer.ID); err != nil || got.Status != state.DeployLive || got.TrafficPercent != 100 {
				t.Fatalf("%s newer = %s at %d%% err=%v, want live at 100%%", kind, got.Status, got.TrafficPercent, err)
			}
			if got, err := s.DeploymentByID(ctx, stable.ID); err != nil || got.Status != state.DeploySuperseded {
				t.Fatalf("%s stable = %s err=%v, want superseded by the newer deploy", kind, got.Status, err)
			}
		}
	})
}

func scopedDeployment(t *testing.T, ctx context.Context, s state.Store, d state.Deployment, name string) state.Deployment {
	t.Helper()
	d.ID = uuid.NewString()
	if d.Kind == "" {
		d.Kind = state.DeploymentKindImage
	}
	d.ImageDigest = "sha256:" + name + "-" + d.ID
	d.RootfsKey = "apps/x/" + d.ID + ".ext4"
	d.CreatedAt = time.Now()
	created, err := s.CreateDeployment(ctx, d)
	if err != nil {
		t.Fatalf("CreateDeployment %s: %v", name, err)
	}
	return created
}

func scopedLive(t *testing.T, ctx context.Context, s state.Store, appID, scope, name string) state.Deployment {
	t.Helper()
	d := scopedDeployment(t, ctx, s, state.Deployment{AppID: appID, Scope: scope}, name)
	if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
		t.Fatalf("MarkDeploymentLive %s: %v", name, err)
	}
	return d
}

func scopedBuilding(t *testing.T, ctx context.Context, s state.Store, appID string, kind state.DeploymentKind, name string) state.Deployment {
	t.Helper()
	d := scopedDeployment(t, ctx, s, state.Deployment{AppID: appID, Scope: state.DefaultEnvScope, Kind: kind, Status: state.DeployBuilding}, name)
	if err := s.UpdateDeploymentStatus(ctx, d.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	return d
}

func scopedCanary(t *testing.T, ctx context.Context, s state.Store, appID, name string) state.Deployment {
	t.Helper()
	return scopedCanaryIn(t, ctx, s, appID, state.DefaultEnvScope, name)
}

func scopedCanaryIn(t *testing.T, ctx context.Context, s state.Store, appID, scope, name string) state.Deployment {
	t.Helper()
	d := scopedDeployment(t, ctx, s, state.Deployment{AppID: appID, Scope: scope, Status: state.DeployBuilding,
		TrafficPercent: 1, TrafficPercentExplicit: true, CanaryPreset: "balanced", CanaryTotalSteps: 4, RolloutState: "pending"}, name)
	if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
		t.Fatalf("MarkDeploymentLive canary %s: %v", name, err)
	}
	return d
}
