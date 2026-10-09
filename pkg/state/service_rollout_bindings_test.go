package state_test

// adr: 600 — checked service routing with durable exact requests.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceRolloutBindingGateMem(t *testing.T) {
	s := state.NewMemStore()
	serviceRolloutBindingSuite(t, s, state.DefaultLocalNodeName, func(ctx context.Context, id string) error {
		return s.SetDeploymentCanaryState(ctx, id, "none", 0, 0, time.Now(), "rolling_out")
	})
}

func TestServiceRolloutBindingGatePG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	serviceRolloutBindingSuite(t, s, resolveDefaultLocal(t, ctx, s), func(ctx context.Context, id string) error {
		_, err := pool.Exec(ctx, `UPDATE deployments SET canary_total_steps=0,canary_step=0,rollout_state='rolling_out' WHERE id=$1`, id)
		return err
	})
}

func serviceRolloutBindingSuite(t *testing.T, s state.Store, nodeID string, mark func(context.Context, string) error) {
	t.Helper()
	ctx := t.Context()
	a, app, predecessor, candidate := bindingPromotionFixture(t, s)
	if err := mark(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	policies := s.(state.BindingReleasePolicyStore)
	if _, err := policies.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	ready, err := s.CreateInstanceWithMode(ctx, app.ID, candidate.ID, "running", 512, nodeID, uuid.NewString(), "service")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateInstanceWithMode(ctx, app.ID, predecessor.ID, "running", 512, nodeID, uuid.NewString(), "service"); err != nil {
		t.Fatal(err)
	}
	queued, err := s.BeginServiceRolloutCutover(ctx, candidate.ID)
	if !state.IsBindingReleaseRequired(err) || queued.ServiceRolloutHandoff.BindingsCheck == nil || queued.ServiceRolloutHandoff.PredecessorDeploymentID != predecessor.ID {
		t.Fatalf("missing durable request: %+v %v", queued, err)
	}
	requestID := queued.ServiceRolloutHandoff.BindingsCheck.RequestID
	assertWeights := func(candidateWeight int) {
		t.Helper()
		for id, want := range map[string]int{candidate.ID: candidateWeight, predecessor.ID: 100 - candidateWeight} {
			d, err := s.DeploymentByID(ctx, id)
			if err != nil || d.TrafficPercent != want || d.Status != state.DeployLive {
				t.Fatalf("wrong routing: %+v %v; want live/%d", d, err, want)
			}
		}
	}
	assertWeights(0)
	writeContext := func(recipient state.Deployment, request string) context.Context {
		f := bindingRecoveryFence(ctx, t, s.(state.BindingPromotionStore), a, app, recipient)
		f.PolicyRevision, f.MaxVerificationAge = 1, api.DefaultBindingVerificationAge
		return state.WithServiceRolloutBindingRequest(state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{f}), request)
	}
	if _, err := s.BeginServiceRolloutCutover(writeContext(candidate, uuid.NewString()), candidate.ID); !errors.Is(err, state.ErrServiceRolloutInvalid) {
		t.Fatalf("wrong request accepted: %v", err)
	}
	if err := s.UpdateInstanceState(ctx, ready.ID, "stopped"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginServiceRolloutCutover(writeContext(candidate, requestID), candidate.ID); !errors.Is(err, state.ErrServiceRolloutNotReady) {
		t.Fatalf("lost readiness accepted: %v", err)
	}
	assertWeights(0)
	if err := s.UpdateInstanceState(ctx, ready.ID, "running"); err != nil {
		t.Fatal(err)
	}
	stale := writeContext(candidate, requestID)
	if err := s.UpsertAppEnv(ctx, a.ID, app.ID, "SERVICE_GATE", "changed"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginServiceRolloutCutover(stale, candidate.ID); !errors.Is(err, state.ErrBindingPromotionChanged) {
		t.Fatalf("changed bindings accepted: %v", err)
	}
	assertWeights(0)
	routed, err := s.BeginServiceRolloutCutover(writeContext(candidate, requestID), candidate.ID)
	if err != nil || routed.ServiceRolloutHandoff.BindingsCheck.Status != "passed" || routed.ServiceRolloutHandoff.BindingsCheck.AuditID == "" {
		t.Fatalf("checked routing: %+v %v", routed, err)
	}
	assertWeights(100)
	// A stale scheduler projection must not erase APID's check/audit receipt.
	if _, err := s.UpdateServiceRolloutHandoff(ctx, candidate.ID, queued.ServiceRolloutHandoff); err != nil {
		t.Fatal(err)
	}
	after, err := s.DeploymentByID(ctx, candidate.ID)
	if err != nil || after.ServiceRolloutHandoff.BindingsCheck.Status != "passed" {
		t.Fatalf("scheduler overwrote check: %+v %v", after, err)
	}
	requests := s.(state.ServiceRolloutBindingStore)
	if err := requests.UpdateServiceRolloutBindingStatus(ctx, candidate.ID, requestID, "stale", nil); !errors.Is(err, state.ErrServiceRolloutInvalid) {
		t.Fatalf("late blocker overwrote commit: %v", err)
	}
	abort, _, err := requests.RequestServiceRolloutAbort(ctx, app.ID, candidate.ID, predecessor.ID, "operator stop")
	if err != nil || abort.ServiceRolloutHandoff.BindingsCheck.RequestID == requestID {
		t.Fatalf("reverse request: %+v %v", abort, err)
	}
	abortRequest := abort.ServiceRolloutHandoff.BindingsCheck.RequestID
	if _, err := s.BeginServiceRolloutAbort(ctx, candidate.ID); !state.IsBindingReleaseRequired(err) {
		t.Fatalf("scheduler bypassed reverse check: %v", err)
	}
	assertWeights(100)
	if _, err := s.BeginServiceRolloutAbort(writeContext(candidate, abortRequest), candidate.ID); !state.IsBindingReleaseRequired(err) {
		t.Fatalf("candidate evidence authorized predecessor: %v", err)
	}
	assertWeights(100)
	if _, err := s.BeginServiceRolloutAbort(writeContext(predecessor, abortRequest), candidate.ID); err != nil {
		t.Fatal(err)
	}
	assertWeights(0)
	// Final drain cleanup requires no traffic grant: expiry or rotation after
	// publishing weights must not strand terminal cleanup behind old evidence.
	if err := s.UpsertAppEnv(ctx, a.ID, app.ID, "SERVICE_GATE", "rotated after routing"); err != nil {
		t.Fatal(err)
	}
	finished, err := s.AbortServiceRollout(ctx, candidate.ID, "operator stop")
	if err != nil || finished.RolloutState != "aborted" || finished.ServiceRolloutHandoff.Phase != "complete" {
		t.Fatalf("cleanup required stale grant: %+v %v", finished, err)
	}
	audits, err := s.ListDeploymentAudit(ctx, candidate.ID, 20)
	if err != nil || len(audits) != 3 {
		t.Fatalf("routing and intent audit count: %d %v", len(audits), err)
	}
}

// Worker observations must survive the actual app lock boundary, not just an
// API-side read. A changed direction and removed pin may never select fallback.
func TestServiceRolloutBindingPGLockRaces(t *testing.T) {
	for _, scenario := range []string{"expired", "policy changed", "predecessor removed", "abort requested"} {
		t.Run(scenario, func(t *testing.T) {
			store, pool, _ := pgStoreWithPool(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			account, app, predecessor, candidate := bindingPromotionFixture(t, store)
			if _, err := pool.Exec(ctx, `UPDATE deployments SET canary_total_steps=0,canary_step=0,rollout_state='rolling_out' WHERE id=$1`, candidate.ID); err != nil {
				t.Fatal(err)
			}
			zero := int64(0)
			if _, err := store.SetBindingReleasePolicy(ctx, account.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateInstanceWithMode(ctx, app.ID, candidate.ID, "running", 512, resolveDefaultLocal(t, ctx, store), uuid.NewString(), "service"); err != nil {
				t.Fatal(err)
			}
			queued, err := store.BeginServiceRolloutCutover(ctx, candidate.ID)
			if !state.IsBindingReleaseRequired(err) {
				t.Fatalf("queue: %v", err)
			}
			fence := bindingRecoveryFence(ctx, t, store, account, app, candidate)
			fence.PolicyRevision, fence.MaxVerificationAge = 1, api.DefaultBindingVerificationAge
			requestID := queued.ServiceRolloutHandoff.BindingsCheck.RequestID
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := tx.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, app.ID); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "expired":
				fence.ValidUntil = time.Now().Add(100 * time.Millisecond)
			case "policy changed":
				_, err = tx.Exec(ctx, `UPDATE app_binding_release_policies SET revision=revision+1,max_age_seconds=30 WHERE app_id=$1 AND scope='default'`, app.ID)
			case "predecessor removed":
				_, err = tx.Exec(ctx, `UPDATE deployments SET status='superseded',traffic_percent=0 WHERE id=$1`, predecessor.ID)
			case "abort requested":
				_, err = tx.Exec(ctx, `UPDATE deployments SET service_rollout_handoff=jsonb_build_object('action','abort','phase','pending','predecessor_deployment_id',$2::text,'bindings_check',jsonb_build_object('request_id',$3::text,'action','abort','deployment_id',$2::text,'status','pending')) WHERE id=$1`, candidate.ID, predecessor.ID, uuid.NewString())
			}
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				writeCtx := state.WithServiceRolloutBindingRequest(state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{fence}), requestID)
				_, err := store.BeginServiceRolloutCutover(writeCtx, candidate.ID)
				done <- err
			}()
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND (query LIKE '%select 1 from apps where id = $1 for update%' OR query LIKE '%LockRoutePolicyApp%' OR query LIKE '%LockRoutePolicyAccount%'))`).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("worker did not wait: %v", err)
				default:
				}
				select {
				case <-ctx.Done():
					t.Fatal("account/app policy lock never observed")
				case <-time.After(10 * time.Millisecond):
				}
			}
			if scenario == "expired" {
				timer := time.NewTimer(time.Until(fence.ValidUntil) + time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					t.Fatal(ctx.Err())
				case <-timer.C:
				}
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			want := state.ErrBindingPromotionChanged
			if scenario == "expired" {
				want = state.ErrBindingPromotionExpired
			}
			if scenario == "predecessor removed" || scenario == "abort requested" {
				want = state.ErrServiceRolloutInvalid
			}
			if err := <-done; !errors.Is(err, want) {
				t.Fatalf("stale worker admitted: %v want %v", err, want)
			}
			row, err := store.DeploymentByID(ctx, candidate.ID)
			if err != nil || row.TrafficPercent != 0 || row.RolloutState != "rolling_out" {
				t.Fatalf("mutated routing: %+v %v", row, err)
			}
			audits, err := store.ListDeploymentAudit(ctx, candidate.ID, 20)
			if err != nil || len(audits) != 0 {
				t.Fatalf("rejected routing audited: %v %v", audits, err)
			}
		})
	}
}
