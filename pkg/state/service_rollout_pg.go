package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type pgServiceRolloutLiveRow struct {
	id        string
	createdAt time.Time
	service   bool
}

func previousServiceRolloutRow(target Deployment, rows []pgServiceRolloutLiveRow) (pgServiceRolloutLiveRow, bool) {
	var previous pgServiceRolloutLiveRow
	found := false
	for _, row := range rows {
		if row.id == target.ID || row.service {
			continue
		}
		if !row.createdAt.Before(target.CreatedAt) && !target.CreatedAt.IsZero() {
			continue
		}
		if !found || row.createdAt.After(previous.createdAt) ||
			(row.createdAt.Equal(previous.createdAt) && row.id > previous.id) {
			previous = row
			found = true
		}
	}
	return previous, found
}

func validServiceRolloutHandoff(h ServiceRolloutHandoff) bool {
	if h.Action != ServiceRolloutActionPromote && h.Action != ServiceRolloutActionAbort {
		return false
	}
	switch h.Phase {
	case ServiceRolloutPhasePending, ServiceRolloutPhaseRouting, ServiceRolloutPhaseDraining, ServiceRolloutPhaseComplete:
		return h.RetryCount >= 0
	default:
		return false
	}
}

// loadAndLockServiceRollout locks in the same order as CreateDeployment:
// parent app first, then the live rows for the target scope. This makes the
// finalizer/aborter serialize with a deploy that is trying to change the same
// generation set.
func (s *PgStore) loadAndLockServiceRollout(ctx context.Context, tx pgx.Tx, id string) (Deployment, []pgServiceRolloutLiveRow, error) {
	var appID string
	if err := tx.QueryRow(ctx, `select app_id from deployments where id = $1`, id).Scan(&appID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Deployment{}, nil, ErrNotFound
		}
		return Deployment{}, nil, fmt.Errorf("state: service rollout app lookup: %w", err)
	}
	var locked int
	if err := tx.QueryRow(ctx, `select 1 from apps where id = $1 for update`, appID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Deployment{}, nil, ErrNotFound
		}
		return Deployment{}, nil, fmt.Errorf("state: service rollout app lock: %w", err)
	}
	target, err := scanDeploymentWithRootfs(tx.QueryRow(ctx,
		`select `+deploymentSelectColumnsWithRootfs+` from deployments where id = $1 for update`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrNotFound) {
			return Deployment{}, nil, ErrNotFound
		}
		return Deployment{}, nil, fmt.Errorf("state: service rollout target load: %w", err)
	}
	if target.Status != DeployLive || !IsServiceRollout(target) {
		return Deployment{}, nil, ErrServiceRolloutInvalid
	}
	rows, err := tx.Query(ctx,
		`select id, created_at, canary_total_steps, rollout_state
		   from deployments
		  where app_id = $1 and scope = $2 and status = 'live'
		  order by id
		  for update`, target.AppID, target.Scope)
	if err != nil {
		return Deployment{}, nil, fmt.Errorf("state: service rollout live lock: %w", err)
	}
	defer rows.Close()
	live := make([]pgServiceRolloutLiveRow, 0)
	for rows.Next() {
		var row pgServiceRolloutLiveRow
		var canaryTotal int
		var rolloutState string
		if err := rows.Scan(&row.id, &row.createdAt, &canaryTotal, &rolloutState); err != nil {
			return Deployment{}, nil, fmt.Errorf("state: service rollout live scan: %w", err)
		}
		row.service = canaryTotal == 0 && rolloutState == "rolling_out"
		live = append(live, row)
	}
	if err := rows.Err(); err != nil {
		return Deployment{}, nil, fmt.Errorf("state: service rollout live iterate: %w", err)
	}
	return target, live, nil
}

