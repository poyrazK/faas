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

func (s *server) checkedRollbackCheck(ctx context.Context, operation api.RollbackOperation) (string, string, []api.BindingCheckFinding, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	store := s.store.(state.CheckedRollbackStore)
	target, err := s.store.DeploymentByID(ctx, operation.TargetDeploymentID)
	if err != nil {
		return "", "", nil, err
	}
	if target.AppID != operation.AppID {
		return "failed", "rollback_deployment_changed", nil, nil
	}
	if target.Status == state.DeployFailed || target.Status == state.DeployCancelled || target.Status == state.DeploySuperseded {
		return "failed", "rollback_readiness_failed", nil, nil
	}
	if operation.Status != "routing" {
		current, readErr := s.store.DeploymentByID(ctx, operation.CurrentDeploymentID)
		if readErr != nil {
			return "", "", nil, readErr
		}
		if current.AppID != operation.AppID || current.Status != state.DeployLive || current.TrafficPercent != 100 || current.CanaryTotalSteps > 0 && current.CanaryStep < current.CanaryTotalSteps || state.IsServiceRollout(current) {
			return "failed", "rollback_deployment_changed", nil, nil
		}
	}

	if operation.Status == "preparing" {
		return "preparing", "", nil, nil
	}
	if operation.Status == "routing" {
		h := target.ServiceRolloutHandoff
		if h.Phase == state.ServiceRolloutPhaseComplete {
			if h.Action != state.ServiceRolloutActionPromote || target.RolloutState != "complete" {
				return "failed", "rollback_handoff_aborted", nil, nil
			}
			return "complete", "", nil, nil
		}
		if target.TrafficPercent != 100 && !h.ActiveAbort() {
			return "failed", "rollback_deployment_changed", nil, nil
		}
		return "routing", h.LastError, nil, nil
	}
	app, err := s.store.AppByID(ctx, operation.AppID)
	if err != nil {
		return "", "", nil, err
	}
	if problem := s.verifyRollbackTargetArtifact(ctx, target); problem != nil {
		if problem.Status >= 500 {
			return rollbackProblemStatus(problem)
		}
		return "failed", problem.Code, nil, nil
	}

	acct, err := s.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return "", "", nil, err
	}
	if !acct.MayDeploy() {
		return "blocked", "account_deploy_blocked", nil, nil
	}
	ctx = context.WithValue(ctx, bindingReleaseWorkerReadsKey{}, true)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
	if err != nil {
		return "", "", nil, err
	}
	observations, problem := s.serviceRecipientBindingObservation(r, acct, app, target)
	if problem != nil {
		return rollbackProblemStatus(problem)
	}
	var updated api.RollbackOperation
	problem, err = s.withBindingReleaseObservations(r, acct, app, observations, func(writeCtx context.Context) error {
		var writeErr error
		updated, writeErr = store.CommitCheckedRollback(writeCtx, operation)
		return writeErr
	})
	if problem != nil {
		return rollbackProblemStatus(problem)
	}
	if errors.Is(err, state.ErrCheckedRollbackChanged) {
		return "failed", "rollback_deployment_changed", nil, nil
	}
	if errors.Is(err, state.ErrServiceRolloutNotReady) {
		return "blocked", "service_rollout_not_ready", []api.BindingCheckFinding{{Code: "service_rollout_not_ready", DeploymentID: target.ID, Message: "The rollback target needs ready service capacity before traffic can move."}}, nil
	}
	if err != nil {
		return "", "", nil, err
	}
	s.notifyCheckedRollback(ctx, updated)
	return "", "", nil, nil // Routing and its receipt committed together; avoid a stale follow-up write.
}
func rollbackProblemStatus(problem *api.Problem) (string, string, []api.BindingCheckFinding, error) {
	var blockers []api.BindingCheckFinding
	if problem.BindingsCheck != nil {
		blockers = problem.BindingsCheck.Blockers
	}
	return "blocked", problem.Code, blockers, nil
}
func (s *server) notifyCheckedRollback(ctx context.Context, r api.RollbackOperation) {
	if s.notif == nil {
		return
	}
	payload, _ := json.Marshal(map[string]string{"kind": "service_rollout", "app_id": r.AppID, "deployment_id": r.TargetDeploymentID, "status": "live"})
	if err := s.notif.Notify(ctx, db.NotifyDeploymentChanged, string(payload)); err != nil {
		s.log.Warn("notify checked rollback routing", "request_id", r.ID, "err", err)
	}
	if !r.Service {
		payload, _ = json.Marshal(map[string]string{"app_id": r.AppID, "deployment_id": r.CurrentDeploymentID, "status": "superseded"})
		previous, err := s.store.DeploymentByID(ctx, r.CurrentDeploymentID)
		if err == nil && previous.Status == state.DeploySuperseded {
			if err := s.notif.Notify(ctx, db.NotifyDeploymentChanged, string(payload)); err != nil {
				s.log.Warn("notify rollback predecessor", "request_id", r.ID, "err", err)
			}
		}
	}
}
func (s *server) checkedRollbackSweep(ctx context.Context) error {
	store, ok := s.store.(state.CheckedRollbackStore)
	if !ok {
		return nil
	}
	operations, err := store.ListPendingCheckedRollbacks(ctx)
	if err != nil {
		return err
	}
	for _, operation := range operations {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		status, code, blockers, err := s.checkedRollbackCheck(ctx, operation)
		if err != nil {
			status = operation.Status
			code = "rollback_check_unavailable"
		}
		if status == "" {
			continue
		}
		if err := store.UpdateCheckedRollback(ctx, operation, status, code, blockers); err != nil && !errors.Is(err, state.ErrCheckedRollbackChanged) && ctx.Err() == nil {
			s.log.Warn("persist checked rollback progress", "request_id", operation.ID, "err", err)
		}
	}
	return ctx.Err()
}
func (s *server) runCheckedRollbackWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(api.ServiceBindingCheckIntervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := s.checkedRollbackSweep(bounded); err != nil && ctx.Err() == nil {
			s.log.Warn("checked rollback sweep", "err", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
