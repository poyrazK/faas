package state_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAlertHistoricalRollbackMem(t *testing.T) {
	alertHistoricalSuite(t, state.NewMemStore(), state.DefaultLocalNodeName, nil)
}
func TestAlertHistoricalRollbackPG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	alertHistoricalSuite(t, s, resolveDefaultLocal(t, ctx, s), pool)
}
func alertHistoricalSuite(t *testing.T, s state.Store, node string, pool *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()
	for _, service := range []bool{false, true} {
		t.Run(map[bool]string{false: "function", true: "service"}[service], func(t *testing.T) {
			acct, app, target, current := checkedRollbackFixtureContext(ctx, t, s, service)
			if pool != nil {
				current = seedHistoricalDeploymentClock(ctx, t, s, pool, current)
			}
			rule := alertRollbackRule(ctx, t, s, acct, app)
			window := 600
			rule, err := s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{PostDeployRollbackWindowSeconds: &window})
			if err != nil {
				t.Fatal(err)
			}
			if pool != nil {
				seedHistoricalRequest(ctx, t, s, acct.ID, app.ID, current.ID, time.Now().UTC().Add(-api.AlertRollbackEvidenceIngestionLag).Truncate(time.Minute).Add(-time.Minute), 500, 100)
			}
			fire := claimAlertRollback(ctx, t, s, rule)
			if fire.Status != "pending" || !fire.Historical || fire.PredecessorDeploymentID != target.ID || fire.CandidateDeploymentID != current.ID {
				t.Fatalf("selection %+v", fire)
			}
			ledger := s.(state.AlertRollbackStore)
			history := s.(state.HistoricalAlertRollbackStore)
			checked := s.(state.CheckedRollbackStore)
			if err := ledger.UpdateAlertRollback(ctx, fire, "blocked", "capacity_unavailable", nil); err != nil {
				t.Fatal(err)
			}
			fire, err = ledger.ReadAlertRollback(ctx, fire.ID)
			if err != nil || fire.Status != "blocked" {
				t.Fatalf("retryable verification failure %+v %v", fire, err)
			}
			if pool == nil {
				got, err := ledger.CommitAlertRollback(ctx, fire)
				if err != nil || got.Status != "blocked" || got.Code != "alert_rollback_telemetry_unavailable" || got.RollbackOperationID != "" {
					t.Fatalf("memory accepted unqualified alert: %+v %v", got, err)
				}
				return
			}
			var wg sync.WaitGroup
			for range 2 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					got, err := ledger.CommitAlertRollback(ctx, fire)
					if err != nil || got.RollbackOperationID != fire.ID || got.RollbackPhase != "preparing" {
						t.Errorf("intent %+v %v", got, err)
					}
				}()
			}
			wg.Wait()
			accepted, _ := ledger.ReadAlertRollback(ctx, fire.ID)
			if accepted.Status != "pending" || accepted.Code != "" || len(accepted.Blockers) != 0 {
				t.Fatalf("accepted intent retained verification failure: %+v", accepted)
			}
			if err := ledger.UpdateAlertRollback(ctx, fire, "failed", "stale", nil); err != nil {
				t.Fatal(err)
			}
			pending, err := history.RefreshHistoricalAlertRollback(ctx, accepted)
			if err != nil || pending.Status != "pending" || pending.CompletedAt != nil {
				t.Fatalf("premature completion %+v %v", pending, err)
			}
			// Accepted cleanup survives rule changes and replacement worker reads.
			off := false
			if _, err := s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{Enabled: &off}); err != nil {
				t.Fatal(err)
			}
			if err := s.MarkDeploymentLive(ctx, target.ID); err != nil {
				t.Fatal(err)
			}
			op, err := checked.GetCheckedRollback(ctx, acct.ID, app.ID, fire.ID)
			if err != nil || op.Status != "ready" {
				t.Fatalf("readiness %+v %v", op, err)
			}
			zero := int64(0)
			policy, err := s.(state.BindingReleasePolicyStore).SetBindingReleasePolicy(ctx, acct.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := checked.CommitCheckedRollback(ctx, op); !state.IsBindingReleaseRequired(err) {
				t.Fatalf("binding bypass %v", err)
			}
			finding := []api.BindingCheckFinding{{Code: "binding_verification_missing", DeploymentID: target.ID}}
			if err := checked.UpdateCheckedRollback(ctx, op, "blocked", "binding_verification_missing", finding); err != nil {
				t.Fatal(err)
			}
			blocked, err := history.RefreshHistoricalAlertRollback(ctx, accepted)
			if err != nil || blocked.Status != "blocked" || len(blocked.Blockers) != 1 {
				t.Fatalf("blockers %+v %v", blocked, err)
			}
			op, _ = checked.GetCheckedRollback(ctx, acct.ID, app.ID, fire.ID)
			fence := bindingRecoveryFence(ctx, t, s.(state.BindingPromotionStore), acct, app, target)
			fence.PolicyRevision = policy.Revision
			fence.MaxVerificationAge = api.DefaultBindingVerificationAge
			expired := fence
			expired.ValidUntil = time.Now().Add(-time.Second)
			if _, err := checked.CommitCheckedRollback(state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{expired}), op); !errors.Is(err, state.ErrBindingPromotionExpired) {
				t.Fatalf("expired grant %v", err)
			}
			if service {
				for range 2 {
					if _, err := s.CreateInstanceWithMode(ctx, app.ID, target.ID, "running", 128, node, uuid.NewString(), "service"); err != nil {
						t.Fatal(err)
					}
				}
			}
			fence = bindingRecoveryFence(ctx, t, s.(state.BindingPromotionStore), acct, app, target)
			fence.PolicyRevision, fence.MaxVerificationAge = policy.Revision, api.DefaultBindingVerificationAge
			routed, err := checked.CommitCheckedRollback(state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{fence}), op)
			if err != nil {
				t.Fatal(err)
			}
			if service {
				progress, err := history.RefreshHistoricalAlertRollback(ctx, blocked)
				if err != nil || progress.Status != "pending" || progress.RollbackPhase != "routing" {
					t.Fatalf("routing %+v %v", progress, err)
				}
				d, _ := s.DeploymentByID(ctx, target.ID)
				h := d.ServiceRolloutHandoff
				now := time.Now().UTC()
				h.Phase = "draining"
				h.AcknowledgedAt = &now
				if _, err := s.UpdateServiceRolloutHandoff(ctx, target.ID, h); err != nil {
					t.Fatal(err)
				}
				progress, err = history.RefreshHistoricalAlertRollback(ctx, progress)
				if err != nil || progress.Status != "pending" || progress.CompletedAt != nil {
					t.Fatalf("draining %+v %v", progress, err)
				}
				if _, err := s.FinalizeServiceRollout(ctx, target.ID); err != nil {
					t.Fatal(err)
				}
				if err := checked.UpdateCheckedRollback(ctx, routed, "complete", "", nil); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					got, err := history.RefreshHistoricalAlertRollback(ctx, blocked)
					if err != nil || got.Status != "complete" || got.RollbackRoutingAuditID == "" || got.CompletedAt == nil || got.AuditID == "" {
						t.Errorf("complete %+v %v", got, err)
					}
				}()
			}
			wg.Wait()
			audits, err := s.ListDeploymentAuditByAlertRule(ctx, rule.ID, 100)
			if err != nil || len(audits) != 2 {
				t.Fatalf("attribution %+v %v", audits, err)
			}
			// The restored deployment cannot become the next automatic rollback's candidate.
			rule = alertRollbackRule(ctx, t, s, acct, app)
			rule, err = s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{PostDeployRollbackWindowSeconds: &window})
			if err != nil {
				t.Fatal(err)
			}
			chained := claimAlertRollback(ctx, t, s, rule)
			if chained.Status != "failed" || chained.Code != "alert_rollback_chain_prevented" {
				t.Fatalf("rollback chain %+v", chained)
			}
		})
	}
	t.Run("window opt in and immutable lineage", func(t *testing.T) {
		acct, app, target, current := checkedRollbackFixtureContext(ctx, t, s, false)
		rule := alertRollbackRule(ctx, t, s, acct, app)
		if fire := claimAlertRollback(ctx, t, s, rule); fire.Status != "failed" {
			t.Fatalf("default expanded automation %+v", fire)
		}
		rule = alertRollbackRule(ctx, t, s, acct, app)
		window := 1
		rule, err := s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{PostDeployRollbackWindowSeconds: &window})
		if err != nil {
			t.Fatal(err)
		}
		// A supplied fire timestamp outside the window must fail closed at capture.
		id, won, err := s.ClaimAlertFire(ctx, rule.ID, rule.ID+":expired", nil, 42, current.RolloutCompletedAt.Add(2*time.Second))
		if err != nil || !won {
			t.Fatalf("claim %v %v", won, err)
		}
		fire, _ := s.(state.AlertRollbackStore).ReadAlertRollback(ctx, id)
		if fire.Code != "alert_rollback_window_expired" {
			t.Fatalf("expired %+v", fire)
		}
		rule = alertRollbackRule(ctx, t, s, acct, app)
		window = 600
		rule, err = s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{PostDeployRollbackWindowSeconds: &window})
		if err != nil {
			t.Fatal(err)
		}
		fire = claimAlertRollback(ctx, t, s, rule)
		newer, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: current.ImageDigest, Status: state.DeployPending, TrafficPercent: 100})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetDeploymentRootfs(ctx, newer.ID, "/test/new", "test/new", 4096); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkDeploymentLive(ctx, newer.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.(state.AlertRollbackStore).CommitAlertRollback(ctx, fire); !errors.Is(err, state.ErrAlertRollbackChanged) {
			t.Fatalf("reselected %v", err)
		}
		same, _ := s.(state.AlertRollbackStore).ReadAlertRollback(ctx, fire.ID)
		if same.PredecessorDeploymentID != target.ID || same.CandidateDeploymentID != current.ID {
			t.Fatalf("changed pair %+v", same)
		}
	})
}
