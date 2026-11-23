package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
)

// ErrProjectRollbackHeld signals a successful cutover check while the project
// barrier still owns routing.
var ErrProjectRollbackHeld = errors.New("state: project rollback is holding cutover")
var ErrProjectRollbackPreparationEnded = errors.New("state: project rollback preparation has ended")

const projectRollbackCheckFreshness = time.Minute

func projectRollbackCutoverOrder(parent ProjectRollbackRecord, child api.RollbackOperation, now time.Time) error {
	if parent.PauseRequestedAt != nil {
		return ErrProjectRollbackPaused
	}
	if parent.ID == "" || parent.Status != "running" || parent.Phase == api.ProjectRollbackReleasing ||
		parent.PreparationDeadline == nil || !now.Before(*parent.PreparationDeadline) {
		return ErrProjectRollbackPreparationEnded
	}
	if parent.Phase != api.ProjectRollbackCutover {
		return ErrProjectRollbackHeld
	}
	if parent.VerifyBeforeCutover {
		for _, step := range parent.Steps {
			if step.Status != "skipped" && len(parent.HealthProbes[step.AppID]) > 0 && !step.PreCutoverHealthPassed() {
				return ErrProjectRollbackHeld
			}
		}
	}
	for _, step := range parent.Steps {
		if step.Status == "skipped" {
			continue
		}
		if step.Status == "rolled_back" {
			if parent.Verify && len(parent.HealthProbes[step.AppID]) != 0 &&
				(!step.Health.PassedFor(parent.StabilizeForSeconds) || step.Health.RouteGeneration == nil) {
				return ErrProjectRollbackHeld
			}
			if parent.VerifyLiveTraffic && len(parent.LiveTrafficPolicies[step.AppID].Routes) > 0 && !step.LiveTraffic.Passed() {
				return ErrProjectRollbackHeld
			}
			continue
		}
		if step.AttemptID == child.ID && step.AppID == child.AppID && step.TargetID == child.TargetDeploymentID &&
			step.CurrentID == child.CurrentDeploymentID {
			return nil
		}
		break
	}
	return ErrProjectRollbackHeld
}

