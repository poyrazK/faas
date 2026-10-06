package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAlertRollbackMem(t *testing.T) { alertRollbackSuite(t, state.NewMemStore()) }
func TestAlertRollbackPG(t *testing.T)  { s, _ := pgStore(t); alertRollbackSuite(t, s) }

func alertRollbackRule(ctx context.Context, t *testing.T, s state.Store, acct state.Account, app state.App) state.AlertRule {
	t.Helper()
	rule, err := s.CreateAlertRule(ctx, state.AlertRule{AccountID: acct.ID, AppID: app.ID, Name: "rollback-" + uuid.NewString(), Enabled: true, Metric: state.AlertMetricErrorRate, Comparison: state.AlertGt, Threshold: 1, WindowSpec: state.AlertWindow5m, Action: state.AlertActionRollback, WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("sealed"), CooldownMinutes: 15})
	if err != nil {
		t.Fatal(err)
	}
	return rule
}
func claimAlertRollback(ctx context.Context, t *testing.T, s state.Store, rule state.AlertRule) api.AlertRollback {
	t.Helper()
	id, won, err := s.ClaimAlertFire(ctx, rule.ID, rule.ID+":fire", []byte(`{"metric":"error_rate_pct"}`), 42, time.Now().UTC())
	if err != nil || !won {
		t.Fatalf("claim %s %v %v", id, won, err)
	}
	r, err := s.(state.AlertRollbackStore).ReadAlertRollback(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func alertRollbackSuite(t *testing.T, s state.Store) {
	t.Helper()
	ctx := context.Background()
	store := s.(state.AlertRollbackStore)
	t.Run("nonfinite observations cannot corrupt intent", func(t *testing.T) {
		acct, app, _, _ := bindingReleaseRecoveryFixtureCtx(ctx, t, s)
		rule := alertRollbackRule(ctx, t, s, acct, app)
		for _, observed := range []float64{math.Inf(1), math.NaN()} {
			id, won, err := s.ClaimAlertFire(ctx, rule.ID, rule.ID+":invalid", nil, observed, time.Now())
			if !errors.Is(err, state.ErrInvalidArgument) || won || id != "" {
				t.Fatalf("nonfinite fire %s %v %v", id, won, err)
			}
		}
		deliveries, err := s.ListAlertDeliveriesForRule(ctx, rule.ID, 100, false)
		if err != nil || len(deliveries) != 0 {
			t.Fatalf("partial fire %+v %v", deliveries, err)
		}
		rows, err := store.ListAlertRollbacks(ctx, acct.ID, app.ID)
		if err != nil || len(rows) != 0 {
			t.Fatalf("partial action %+v %v", rows, err)
		}
	})
	t.Run("atomic fire and concurrent completion", func(t *testing.T) {
		acct, app, predecessor, candidate := bindingReleaseRecoveryFixtureCtx(ctx, t, s)
		rule := alertRollbackRule(ctx, t, s, acct, app)
		r := claimAlertRollback(ctx, t, s, rule)
		if r.Status != "pending" || r.CandidateDeploymentID != candidate.ID || r.PredecessorDeploymentID != predecessor.ID {
			t.Fatalf("unpinned fire %+v", r)
		}
		_, won, err := s.ClaimAlertFire(ctx, rule.ID, rule.ID+":fire", nil, 45, time.Now().Add(time.Second))
		if err != nil || won {
			t.Fatalf("duplicate fire %v %v", won, err)
		}
		list, err := store.ListAlertRollbacks(ctx, acct.ID, app.ID)
		if err != nil || len(list) != 1 {
			t.Fatalf("duplicate outbox %+v %v", list, err)
		}
		if _, err := store.GetAlertRollback(ctx, uuid.NewString(), app.ID, r.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("foreign receipt: %v", err)
		}
		var wg sync.WaitGroup
		results := make(chan api.AlertRollback, 2)
		errs := make(chan error, 2)
		for range 2 {
			wg.Add(1)
			go func() { defer wg.Done(); got, err := store.CommitAlertRollback(ctx, r); results <- got; errs <- err }()
		}
		wg.Wait()
		close(results)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		for got := range results {
			if got.Status != "complete" || got.AuditID == "" || got.CompletedAt == nil {
				t.Fatalf("incomplete receipt %+v", got)
			}
		}
		if err := store.UpdateAlertRollback(ctx, r, "blocked", "stale_worker", nil); err != nil {
			t.Fatal(err)
		}
		got, err := store.ReadAlertRollback(ctx, r.ID)
		if err != nil || got.Status != "complete" || got.Code != "" {
			t.Fatalf("late blocker %+v %v", got, err)
		}
		audits, err := s.ListDeploymentAuditByAlertRule(ctx, rule.ID, 100)
		if err != nil || len(audits) != 1 || audits[0].Actor != "apid:alert_rollback" {
			t.Fatalf("attribution %+v %v", audits, err)
		}
		var data map[string]any
		if json.Unmarshal(audits[0].Data, &data) != nil || data["alert_fire_id"] != r.ID {
			t.Fatalf("fire attribution %s", audits[0].Data)
		}
		for id, pct := range map[string]int{candidate.ID: 0, predecessor.ID: 100} {
			d, err := s.DeploymentByID(ctx, id)
			if err != nil || d.TrafficPercent != pct {
				t.Fatalf("traffic %+v %v", d, err)
			}
		}
	})
	t.Run("binding fences remain fresh", func(t *testing.T) {
		acct, app, predecessor, candidate := bindingReleaseRecoveryFixtureCtx(ctx, t, s)
		rule := alertRollbackRule(ctx, t, s, acct, app)
		r := claimAlertRollback(ctx, t, s, rule)
		zero := int64(0)
		policy, err := s.(state.BindingReleasePolicyStore).SetBindingReleasePolicy(ctx, acct.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CommitAlertRollback(ctx, r); !state.IsBindingReleaseRequired(err) {
			t.Fatalf("unguarded action %v", err)
		}
		fence := bindingRecoveryFence(ctx, t, s.(state.BindingPromotionStore), acct, app, predecessor)
		fence.PolicyRevision, fence.MaxVerificationAge = policy.Revision, api.DefaultBindingVerificationAge
		expired := fence
		expired.ValidUntil = time.Now().Add(-time.Second)
		if _, err := store.CommitAlertRollback(state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{expired}), r); !errors.Is(err, state.ErrBindingPromotionExpired) {
			t.Fatalf("expired evidence %v", err)
		}
		if err := s.UpsertAppEnv(ctx, acct.ID, app.ID, "ROLLBACK_CHECK", "changed"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CommitAlertRollback(state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{fence}), r); !errors.Is(err, state.ErrBindingPromotionChanged) {
			t.Fatalf("stale evidence %v", err)
		}
		if err := store.UpdateAlertRollback(ctx, r, "blocked", "binding_verification_missing", []api.BindingCheckFinding{{Code: "binding_verification_missing", DeploymentID: predecessor.ID, Message: "verify retained predecessor"}}); err != nil {
			t.Fatal(err)
		}
		blocked, _ := store.ReadAlertRollback(ctx, r.ID)
		if blocked.Status != "blocked" || blocked.CandidateDeploymentID != candidate.ID || blocked.PredecessorDeploymentID != predecessor.ID {
			t.Fatalf("retargeted %+v", blocked)
		}
		fence = bindingRecoveryFence(ctx, t, s.(state.BindingPromotionStore), acct, app, predecessor)
		fence.PolicyRevision, fence.MaxVerificationAge = policy.Revision, api.DefaultBindingVerificationAge
		got, err := store.CommitAlertRollback(state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{fence}), blocked)
		if err != nil || got.Status != "complete" {
			t.Fatalf("fresh retry %+v %v", got, err)
		}
	})
	for _, scenario := range []string{"changed release", "disabled rule"} {
		t.Run(scenario, func(t *testing.T) {
			acct, app, _, _ := bindingReleaseRecoveryFixtureCtx(ctx, t, s)
			rule := alertRollbackRule(ctx, t, s, acct, app)
			r := claimAlertRollback(ctx, t, s, rule)
			if scenario == "changed release" {
				candidate, err := s.DeploymentByID(ctx, r.CandidateDeploymentID)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := s.(state.CanaryAdvancer).AdvanceCanary(ctx, candidate.ID, state.CanaryAdvanceParams{ExpectedStep: candidate.CanaryStep, TrafficPercent: 100, Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "test:manual_completion"}}); err != nil {
					t.Fatal(err)
				}
			} else {
				enabled := false
				if _, err := s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{Enabled: &enabled}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.CommitAlertRollback(ctx, r); !errors.Is(err, state.ErrAlertRollbackChanged) {
				t.Fatalf("changed selection %v", err)
			}
			got, _ := store.ReadAlertRollback(ctx, r.ID)
			if got.CandidateDeploymentID != r.CandidateDeploymentID || got.PredecessorDeploymentID != r.PredecessorDeploymentID || got.Status == "complete" {
				t.Fatalf("retargeted %+v", got)
			}
		})
	}
	t.Run("no target cannot select later", func(t *testing.T) {
		acct, app, _, _ := bindingPromotionFixtureCtx(ctx, t, s)
		rule := alertRollbackRule(ctx, t, s, acct, app)
		r := claimAlertRollback(ctx, t, s, rule)
		if r.Status != "failed" || r.Code != "alert_rollback_target_unavailable" {
			t.Fatalf("missing target %+v", r)
		}
		got, err := store.CommitAlertRollback(ctx, r)
		if err != nil || got.Status != "failed" {
			t.Fatalf("terminal target %+v %v", got, err)
		}
		if err := s.DeleteAlertRule(ctx, rule.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ReadAlertRollback(ctx, r.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("deleted rule retains action %v", err)
		}
	})
}
