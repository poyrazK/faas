package main

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// These workers restore the sole older serving sibling. Their original store
// operation still rechecks the lease/health decision and exact recipient under
// locks. A changed selection invalidates the binding fence before any writes.
func (s *server) withBindingCheckedCanaryAbort(r *http.Request, acct state.Account, app state.App, candidate state.Deployment, write func(context.Context) error) (*api.Problem, error) {
	policies, ok := s.store.(state.BindingReleasePolicyStore)
	if !ok {
		return api.ErrCapacity("binding release policies are unavailable"), nil
	}
	policy, err := policies.GetBindingReleasePolicy(r.Context(), acct.ID, app.ID, candidate.Scope)
	if err != nil {
		return api.ErrCapacity("binding release policy could not be read"), err
	}
	if policy.Mode != "enforce" {
		return s.withBindingReleaseObservations(r, acct, app, nil, write)
	}
	rows, err := s.store.LiveDeployments(r.Context(), app.ID)
	if err != nil {
		return api.ErrCapacity("recovery predecessors could not be read"), err
	}
	var predecessor state.Deployment
	for _, d := range rows {
		if d.ID == candidate.ID || d.TrafficPercent <= 0 || recoveryScope(d.Scope) != recoveryScope(candidate.Scope) {
			continue
		}
		if predecessor.ID != "" || !d.CreatedAt.Before(candidate.CreatedAt) {
			return nil, state.ErrCanaryStateInvalid
		}
		predecessor = d
	}
	if predecessor.ID == "" {
		return nil, state.ErrNotFound
	}
	age, err := time.ParseDuration(policy.MaxVerificationAge)
	if err != nil {
		return api.ErrCapacity("binding release policy is invalid"), err
	}
	observation, problem := s.observeBindingPromotion(r, acct, app, predecessor, api.BindingPromotionRequest{RequireApplicationAck: policy.RequireApplicationAck}, age)
	if problem != nil {
		return problem, nil
	}
	observation.expiration.Message = "Binding verification expired before recovery; verify the exact predecessor again."
	return s.withBindingReleaseObservations(r, acct, app, []bindingPromotionObservation{observation}, write)
}

type bindingCheckedEmergencyRecoveryStore struct {
	state.SafeReleaseEmergencyRecoveryStore
	server *server
}

func (s bindingCheckedEmergencyRecoveryStore) AbortCanaryOnExpiredWorkerLease(ctx context.Context, appID, deploymentID string, grace time.Duration) (state.Deployment, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	healthStore, ok := s.SafeReleaseEmergencyRecoveryStore.(state.SafeReleaseWorkerLeaseHealthStore)
	if !ok {
		return state.Deployment{}, 0, state.ErrSafeReleaseLeaseUnavailable
	}
	health, err := healthStore.SafeReleaseWorkerLeaseHealth(ctx)
	if err != nil {
		return state.Deployment{}, 0, err
	}
	if !health.Exists {
		return state.Deployment{}, 0, state.ErrSafeReleaseLeaseMissing
	}
	if health.CheckedAt.Before(health.ExpiresAt.Add(grace)) {
		return state.Deployment{}, 0, state.ErrSafeReleaseLeaseNotExpired
	}
	candidate, err := s.server.store.DeploymentByID(ctx, deploymentID)
	if err != nil || candidate.AppID != appID {
		if err == nil {
			err = state.ErrNotFound
		}
		return state.Deployment{}, 0, err
	}
	app, err := s.server.store.AppByID(ctx, appID)
	if err != nil {
		return state.Deployment{}, 0, err
	}
	acct, err := s.server.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return state.Deployment{}, 0, err
	}
	ctx = context.WithValue(ctx, bindingReleaseWorkerReadsKey{}, true)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
	if err != nil {
		return state.Deployment{}, 0, err
	}
	var updated state.Deployment
	var auditID int64
	problem, err := s.server.withBindingCheckedCanaryAbort(r, acct, app, candidate, func(writeCtx context.Context) error {
		var writeErr error
		updated, auditID, writeErr = s.SafeReleaseEmergencyRecoveryStore.AbortCanaryOnExpiredWorkerLease(writeCtx, appID, deploymentID, grace)
		return writeErr
	})
	if problem != nil {
		return state.Deployment{}, 0, problem
	}
	return updated, auditID, err
}