func authorizeProjectRollbackCutover(ctx context.Context, tx pgx.Tx, child api.RollbackOperation) error {
	parent, err := scanProjectRollback(tx.QueryRow(ctx, `
		SELECT version, next_run_at, receipt FROM project_rollback_operations WHERE id = $1 FOR UPDATE`, child.ProjectRollbackID))
	if errors.Is(err, ErrNotFound) {
		return ErrProjectRollbackPreparationEnded
	}
	if err != nil {
		return err
	}
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return err
	}
	if err := projectRollbackCutoverOrder(parent, child, now); err != nil {
		return err
	}
	steps, err := json.Marshal(parent.Steps)
	if err != nil {
		return err
	}
	// Peer app locks fence config, traffic, and receipt changes during this
	// commit. A sibling worker may already hold its app while waiting for
	// the project row, so back off instead of waiting for its lock here.
	if _, err := tx.Exec(ctx, `
		SELECT a.id FROM jsonb_array_elements($1::jsonb) AS step
		JOIN apps a ON a.id = (step->>'app_id')::uuid
		WHERE step->>'status' <> 'skipped' AND a.id <> $2
		ORDER BY a.id FOR UPDATE OF a NOWAIT`, steps, child.AppID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.LockNotAvailable {
			return ErrProjectRollbackHeld
		}
		return err
	}
	var confirmed bool
	err = tx.QueryRow(ctx, `
		SELECT NOT EXISTS (
			SELECT 1 FROM jsonb_array_elements($1::jsonb) AS step
			LEFT JOIN apps a ON a.id = (step->>'app_id')::uuid AND a.project_id = $2 AND a.status <> 'deleted'
			LEFT JOIN deployments live ON live.app_id = a.id AND live.deleted_at IS NULL
			  AND live.id = (CASE WHEN step->>'status' = 'rolled_back' THEN step->>'target_deployment_id'
			                     ELSE step->>'expected_current_deployment_id' END)::uuid
			  AND live.status = 'live' AND live.traffic_percent = 100 AND live.rollout_state = 'complete'
			  AND live.scope IN ('', 'default', 'production')
			LEFT JOIN deployment_rollback_operations peer ON peer.id = (step->>'attempt_id')::uuid
			  AND peer.project_rollback_id = $6
			LEFT JOIN deployments target ON target.id = peer.target_deployment_id AND target.deleted_at IS NULL
			  AND target.status = 'live' AND target.traffic_percent = 0
			WHERE step->>'status' <> 'skipped' AND (
			    live.id IS NULL OR EXISTS (
			        SELECT 1 FROM deployments competing WHERE competing.app_id = a.id AND competing.id <> live.id
			        AND competing.deleted_at IS NULL AND competing.scope IN ('', 'default', 'production')
			        AND competing.status = 'live' AND competing.traffic_percent > 0
			    ) OR (
			    step->>'status' <> 'rolled_back' AND (
			        live.rollout_completed_at IS DISTINCT FROM (step->>'current_served_at')::timestamptz
			        OR (step->>'attempt_id' IS DISTINCT FROM $3 AND (
			            target.id IS NULL OR peer.status <> 'ready'
			            OR peer.receipt->>'code' IS DISTINCT FROM $4
			            OR peer.updated_at < $5
			            OR peer.app_id <> a.id OR peer.current_deployment_id <> live.id
			            OR peer.target_deployment_id <> (step->>'target_deployment_id')::uuid
			        ))
			    ))
			)
		)`, steps, parent.ProjectID, child.ID, api.CodeProjectRollbackPrepared, now.Add(-projectRollbackCheckFreshness), parent.ID).Scan(&confirmed)
	if err != nil {
		return err
	}
	if !confirmed {
		return ErrProjectRollbackHeld
	}
	if parent.Verify {
		err = tx.QueryRow(ctx, `
			SELECT NOT EXISTS (
			    SELECT 1 FROM jsonb_array_elements($1::jsonb) AS step
			    WHERE step->>'status' = 'rolled_back' AND (step ? 'health' OR step ? 'live_traffic')
			      AND NOT EXISTS (
			          SELECT 1 FROM project_rollback_http_recovery r
			          WHERE r.operation_id = $2 AND r.rollback_id = (step->>'attempt_id')::uuid
			            AND (NOT (step ? 'health') OR r.generation = (step #>> '{health,route_generation}')::bigint)
			            AND (NOT (step ? 'live_traffic') OR r.generation = (step #>> '{live_traffic,route_generation}')::bigint)
			            AND r.gateways_applied_at IS NOT NULL AND `+projectRollbackRouteCurrent+`
			      )
			)`, steps, parent.ID).Scan(&confirmed)
		if err != nil {
			return err
		}
		if !confirmed {
			return ErrProjectRollbackHeld
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('faas.project_rollback_verified_cutover', $1, true)`, child.ID); err != nil {
			return err
		}
	}
	if parent.VerifyBeforeCutover {
		if _, err := tx.Exec(ctx, `SELECT set_config('faas.project_rollback_pre_cutover_verified', $1, true)`, child.ID); err != nil {
			return err
		}
	}
	if parent.VerifyLiveTraffic {
		if _, err := tx.Exec(ctx, `SELECT set_config('faas.project_rollback_live_traffic_verified', $1, true)`, child.ID); err != nil {
			return err
		}
	}
	if parent.UndoOf == "" {
		if err := holdProjectRollbackUndoArtifactsTx(ctx, tx, child); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `SELECT set_config('faas.project_rollback_cutover', $1, true)`, child.ID)
	return err
}

func (m *MemStore) authorizeProjectRollbackCutoverLocked(child api.RollbackOperation) error {
	parent := m.projectRollbackOperations[child.ProjectRollbackID]
	now := time.Now().UTC()
	if err := projectRollbackCutoverOrder(parent, child, now); err != nil {
		return err
	}
	for _, step := range parent.Steps {
		if step.Status == "skipped" {
			continue
		}
		app := m.apps[step.AppID]
		expected := step.CurrentID
		if step.Status == "rolled_back" {
			expected = step.TargetID
		}
		live := m.deployments[expected]
		if app.ProjectID != parent.ProjectID || app.Status == AppDeleted || live.ID == "" || live.AppID != app.ID ||
			live.DeletedAt != nil || live.Status != DeployLive || live.TrafficPercent != 100 || live.RolloutState != "complete" ||
			!routeRemovalProductionScope(live.Scope) {
			return ErrProjectRollbackHeld
		}
		for _, competing := range m.liveDeploymentsLocked(app.ID) {
			if competing.ID != live.ID && competing.DeletedAt == nil && routeRemovalProductionScope(competing.Scope) && competing.TrafficPercent > 0 {
				return ErrProjectRollbackHeld
			}
		}
		if step.Status == "rolled_back" {
			continue
		}
		if !sameProjectServingPeriod(live.RolloutCompletedAt, step.CurrentServedAt) {
			return ErrProjectRollbackHeld
		}
		if step.AttemptID == child.ID {
			continue
		}
		peer := m.checkedRollbacks[step.AttemptID]
		target := m.deployments[step.TargetID]
		if peer.ProjectRollbackID != parent.ID || peer.AppID != app.ID || peer.CurrentDeploymentID != live.ID ||
			peer.TargetDeploymentID != target.ID || peer.Status != "ready" || peer.Code != api.CodeProjectRollbackPrepared ||
			peer.UpdatedAt.Before(now.Add(-projectRollbackCheckFreshness)) || target.Status != DeployLive ||
			target.DeletedAt != nil || target.TrafficPercent != 0 {
			return ErrProjectRollbackHeld
		}
	}
	return nil
}
