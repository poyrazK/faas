package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) serviceRolloutBindingCheck(ctx context.Context, candidate state.Deployment) (*api.Problem, error) {
	if rollbacks, ok := s.store.(state.CheckedRollbackStore); ok {
		_, err := rollbacks.CheckedRollbackForTarget(ctx, candidate.ID)
		if err == nil {
			return nil, nil
		}
		if !errors.Is(err, state.ErrNotFound) {
			return nil, err
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	gate := candidate.ServiceRolloutHandoff.BindingsCheck
	if gate == nil || gate.Status == "passed" {
		return nil, nil
	}
	app, err := s.store.AppByID(ctx, candidate.AppID)
	if err != nil {
		return nil, err
	}
	acct, err := s.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return nil, err
	}
	if !acct.MayDeploy() {
		return api.NewProblem(http.StatusConflict, "account_deploy_blocked", "Deployment unavailable", "The account cannot currently change serving deployments."), nil
	}
	recipient, err := s.store.DeploymentByID(ctx, gate.DeploymentID)
	if err != nil {
		return nil, err
	}
	if recipient.AppID != app.ID || recipient.Status != state.DeployLive || recoveryScope(recipient.Scope) != recoveryScope(candidate.Scope) {
		return nil, state.ErrServiceRolloutInvalid
	}
	ctx = state.WithServiceRolloutBindingRequest(ctx, gate.RequestID)
	ctx = context.WithValue(ctx, bindingReleaseWorkerReadsKey{}, true)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
	if err != nil {
		return nil, err
	}
	observations, problem := s.serviceRecipientBindingObservation(r, acct, app, recipient)
	if problem != nil {
		return problem, nil
	}
	var updated state.Deployment
	problem, err = s.withBindingReleaseObservations(r, acct, app, observations, func(writeCtx context.Context) error {
		var writeErr error
		switch gate.Action {
		case state.ServiceRolloutActionPromote:
			updated, writeErr = s.store.BeginServiceRolloutCutover(writeCtx, candidate.ID)
		case state.ServiceRolloutActionAbort:
			updated, writeErr = s.store.BeginServiceRolloutAbort(writeCtx, candidate.ID)
		default:
			writeErr = state.ErrServiceRolloutInvalid
		}
		return writeErr
	})
	if problem == nil && err == nil && s.notif != nil {
		payload, _ := json.Marshal(map[string]string{"kind": "service_rollout", "app_id": app.ID, "deployment_id": updated.ID, "status": string(updated.Status)})
		if err := s.notif.Notify(ctx, db.NotifyDeploymentChanged, string(payload)); err != nil {
			s.log.Warn("notify checked service routing", "deployment_id", candidate.ID, "err", err)
		}
	}
	return problem, err
}

func (s *server) serviceRecipientBindingObservation(r *http.Request, acct state.Account, app state.App, recipient state.Deployment) ([]bindingPromotionObservation, *api.Problem) {
	if problem := s.durableEntityValidatorReleaseProblem(r.Context(), app, recipient); problem != nil {
		return nil, problem
	}
	policies, ok := s.store.(state.BindingReleasePolicyStore)
	if !ok {
		return nil, api.ErrCapacity("binding release policies are unavailable")
	}
	policy, err := policies.GetBindingReleasePolicy(r.Context(), acct.ID, app.ID, recipient.Scope)
	if err != nil {
		return nil, api.ErrCapacity("binding release policy could not be read")
	}
	if policy.Mode != "enforce" {
		return nil, nil
	}
	age, err := time.ParseDuration(policy.MaxVerificationAge)
	if err != nil {
		return nil, api.ErrCapacity("binding release policy is invalid")
	}
	observation, problem := s.observeBindingPromotion(r, acct, app, recipient, api.BindingPromotionRequest{RequireApplicationAck: policy.RequireApplicationAck}, age)
	if problem != nil {
		return nil, problem
	}
	return []bindingPromotionObservation{observation}, nil
}

func (s *server) serviceRolloutBindingSweep(ctx context.Context, cursor *int) error {
	rows, err := s.store.ListServiceRolloutsInFlight(ctx, "")
	if err != nil || len(rows) == 0 {
		return err
	}
	statusStore, ok := s.store.(state.ServiceRolloutBindingStore)
	if !ok {
		return errors.New("service binding status store unavailable")
	}
	start := *cursor % len(rows)
	for n := 0; n < len(rows) && n < api.ServiceBindingCheckBatchSize && ctx.Err() == nil; n++ {
		index := (start + n) % len(rows)
		*cursor = (index + 1) % len(rows)
		candidate := rows[index]
		gate := candidate.ServiceRolloutHandoff.BindingsCheck
		if gate == nil || gate.Status == "passed" {
			continue
		}
		problem, err := s.serviceRolloutBindingCheck(ctx, candidate)
		if problem == nil && err == nil {
			continue
		}
		code := "service_rollout_check_unavailable"
		var blockers []api.BindingCheckFinding
		if problem != nil {
			code = problem.Code
			if problem.BindingsCheck != nil {
				blockers = problem.BindingsCheck.Blockers
			}
		} else if errors.Is(err, state.ErrServiceRolloutNotReady) {
			code = "service_rollout_not_ready"
			blockers = []api.BindingCheckFinding{{Code: code, DeploymentID: gate.DeploymentID, Message: "The exact recipient must have ready service capacity before routing can change."}}
		}
		if updateErr := statusStore.UpdateServiceRolloutBindingStatus(ctx, candidate.ID, gate.RequestID, code, blockers); updateErr != nil && !errors.Is(updateErr, state.ErrServiceRolloutInvalid) && ctx.Err() == nil {
			s.log.Warn("persist service binding blocker", "deployment_id", candidate.ID, "err", updateErr)
		}
	}
	return ctx.Err()
}

func (s *server) runServiceRolloutBindingWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(api.ServiceBindingCheckIntervalSeconds) * time.Second)
	defer ticker.Stop()
	cursor := 0
	for {
		sweepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := s.serviceRolloutBindingSweep(sweepCtx, &cursor); err != nil && ctx.Err() == nil {
			s.log.Warn("service binding check sweep", "err", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