// FinalizeServiceRollout promotes a ready service generation in one
// transaction. Older live rows are removed from the serving set before the
// target is stamped complete, which also satisfies the overlap index.
func (s *PgStore) FinalizeServiceRollout(ctx context.Context, id string) (Deployment, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Deployment{}, fmt.Errorf("state: finalize service rollout begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	target, _, err := s.loadAndLockServiceRollout(ctx, tx, id)
	if err != nil {
		return Deployment{}, err
	}
	if target.ServiceRolloutHandoff.ActiveAbort() {
		return target, ErrServiceRolloutInvalid
	}
	if _, err := tx.Exec(ctx,
		`update deployments
		    set status = 'superseded', traffic_percent = 0
		  where app_id = $1 and scope = $2 and status = 'live' and id <> $3`,
		target.AppID, target.Scope, target.ID); err != nil {
		return Deployment{}, fmt.Errorf("state: finalize service rollout supersede siblings: %w", err)
	}
	now := time.Now().UTC()
	handoff := target.ServiceRolloutHandoff
	handoff.Action = ServiceRolloutActionPromote
	handoff.Phase = ServiceRolloutPhaseComplete
	handoff.LastError = ""
	handoff.CompletedAt = &now
	handoff.UpdatedAt = &now
	handoffJSON, err := json.Marshal(handoff)
	if err != nil {
		return Deployment{}, fmt.Errorf("state: finalize service rollout encode handoff: %w", err)
	}
	updated, err := scanDeploymentWithRootfs(tx.QueryRow(ctx,
		`update deployments set
		    traffic_percent = 100,
		    rollout_state = 'complete',
		    rollout_completed_at = $2,
		    rollout_aborted_at = null,
		    rollout_aborted_reason = '',
		    service_rollout_handoff = $3::jsonb
		  where id = $1
		  returning `+deploymentSelectColumnsWithRootfs, target.ID, now, handoffJSON))
	if err != nil {
		return Deployment{}, fmt.Errorf("state: finalize service rollout update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Deployment{}, fmt.Errorf("state: finalize service rollout commit: %w", err)
	}
	return updated, nil
}

// BeginServiceRolloutCutover makes the ready candidate the only
// positive-weight live generation while deliberately retaining the previous
// generation as live. Gateways refresh and acknowledge this state before
// FinalizeServiceRollout supersedes the predecessor.
func (s *PgStore) BeginServiceRolloutCutover(ctx context.Context, id string) (Deployment, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Deployment{}, fmt.Errorf("state: begin service rollout cutover: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	target, rows, err := s.loadAndLockServiceRollout(ctx, tx, id)
	if err != nil {
		return Deployment{}, err
	}
	if target.ServiceRolloutHandoff.ActiveAbort() {
		return target, ErrServiceRolloutInvalid
	}
	if _, err := tx.Exec(ctx,
		`update deployments
		    set traffic_percent = case when id = $3 then 100 else 0 end
		  where app_id = $1 and scope = $2 and status = 'live'`,
		target.AppID, target.Scope, target.ID); err != nil {
		return Deployment{}, fmt.Errorf("state: begin service rollout cutover weights: %w", err)
	}
	previous, _ := previousServiceRolloutRow(target, rows)
	now := time.Now().UTC()
	handoff := target.ServiceRolloutHandoff
	if handoff.StartedAt == nil || handoff.Action != ServiceRolloutActionPromote {
		handoff.StartedAt = &now
	}
	handoff.Action = ServiceRolloutActionPromote
	handoff.Phase = ServiceRolloutPhaseRouting
	handoff.PredecessorDeploymentID = previous.id
	handoff.RetryCount++
	handoff.LastError = ""
	handoff.UpdatedAt = &now
	handoff.CompletedAt = nil
	handoffJSON, err := json.Marshal(handoff)
	if err != nil {
		return Deployment{}, fmt.Errorf("state: begin service rollout cutover encode handoff: %w", err)
	}
	updated, err := scanDeploymentWithRootfs(tx.QueryRow(ctx,
		`update deployments set service_rollout_handoff = $2::jsonb
		  where id = $1 returning `+deploymentSelectColumnsWithRootfs, target.ID, handoffJSON))
	if err != nil {
		return Deployment{}, fmt.Errorf("state: begin service rollout cutover reload: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Deployment{}, fmt.Errorf("state: begin service rollout cutover commit: %w", err)
	}
	return updated, nil
}

// BeginServiceRolloutAbort is the reverse of BeginServiceRolloutCutover. It
// restores the recorded predecessor to 100% while both generations remain
// live, making the traffic change recoverable until the gateway and request
// drain barriers complete.
func (s *PgStore) BeginServiceRolloutAbort(ctx context.Context, id string) (Deployment, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Deployment{}, fmt.Errorf("state: begin service rollout abort: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	target, rows, err := s.loadAndLockServiceRollout(ctx, tx, id)
	if err != nil {
		return Deployment{}, err
	}
	if !target.ServiceRolloutHandoff.ActiveAbort() {
		return target, ErrServiceRolloutInvalid
	}
	previous, found := previousServiceRolloutRow(target, rows)
	if !found || (target.ServiceRolloutHandoff.PredecessorDeploymentID != "" && target.ServiceRolloutHandoff.PredecessorDeploymentID != previous.id) {
		return target, ErrServiceRolloutInvalid
	}
	if _, err := tx.Exec(ctx,
		`update deployments
		    set traffic_percent = case when id = $3 then 100 else 0 end
		  where app_id = $1 and scope = $2 and status = 'live'`,
		target.AppID, target.Scope, previous.id); err != nil {
		return Deployment{}, fmt.Errorf("state: begin service rollout abort weights: %w", err)
	}
	now := time.Now().UTC()
	handoff := target.ServiceRolloutHandoff
	handoff.Phase = ServiceRolloutPhaseRouting
	handoff.PredecessorDeploymentID = previous.id
	handoff.RetryCount++
	handoff.LastError = ""
	handoff.UpdatedAt = &now
	handoff.CompletedAt = nil
	handoffJSON, err := json.Marshal(handoff)
	if err != nil {
		return Deployment{}, fmt.Errorf("state: begin service rollout abort encode handoff: %w", err)
	}
	updated, err := scanDeploymentWithRootfs(tx.QueryRow(ctx,
		`update deployments set service_rollout_handoff = $2::jsonb
		  where id = $1 returning `+deploymentSelectColumnsWithRootfs, target.ID, handoffJSON))
	if err != nil {
		return Deployment{}, fmt.Errorf("state: begin service rollout abort reload: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Deployment{}, fmt.Errorf("state: begin service rollout abort commit: %w", err)
	}
	return updated, nil
}

func (s *PgStore) UpdateServiceRolloutHandoff(ctx context.Context, id string, handoff ServiceRolloutHandoff) (Deployment, error) {
	if !validServiceRolloutHandoff(handoff) {
		return Deployment{}, ErrServiceRolloutInvalid
	}
	payload, err := json.Marshal(handoff)
	if err != nil {
		return Deployment{}, fmt.Errorf("state: encode service rollout handoff: %w", err)
	}
	updated, err := scanDeploymentWithRootfs(s.pool.QueryRow(ctx,
		`update deployments
		    set service_rollout_handoff = $2::jsonb
		  where id = $1 and status = 'live' and canary_total_steps = 0 and rollout_state = 'rolling_out'
		    and (coalesce(service_rollout_handoff->>'action', '') <> 'abort'
		         or coalesce(service_rollout_handoff->>'phase', '') = 'complete'
		         or $3 = 'abort')
		    and (coalesce(service_rollout_handoff->>'action', '') <> $3
		         or coalesce((service_rollout_handoff->>'generation')::bigint, 0) <= $4)
		  returning `+deploymentSelectColumnsWithRootfs, id, payload, handoff.Action, handoff.Generation))
	if errors.Is(err, ErrNotFound) {
		return Deployment{}, ErrServiceRolloutInvalid
	}
	if err != nil {
		return Deployment{}, fmt.Errorf("state: update service rollout handoff: %w", err)
	}
	return updated, nil
}

// AbortServiceRollout restores the newest older stable generation and closes
// the failed target atomically. The target remains in deployment history with
// an explicit aborted reason for operator visibility.
func (s *PgStore) AbortServiceRollout(ctx context.Context, id, reason string) (Deployment, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Deployment{}, fmt.Errorf("state: abort service rollout begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	target, rows, err := s.loadAndLockServiceRollout(ctx, tx, id)
	if err != nil {
		return Deployment{}, err
	}
	previous, _ := previousServiceRolloutRow(target, rows)
	previousID := previous.id
	for _, row := range rows {
		if row.id == id {
			continue
		}
		status := string(DeploySuperseded)
		traffic := 0
		if row.id == previousID {
			status = string(DeployLive)
			traffic = 100
		}
		if _, err := tx.Exec(ctx,
			`update deployments set status = $2, traffic_percent = $3 where id = $1`,
			row.id, status, traffic); err != nil {
			return Deployment{}, fmt.Errorf("state: abort service rollout sibling %s: %w", row.id, err)
		}
	}
	now := time.Now().UTC()
	handoff := target.ServiceRolloutHandoff
	handoff.Action = ServiceRolloutActionAbort
	handoff.Phase = ServiceRolloutPhaseComplete
	handoff.Reason = reason
	handoff.LastError = ""
	handoff.UpdatedAt = &now
	handoff.CompletedAt = &now
	handoffJSON, err := json.Marshal(handoff)
	if err != nil {
		return Deployment{}, fmt.Errorf("state: abort service rollout encode handoff: %w", err)
	}
	updated, err := scanDeploymentWithRootfs(tx.QueryRow(ctx,
		`update deployments set
		    status = 'superseded',
		    traffic_percent = 0,
		    rollout_state = 'aborted',
		    rollout_completed_at = null,
		    rollout_aborted_at = $2,
		    rollout_aborted_reason = $3,
		    service_rollout_handoff = $4::jsonb
		  where id = $1
		  returning `+deploymentSelectColumnsWithRootfs, target.ID, now, reason, handoffJSON))
	if err != nil {
		return Deployment{}, fmt.Errorf("state: abort service rollout update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Deployment{}, fmt.Errorf("state: abort service rollout commit: %w", err)
	}
	return updated, nil
}
