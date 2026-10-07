// adr: 602 — APID owns durable alert-triggered exact recovery.
package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) alertRollbackCheck(ctx context.Context, operation api.AlertRollback) (string, string, []api.BindingCheckFinding, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if operation.RollbackOperationID != "" {
		store, ok := s.store.(state.HistoricalAlertRollbackStore)
		if !ok {
			return "", "", nil, errors.New("historical alert rollback store unavailable")
		}
		_, err := store.RefreshHistoricalAlertRollback(ctx, operation)
		return "", "", nil, err
	}
	if operation.ServiceRequestID != "" {
		store, ok := s.store.(state.ServiceAlertRollbackStore)
		if !ok {
			return "", "", nil, errors.New("service alert rollback store unavailable")
		}
		_, err := store.RefreshServiceAlertRollback(ctx, operation)
		return "", "", nil, err
	}
	rule, err := s.store.AlertRuleByID(ctx, operation.RuleID)
	if errors.Is(err, state.ErrNotFound) {
		return "failed", "alert_rollback_rule_invalid", nil, nil
	}
	if err != nil {
		return "", "", nil, err
	}
	if !rule.Enabled || rule.Action != state.AlertActionRollback || rule.AccountID != operation.AccountID || rule.AppID != operation.AppID || !api.AlertRuleActionAllowedForMetric(string(rule.Metric), string(rule.Action)) {
		return "failed", "alert_rollback_rule_invalid", nil, nil
	}
	app, err := s.store.AppByID(ctx, operation.AppID)
	if errors.Is(err, state.ErrNotFound) {
		return "failed", "alert_rollback_deployment_changed", nil, nil
	}
	if err != nil {
		return "", "", nil, err
	}
	if app.AccountID != operation.AccountID {
		return "failed", "alert_rollback_rule_invalid", nil, nil
	}
	acct, err := s.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return "", "", nil, err
	}
	if !acct.MayDeploy() {
		return "blocked", "account_deploy_blocked", nil, nil
	}
	if !operation.Service && !operation.Historical && !acct.Plan.TrafficSplitAllowed() {
		return "blocked", "plan_traffic_split_not_allowed", nil, nil
	}
	if operation.Historical {
		return s.queueHistoricalAlertRollback(ctx, operation)
	}
	candidate, err := s.store.DeploymentByID(ctx, operation.CandidateDeploymentID)
	if errors.Is(err, state.ErrNotFound) {
		return "failed", "alert_rollback_deployment_changed", nil, nil
	}
	if err != nil {
		return "", "", nil, err
	}
	active := candidate.CanaryTotalSteps > 0 && candidate.CanaryStep < candidate.CanaryTotalSteps && (candidate.RolloutState == "pending" || candidate.RolloutState == "rolling_out")
	if operation.Service {
		active = state.IsServiceRollout(candidate) && app.Manifest.ExecutionMode == api.ExecutionModeService
	}
	if candidate.AppID != app.ID || candidate.Status != state.DeployLive || !active || recoveryScope(candidate.Scope) != operation.Scope {
		return "failed", "alert_rollback_deployment_changed", nil, nil
	}
	predecessor, err := s.store.DeploymentByID(ctx, operation.PredecessorDeploymentID)
	if errors.Is(err, state.ErrNotFound) {
		return "failed", "alert_rollback_deployment_changed", nil, nil
	}
	if err != nil {
		return "", "", nil, err
	}
	if predecessor.AppID != app.ID || predecessor.Status != state.DeployLive || !operation.Service && predecessor.TrafficPercent <= 0 || recoveryScope(predecessor.Scope) != operation.Scope {
		return "failed", "alert_rollback_deployment_changed", nil, nil
	}
	if operation.Service {
		_, err = s.store.(state.AlertRollbackStore).CommitAlertRollback(ctx, operation)
		if errors.Is(err, state.ErrAlertRollbackChanged) || errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrServiceRolloutInvalid) {
			return "failed", "alert_rollback_deployment_changed", nil, nil
		}
		return "", "", nil, err
	}
	ctx = context.WithValue(ctx, bindingReleaseWorkerReadsKey{}, true)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
	if err != nil {
		return "", "", nil, err
	}
	observations, problem := s.serviceRecipientBindingObservation(request, acct, app, predecessor)
	if problem != nil {
		return rollbackProblemStatus(problem)
	}
	problem, err = s.withBindingReleaseObservations(request, acct, app, observations, func(writeCtx context.Context) error {
		_, writeErr := s.store.(state.AlertRollbackStore).CommitAlertRollback(writeCtx, operation)
		return writeErr
	})
	if problem != nil {
		return rollbackProblemStatus(problem)
	}
	if errors.Is(err, state.ErrAlertRollbackChanged) || errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrRolloutStateInvalid) {
		return "failed", "alert_rollback_deployment_changed", nil, nil
	}
	if err != nil {
		return "", "", nil, err
	}
	return "", "", nil, nil // Completion, traffic, audit and notification committed together.
}
func (s *server) processAlertRollback(ctx context.Context, operation api.AlertRollback) error {
	if operation.Status == "complete" || operation.Status == "failed" {
		return nil
	}
	status, code, blockers, err := s.alertRollbackCheck(ctx, operation)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		status, code, blockers = "blocked", "alert_rollback_check_unavailable", nil
	}
	if status == "" {
		return nil
	}
	return s.store.(state.AlertRollbackStore).UpdateAlertRollback(ctx, operation, status, code, blockers)
}
func (s *server) alertRollbackSweep(ctx context.Context) error {
	store, ok := s.store.(state.AlertRollbackStore)
	if !ok {
		return nil
	}
	rows, err := store.ListPendingAlertRollbacks(ctx)
	if err != nil {
		return err
	}
	for _, operation := range rows {
		if err := s.processAlertRollback(ctx, operation); err != nil && !errors.Is(err, state.ErrNotFound) {
			return err
		}
	}
	return ctx.Err()
}
func (s *server) runAlertRollbackWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(api.AlertRollbackCheckIntervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := s.alertRollbackSweep(bounded); err != nil && ctx.Err() == nil {
			s.log.Warn("alert rollback sweep", "err", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
