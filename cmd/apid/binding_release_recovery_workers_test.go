package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// The real lease/health decisions are covered by PgStore's recovery suites;
// this adapter proves that APID passes the checked recipient fence to writers.
type bindingRecoveryWorkerStore struct {
	*state.MemStore
	predecessorID string
	health        state.SafeReleaseWorkerLeaseHealth
	calls         int
}

func (s *bindingRecoveryWorkerStore) RecoverCanaryRouteHealth(ctx context.Context, accountID, appID, deploymentID string, expectedStep int) (state.RouteHealthRecoveryResult, error) {
	d, err := s.DeploymentByID(ctx, deploymentID)
	if err != nil {
		return state.RouteHealthRecoveryResult{}, err
	}
	if d.CanaryStep != expectedStep {
		return state.RouteHealthRecoveryResult{}, state.ErrCanaryStepConflict
	}
	s.calls++
	updated, auditID, err := s.RecoverRolloutForDeployment(ctx, appID, deploymentID, s.predecessorID, "abort", "health worker")
	return state.RouteHealthRecoveryResult{Deployment: updated, AuditID: auditID, Aborted: err == nil, Decision: &api.RouteHealthDecision{DeploymentID: deploymentID, HistoryID: uuid.NewString(), Status: "aborted"}}, err
}

func (s *bindingRecoveryWorkerStore) SafeReleaseWorkerLeaseHealth(context.Context) (state.SafeReleaseWorkerLeaseHealth, error) {
	return s.health, nil
}
func (s *bindingRecoveryWorkerStore) AbortCanaryOnExpiredWorkerLease(ctx context.Context, appID, deploymentID string, _ time.Duration) (state.Deployment, int64, error) {
	s.calls++
	return s.RecoverRolloutForDeployment(ctx, appID, deploymentID, s.predecessorID, "abort", "lease worker")
}

func TestBindingReleaseRecoveryAdditionalWorkers(t *testing.T) {
	for _, worker := range []string{"route health", "expired lease"} {
		t.Run(worker, func(t *testing.T) {
			e, app, predecessor, candidate := promotionFixture(t)
			ctx := t.Context()
			if _, err := e.store.UpdateDeploymentTraffic(ctx, candidate.ID, 25); err != nil {
				t.Fatal(err)
			}
			if err := e.store.SetDeploymentCanaryState(ctx, candidate.ID, "balanced", 1, 4, time.Now().Add(-time.Minute), "rolling_out"); err != nil {
				t.Fatal(err)
			}
			zero := int64(0)
			if _, err := e.store.SetBindingReleasePolicy(ctx, e.acct.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
				t.Fatal(err)
			}
			store := &bindingRecoveryWorkerStore{MemStore: e.store, predecessorID: predecessor.ID, health: state.SafeReleaseWorkerLeaseHealth{Exists: true, CheckedAt: time.Now(), ExpiresAt: time.Now().Add(-5 * time.Minute)}}
			e.s.store = store
			adapter := bindingCheckedEmergencyRecoveryStore{SafeReleaseEmergencyRecoveryStore: store, server: e.s}
			const token = "additional-canary-recovery-action-00000001"
			mux := http.NewServeMux()
			if err := e.s.mountInternalSafeDeploy(mux, "127.0.0.1:9101", "additional-canary-recovery-progress-000001", token); err != nil {
				t.Fatal(err)
			}
			if worker == "expired lease" {
				store.health.ExpiresAt = time.Now().Add(time.Minute)
				if _, _, err := adapter.AbortCanaryOnExpiredWorkerLease(ctx, app.ID, candidate.ID, safeReleaseEmergencyGrace); !errors.Is(err, state.ErrSafeReleaseLeaseNotExpired) || store.calls != 0 {
					t.Fatalf("healthy lease touched binding recovery: calls=%d err=%v", store.calls, err)
				}
				store.health.ExpiresAt = time.Now().Add(-5 * time.Minute)
			}
			call := func() (int, *api.Problem) {
				if worker == "expired lease" {
					d, auditID, err := adapter.AbortCanaryOnExpiredWorkerLease(ctx, app.ID, candidate.ID, safeReleaseEmergencyGrace)
					if err == nil {
						if d.RolloutState != "aborted" || auditID == 0 {
							t.Fatalf("invalid emergency receipt: %+v %d", d, auditID)
						}
						return 200, nil
					}
					var problem *api.Problem
					if !errors.As(err, &problem) {
						t.Fatalf("unexpected emergency failure: %v", err)
					}
					return problem.Status, problem
				}
				r := httptest.NewRequest(http.MethodPost, "/v1/internal/safe-deploy/deployments/"+candidate.ID+"/route-health/recover", strings.NewReader(`{"expected_step":1}`))
				r.RemoteAddr = "127.0.0.1:10000"
				r.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				if w.Code == 200 {
					return w.Code, nil
				}
				var problem api.Problem
				if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
					t.Fatalf("worker response: %d %s", w.Code, w.Body)
				}
				return w.Code, &problem
			}
			status, problem := call()
			if status != 409 || problem == nil || problem.BindingsCheck == nil || problem.BindingsCheck.DeploymentID != predecessor.ID || store.calls != 0 {
				t.Fatalf("missing recipient evidence: status=%d calls=%d problem=%+v", status, store.calls, problem)
			}
			assertPromotionWeights(t, e, predecessor, candidate, 25)
			completePromotionProbe(t, e, app, predecessor, passedPostgresVerification)
			if status, problem = call(); status != 200 || store.calls != 1 {
				t.Fatalf("verified worker: status=%d calls=%d problem=%+v", status, store.calls, problem)
			}
			assertPromotionWeights(t, e, predecessor, candidate, 0)
		})
	}
}
