package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type bindingRecoveryStore interface {
	RecoverRolloutForDeployment(context.Context, string, string, string, string, string) (state.Deployment, int64, error)
}

func bindingRecoveryFence(ctx context.Context, t *testing.T, s state.BindingPromotionStore, a state.Account, app state.App, target state.Deployment) state.BindingPromotionFence {
	t.Helper()
	revision, err := s.ReadBindingPromotionRevision(ctx, a.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	return state.BindingPromotionFence{AccountID: a.ID, AppID: app.ID, DeploymentID: target.ID, Scope: target.Scope, Revision: revision, ValidUntil: time.Now().Add(time.Minute)}
}

func TestBindingReleaseRecoveryMem(t *testing.T) { bindingReleaseRecoverySuite(t, state.NewMemStore()) }
func TestBindingReleaseRecoveryPG(t *testing.T) {
	s, _ := pgStore(t)
	bindingReleaseRecoverySuite(t, s)
}

func bindingReleaseRecoveryFixture(t *testing.T, s state.Store) (state.Account, state.App, state.Deployment, state.Deployment) {
	t.Helper()
	return bindingReleaseRecoveryFixtureCtx(context.Background(), t, s)
}

func bindingReleaseRecoveryFixtureCtx(ctx context.Context, t *testing.T, s state.Store) (state.Account, state.App, state.Deployment, state.Deployment) {
	t.Helper()
	a, app, predecessor, _ := bindingPromotionFixtureCtx(ctx, t, s)
	started := time.Now().Add(-time.Minute)
	candidate, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "default", TrafficPercent: 25, TrafficPercentExplicit: true, ImageDigest: predecessor.ImageDigest, CanaryPreset: "balanced", CanaryStep: 1, CanaryTotalSteps: 4, CanaryStepStartedAt: &started, RolloutState: "rolling_out"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, candidate.ID, "/test/"+candidate.ID, "test/"+candidate.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDeploymentStatus(ctx, candidate.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.(state.CanaryAdvancer).AdvanceCanary(ctx, candidate.ID, state.CanaryAdvanceParams{ExpectedStep: 1, TrafficPercent: 25, Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "test:canary"}}); err != nil {
		t.Fatal(err)
	}
	return a, app, predecessor, candidate
}

func bindingReleaseRecoverySuite(t *testing.T, s state.Store) {
	t.Helper()
	ctx := context.Background()
	a, app, predecessor, candidate := bindingReleaseRecoveryFixture(t, s)
	policies := s.(state.BindingReleasePolicyStore)
	zero := int64(0)
	if _, err := policies.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	recovery := s.(bindingRecoveryStore)
	auditsBefore, err := s.ListDeploymentAudit(ctx, candidate.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	unchanged := func() {
		t.Helper()
		for id, percent := range map[string]int{candidate.ID: 25, predecessor.ID: 75} {
			d, err := s.DeploymentByID(ctx, id)
			if err != nil || d.TrafficPercent != percent || id == candidate.ID && d.RolloutState != "rolling_out" {
				t.Fatalf("blocked recovery mutated deployment: %+v %v", d, err)
			}
		}
		audits, err := s.ListDeploymentAudit(ctx, candidate.ID, 100)
		if err != nil || len(audits) != len(auditsBefore) {
			t.Fatalf("blocked recovery wrote audit: %d %v", len(audits), err)
		}
	}
	fence := func(target state.Deployment) state.BindingPromotionFence {
		f := bindingRecoveryFence(ctx, t, s.(state.BindingPromotionStore), a, app, target)
		p, err := policies.GetBindingReleasePolicy(ctx, a.ID, app.ID, "default")
		if err != nil {
			t.Fatal(err)
		}
		f.PolicyRevision, f.MaxVerificationAge = p.Revision, api.DefaultBindingVerificationAge
		return f
	}
	abort := func(f *state.BindingPromotionFence) error {
		writeCtx := ctx
		if f != nil {
			writeCtx = state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{*f})
		}
		_, _, err := recovery.RecoverRolloutForDeployment(writeCtx, app.ID, candidate.ID, predecessor.ID, "abort", "incident recovery")
		return err
	}
	if err := abort(nil); !state.IsBindingReleaseRequired(err) {
		t.Fatalf("unguarded recovery: %v", err)
	}
	unchanged()
	wrong := fence(candidate)
	if err := abort(&wrong); !state.IsBindingReleaseRequired(err) {
		t.Fatalf("candidate evidence authorized predecessor: %v", err)
	}
	unchanged()
	expired := fence(predecessor)
	expired.ValidUntil = time.Now().Add(-time.Second)
	if err := abort(&expired); !errors.Is(err, state.ErrBindingPromotionExpired) {
		t.Fatalf("expired recovery: %v", err)
	}
	unchanged()
	stale := fence(predecessor)
	if err := s.UpsertAppEnv(ctx, a.ID, app.ID, "RECOVERY_TEST", "changed"); err != nil {
		t.Fatal(err)
	}
	if err := abort(&stale); !errors.Is(err, state.ErrBindingPromotionChanged) {
		t.Fatalf("changed dependencies: %v", err)
	}
	unchanged()
	stale = fence(predecessor)
	revision := int64(1)
	if _, err := policies.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &revision}); err != nil {
		t.Fatal(err)
	}
	if err := abort(&stale); !errors.Is(err, state.ErrBindingPromotionChanged) {
		t.Fatalf("changed policy: %v", err)
	}
	unchanged()
	valid := fence(predecessor)
	if err := abort(&valid); err != nil {
		t.Fatalf("verified recovery: %v", err)
	}
	d, _ := s.DeploymentByID(ctx, candidate.ID)
	p, _ := s.DeploymentByID(ctx, predecessor.ID)
	policy, _ := policies.GetBindingReleasePolicy(ctx, a.ID, app.ID, "default")
	if d.RolloutState != "aborted" || d.TrafficPercent != 0 || p.TrafficPercent != 100 || policy.Mode != "enforce" {
		t.Fatalf("recovery receipt: candidate=%+v predecessor=%+v policy=%+v", d, p, policy)
	}
	audits, err := s.ListDeploymentAudit(ctx, candidate.ID, 100)
	if err != nil || len(audits) != len(auditsBefore)+1 {
		t.Fatalf("recovery audit: %v %v", audits, err)
	}
	var payload struct {
		Predecessor string                        `json:"predecessor_deployment_id"`
		Fences      []state.BindingPromotionFence `json:"binding_fences"`
	}
	if err := json.Unmarshal(audits[0].Data, &payload); err != nil || payload.Predecessor != predecessor.ID || len(payload.Fences) != 1 || payload.Fences[0].PolicyRevision != 2 {
		t.Fatalf("audit lost checked recovery: %s %v", audits[0].Data, err)
	}
}

