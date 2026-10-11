package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) queueHistoricalAlertRollback(ctx context.Context, r api.AlertRollback) (string, string, []api.BindingCheckFinding, error) {
	if r.DeploymentEvidence == nil {
		return "failed", "alert_rollback_evidence_missing", nil, nil
	}
	if !time.Now().Before(r.FiredAt.Add(api.AlertRollbackEvidenceMaxCheckDelay)) {
		return "failed", "alert_rollback_evidence_expired", nil, nil
	}
	target, err := s.store.DeploymentByID(ctx, r.PredecessorDeploymentID)
	if errors.Is(err, state.ErrNotFound) {
		return "failed", "alert_rollback_deployment_changed", nil, nil
	}
	if err != nil {
		return "", "", nil, err
	}
	if problem := s.verifyRollbackTargetArtifact(ctx, target); problem != nil {
		if problem.Status >= 500 {
			return rollbackProblemStatus(problem)
		}
		return "failed", problem.Code, nil, nil
	}
	if api.ApiContractDiffEnabled() && strings.EqualFold(target.Scope, "prod") {
		check, err := openapidiff.CheckDeploymentPromotion(ctx, s.store, r.AppID, target.ID, "prod")
		if err != nil && !errors.Is(err, openapidiff.ErrSnapshotBaselineMissing) {
			return "", "", nil, err
		}
		if len(check.Diff.Breaks) > 0 {
			return "failed", api.CodeAPIContractBreakingChange, nil, nil
		}
	}
	accepted, err := s.store.(state.AlertRollbackStore).CommitAlertRollback(ctx, r)
	if errors.Is(err, state.ErrAlertRollbackEvidenceExpired) {
		return "failed", "alert_rollback_evidence_expired", nil, nil
	}
	if errors.Is(err, state.ErrAlertRollbackChanged) || errors.Is(err, state.ErrCheckedRollbackChanged) || errors.Is(err, state.ErrNoRollbackTarget) || errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
		return "failed", "alert_rollback_deployment_changed", nil, nil
	}
	if err != nil {
		return "", "", nil, err
	}
	if s.notif != nil && accepted.RollbackOperationID != "" {
		payload, _ := json.Marshal(map[string]string{"app_id": r.AppID, "deployment_id": target.ID})
		if err := s.notif.Notify(ctx, db.NotifySnapshotPrime, string(payload)); err != nil {
			s.log.Warn("historical alert readiness wakeup", "fire_id", r.ID, "err", err)
		}
	}
	return "", "", nil, nil
}
