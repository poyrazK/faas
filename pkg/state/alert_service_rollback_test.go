package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAlertServiceRollbackMem(t *testing.T) {
	s := state.NewMemStore()
	alertServiceRollbackSuite(t, s, state.DefaultLocalNodeName, func(ctx context.Context, id string) error {
		return s.SetDeploymentCanaryState(ctx, id, "none", 0, 0, time.Now(), "rolling_out")
	})
}

func TestAlertServiceRollbackPG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	alertServiceRollbackSuite(t, s, resolveDefaultLocal(t, ctx, s), func(ctx context.Context, id string) error {
		_, err := pool.Exec(ctx, `UPDATE deployments SET canary_total_steps=0,canary_step=0,rollout_state='rolling_out' WHERE id=$1`, id)
		return err
	})
}

func alertServiceRollbackFixture(ctx context.Context, t *testing.T, s state.Store, mark func(context.Context, string) error) (state.Account, state.App, state.Deployment, state.Deployment) {
	t.Helper()
	acct, app, predecessor, candidate := bindingPromotionFixtureCtx(ctx, t, s)
	manifest := app.Manifest
	manifest.ExecutionMode = api.ExecutionModeService
	var err error
	app, err = s.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	if err := mark(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginServiceRolloutCutover(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	return acct, app, predecessor, candidate
}

func alertServiceRollbackSuite(t *testing.T, s state.Store, node string, mark func(context.Context, string) error) {
	t.Helper()
	ctx := t.Context()
	store := s.(state.AlertRollbackStore)
	refresh := s.(state.ServiceAlertRollbackStore)
	t.Run("pinned zero weight recipient and acknowledged completion", func(t *testing.T) {
		acct, app, predecessor, candidate := alertServiceRollbackFixture(ctx, t, s, mark)
		rule := alertRollbackRule(ctx, t, s, acct, app)
		fire := claimAlertRollback(ctx, t, s, rule)
		if !fire.Service || fire.Status != "pending" || fire.PredecessorDeploymentID != predecessor.ID || fire.CandidateDeploymentID != candidate.ID {
			t.Fatalf("service fire not pinned %+v", fire)
		}
		var wg sync.WaitGroup
		receipts := make(chan api.AlertRollback, 2)
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := store.CommitAlertRollback(ctx, fire)
				if err != nil {
					t.Error(err)
				}
				receipts <- got
			}()
		}
		wg.Wait()
		close(receipts)
		var requested api.AlertRollback
		for got := range receipts {
			if got.ServiceRequestID != fire.ID || got.Status != "pending" || got.CompletedAt != nil || got.ServicePhase != "pending" {
				t.Fatalf("accepted as complete %+v", got)
			}
			requested = got
		}
		if err := store.UpdateAlertRollback(ctx, fire, "failed", "stale_rule_read", nil); err != nil {
			t.Fatal(err)
		}
		zero := int64(0)
		policy, err := s.(state.BindingReleasePolicyStore).SetBindingReleasePolicy(ctx, acct.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero})
		if err != nil {
			t.Fatal(err)
		}
		grant := func(parentCtx context.Context, recipient state.Deployment) context.Context {
			fence := bindingRecoveryFence(parentCtx, t, s.(state.BindingPromotionStore), acct, app, recipient)
			fence.PolicyRevision, fence.MaxVerificationAge = policy.Revision, api.DefaultBindingVerificationAge
			return state.WithServiceRolloutBindingRequest(state.WithBindingReleaseFences(parentCtx, []state.BindingPromotionFence{fence}), fire.ID)
		}
		if _, err := s.BeginServiceRolloutAbort(grant(ctx, candidate), candidate.ID); !state.IsBindingReleaseRequired(err) {
			t.Fatalf("candidate evidence authorized recipient: %v", err)
		}
		if _, err := s.BeginServiceRolloutAbort(grant(ctx, predecessor), candidate.ID); !errors.Is(err, state.ErrServiceRolloutNotReady) {
			t.Fatalf("missing capacity bypassed: %v", err)
		}
		if err := s.(state.ServiceRolloutBindingStore).UpdateServiceRolloutBindingStatus(ctx, candidate.ID, fire.ID, "service_rollout_not_ready", []api.BindingCheckFinding{{Code: "service_rollout_not_ready", DeploymentID: predecessor.ID}}); err != nil {
			t.Fatal(err)
		}
		blocked, err := refresh.RefreshServiceAlertRollback(ctx, requested)
		if err != nil || blocked.Status != "blocked" || len(blocked.Blockers) != 1 || blocked.ServiceRequestID != fire.ID {
			t.Fatalf("blocked projection %+v %v", blocked, err)
		}
		if _, err := s.CreateInstanceWithMode(ctx, app.ID, predecessor.ID, "running", 512, node, uuid.NewString(), "service"); err != nil {
			t.Fatal(err)
		}
		routed, err := s.BeginServiceRolloutAbort(grant(ctx, predecessor), candidate.ID)
		if err != nil {
			t.Fatal(err)
		}
		pending, err := refresh.RefreshServiceAlertRollback(ctx, blocked)
		if err != nil || pending.Status != "pending" || pending.ServicePhase != "routing" || pending.ServiceRoutingAuditID == "" || pending.CompletedAt != nil {
			t.Fatalf("routing mistaken for completion %+v %v", pending, err)
		}
		// Disabling a rule cannot strand cleanup of already committed intent.
		enabled := false
		if _, err := s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{Enabled: &enabled}); err != nil {
			t.Fatal(err)
		}
		h := routed.ServiceRolloutHandoff
		now := time.Now().UTC()
		h.Phase, h.AcknowledgedAt = "draining", &now
		if _, err := s.UpdateServiceRolloutHandoff(ctx, candidate.ID, h); err != nil {
			t.Fatal(err)
		}
		pending, err = refresh.RefreshServiceAlertRollback(ctx, pending)
		if err != nil || pending.Status != "pending" || pending.ServicePhase != "draining" {
			t.Fatalf("drain mistaken for completion %+v %v", pending, err)
		}
		if err := s.UpsertAppEnv(ctx, acct.ID, app.ID, "POST_ROUTING_ROTATION", "changed"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AbortServiceRollout(ctx, candidate.ID, fire.Reason); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				complete, err := refresh.RefreshServiceAlertRollback(ctx, pending)
				if err != nil || complete.Status != "complete" || complete.ServicePhase != "complete" || complete.CompletedAt == nil || complete.AuditID == requested.AuditID {
					t.Errorf("completion %+v %v", complete, err)
				}
			}()
		}
		wg.Wait()
		audits, err := s.ListDeploymentAuditByAlertRule(ctx, rule.ID, 100)
		if err != nil || len(audits) != 2 {
			t.Fatalf("duplicate or unattributed audit %+v %v", audits, err)
		}
		for _, audit := range audits {
			var data map[string]any
			if json.Unmarshal(audit.Data, &data) != nil || data["alert_fire_id"] != fire.ID || data["service_request_id"] != fire.ID {
				t.Fatalf("missing fire attribution %s", audit.Data)
			}
		}
	})
	for _, scenario := range []string{"disabled rule", "completed candidate", "missing predecessor", "manual abort"} {
		t.Run(scenario, func(t *testing.T) {
			acct, app, predecessor, candidate := alertServiceRollbackFixture(ctx, t, s, mark)
			rule := alertRollbackRule(ctx, t, s, acct, app)
			fire := claimAlertRollback(ctx, t, s, rule)
			switch scenario {
			case "disabled rule":
				enabled := false
				_, _ = s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{Enabled: &enabled})
			case "completed candidate":
				_, _ = s.FinalizeServiceRollout(ctx, candidate.ID)
			case "missing predecessor":
				_ = s.UpdateDeploymentStatus(ctx, predecessor.ID, state.DeploySuperseded, "")
			case "manual abort":
				_, _, _ = s.(state.ServiceRolloutBindingStore).RequestServiceRolloutAbort(ctx, app.ID, candidate.ID, predecessor.ID, "operator abort")
			}
			if _, err := store.CommitAlertRollback(ctx, fire); !errors.Is(err, state.ErrAlertRollbackChanged) {
				t.Fatalf("changed target accepted: %v", err)
			}
		})
	}
	t.Run("replacement request cannot complete the earlier fire", func(t *testing.T) {
		acct, app, predecessor, candidate := alertServiceRollbackFixture(ctx, t, s, mark)
		rule := alertRollbackRule(ctx, t, s, acct, app)
		fire := claimAlertRollback(ctx, t, s, rule)
		requested, err := store.CommitAlertRollback(ctx, fire)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateInstanceWithMode(ctx, app.ID, predecessor.ID, "running", 512, node, uuid.NewString(), "service"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.BeginServiceRolloutAbort(state.WithServiceRolloutBindingRequest(ctx, fire.ID), candidate.ID); err != nil {
			t.Fatal(err)
		}
		manual, _, err := s.(state.ServiceRolloutBindingStore).RequestServiceRolloutAbort(ctx, app.ID, candidate.ID, predecessor.ID, "new manual request")
		if err != nil || manual.ServiceRolloutHandoff.BindingsCheck.RequestID == fire.ID {
			t.Fatalf("replacement request %+v %v", manual, err)
		}
		failed, err := refresh.RefreshServiceAlertRollback(ctx, requested)
		if err != nil || failed.Status != "failed" || failed.ServiceRequestID != fire.ID || failed.CompletedAt != nil {
			t.Fatalf("earlier fire adopted replacement %+v %v", failed, err)
		}
	})
}