func TestBindingReleaseRecoveryPGLockRaces(t *testing.T) {
	for _, scenario := range []string{"policy changed", "expired", "predecessor moved", "policy enabled"} {
		t.Run(scenario, func(t *testing.T) {
			s, pool, _ := pgStoreWithPool(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			a, app, predecessor, candidate := bindingReleaseRecoveryFixture(t, s)
			zero := int64(0)
			if scenario != "policy enabled" {
				if _, err := s.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
					t.Fatal(err)
				}
			}
			f := bindingRecoveryFence(ctx, t, s, a, app, predecessor)
			f.PolicyRevision, f.MaxVerificationAge = 1, api.DefaultBindingVerificationAge
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			// Recovery must sample all evidence after waiting for its app lock.
			if _, err := tx.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, app.ID); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "expired":
				f.ValidUntil = time.Now().Add(100 * time.Millisecond)
			case "policy changed":
				_, err = tx.Exec(ctx, `UPDATE app_binding_release_policies SET revision=revision+1,max_age_seconds=30 WHERE app_id=$1 AND scope='default'`, app.ID)
			case "policy enabled":
				_, err = tx.Exec(ctx, `INSERT INTO app_binding_release_policies(app_id,scope,mode,revision,max_age_seconds) VALUES($1,'default','enforce',1,600)`, app.ID)
			case "predecessor moved":
				_, err = tx.Exec(ctx, `UPDATE deployments SET traffic_percent=0 WHERE id=$1`, predecessor.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				writeCtx := state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{f})
				if scenario == "policy enabled" {
					writeCtx = ctx
				}
				_, _, err := s.RecoverRolloutForDeployment(writeCtx, app.ID, candidate.ID, predecessor.ID, "abort", "race test")
				done <- err
			}()
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND (query LIKE '%LockCanaryRouteGateApp%' OR query LIKE '%LockRoutePolicyApp%' OR query LIKE '%LockRoutePolicyAccount%'))`).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("recovery did not wait: %v", err)
				default:
				}
				select {
				case <-ctx.Done():
					t.Fatal("recovery account/app policy lock never observed")
				case <-time.After(10 * time.Millisecond):
				}
			}
			if scenario == "expired" {
				for time.Now().Before(f.ValidUntil) {
					time.Sleep(10 * time.Millisecond)
				}
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			want := state.ErrBindingPromotionChanged
			if scenario == "expired" {
				want = state.ErrBindingPromotionExpired
			}
			if scenario == "predecessor moved" {
				want = state.ErrNotFound
			}
			if scenario == "policy enabled" {
				want = state.ErrBindingReleaseRequired
			}
			if err := <-done; !errors.Is(err, want) && !(scenario == "policy enabled" && state.IsBindingReleaseRequired(err)) {
				t.Fatalf("race admitted: %v want %v", err, want)
			}
			d, err := s.DeploymentByID(ctx, candidate.ID)
			if err != nil || d.TrafficPercent != 25 || d.RolloutState != "rolling_out" {
				t.Fatalf("race mutated candidate: %+v %v", d, err)
			}
			audits, err := s.ListDeploymentAudit(ctx, candidate.ID, 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, audit := range audits {
				if audit.Kind == state.DeployRolledBack {
					t.Fatalf("race wrote rollback audit: %+v", audit)
				}
			}
		})
	}
}

func TestBindingReleaseRecoveryPGExpiredLease(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	ctx := t.Context()
	a, app, predecessor, candidate := bindingReleaseRecoveryFixture(t, s)
	zero := int64(0)
	if _, err := s.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	if err := s.StampSafeReleaseWorkerLease(ctx, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE safe_release_worker_lease SET healthy_at=clock_timestamp()-interval '6 minutes',expires_at=clock_timestamp()-interval '5 minutes' WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AbortCanaryOnExpiredWorkerLease(ctx, app.ID, candidate.ID, 2*time.Minute); !state.IsBindingReleaseRequired(err) {
		t.Fatalf("unchecked emergency recovery: %v", err)
	}
	f := bindingRecoveryFence(ctx, t, s, a, app, predecessor)
	f.PolicyRevision, f.MaxVerificationAge = 1, api.DefaultBindingVerificationAge
	updated, auditID, err := s.AbortCanaryOnExpiredWorkerLease(state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{f}), app.ID, candidate.ID, 2*time.Minute)
	if err != nil || updated.RolloutState != "aborted" || auditID == 0 {
		t.Fatalf("checked emergency recovery: %+v %d %v", updated, auditID, err)
	}
	audits, err := s.ListDeploymentAudit(ctx, candidate.ID, 100)
	if err != nil || len(audits) == 0 || audits[0].Actor != "apid:safe_release_lease_expired" {
		t.Fatalf("emergency audit: %v %v", audits, err)
	}
}

func TestBindingReleaseRecoveryPGRouteHealthAudit(t *testing.T) {
	pool, s, a, app, candidate, seed := healthAbortPG(t)
	ctx := t.Context()
	seed(100, 100)
	healthAbortErrors(t, pool, a, app, candidate)
	rows, err := s.LiveDeployments(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	var predecessor state.Deployment
	for _, d := range rows {
		if _, err := pool.Exec(ctx, `UPDATE deployments SET image_digest='sha256:binding-recovery',rootfs_key='test/'||id::text WHERE id=$1`, d.ID); err != nil {
			t.Fatal(err)
		}
		if d.ID != candidate.ID && d.TrafficPercent > 0 {
			predecessor = d
		}
	}
	if predecessor.ID == "" {
		t.Fatal("no route-health predecessor")
	}
	zero := int64(0)
	if _, err := s.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecoverCanaryRouteHealth(ctx, a.ID, app.ID, candidate.ID, 0); !state.IsBindingReleaseRequired(err) {
		t.Fatalf("unchecked health recovery: %v", err)
	}
	f := bindingRecoveryFence(ctx, t, s, a, app, predecessor)
	f.PolicyRevision, f.MaxVerificationAge = 1, api.DefaultBindingVerificationAge
	result, err := s.RecoverCanaryRouteHealth(state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{f}), a.ID, app.ID, candidate.ID, 0)
	if err != nil || !result.Aborted || result.AuditID == 0 {
		t.Fatalf("checked health recovery: %+v %v", result, err)
	}
	audits, err := s.ListDeploymentAudit(ctx, candidate.ID, 100)
	if err != nil || len(audits) == 0 || audits[0].Actor != "meterd:route_health_recovery" {
		t.Fatalf("health audit: %v %v", audits, err)
	}
	var data struct {
		Fences      []state.BindingPromotionFence `json:"binding_fences"`
		Health      json.RawMessage               `json:"route_health"`
		Predecessor string                        `json:"predecessor_deployment_id"`
	}
	if err := json.Unmarshal(audits[0].Data, &data); err != nil || len(data.Fences) != 1 || len(data.Health) == 0 || data.Predecessor != predecessor.ID {
		t.Fatalf("health recovery lost checked audit: %s %v", audits[0].Data, err)
	}
}
