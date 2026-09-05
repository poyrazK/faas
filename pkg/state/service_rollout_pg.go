package state

import (
	"context"
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
	if _, err := tx.Exec(ctx,
		`update deployments
		    set status = 'superseded', traffic_percent = 0
		  where app_id = $1 and scope = $2 and status = 'live' and id <> $3`,
		target.AppID, target.Scope, target.ID); err != nil {
		return Deployment{}, fmt.Errorf("state: finalize service rollout supersede siblings: %w", err)
	}
	now := time.Now().UTC()
	updated, err := scanDeploymentWithRootfs(tx.QueryRow(ctx,
		`update deployments set
		    traffic_percent = 100,
		    rollout_state = 'complete',
		    rollout_completed_at = $2,
		    rollout_aborted_at = null,
		    rollout_aborted_reason = ''
		  where id = $1
		  returning `+deploymentSelectColumnsWithRootfs, target.ID, now))
	if err != nil {
		return Deployment{}, fmt.Errorf("state: finalize service rollout update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Deployment{}, fmt.Errorf("state: finalize service rollout commit: %w", err)
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
	var previousID string
	var previousAt time.Time
	for _, row := range rows {
		if row.id == id || row.service {
			continue
		}
		if !row.createdAt.Before(target.CreatedAt) && !target.CreatedAt.IsZero() {
			continue
		}
		if previousID == "" || row.createdAt.After(previousAt) ||
			(row.createdAt.Equal(previousAt) && row.id > previousID) {
			previousID = row.id
			previousAt = row.createdAt
		}
	}
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
	updated, err := scanDeploymentWithRootfs(tx.QueryRow(ctx,
		`update deployments set
		    status = 'superseded',
		    traffic_percent = 0,
		    rollout_state = 'aborted',
		    rollout_completed_at = null,
		    rollout_aborted_at = $2,
		    rollout_aborted_reason = $3
		  where id = $1
		  returning `+deploymentSelectColumnsWithRootfs, target.ID, now, reason))
	if err != nil {
		return Deployment{}, fmt.Errorf("state: abort service rollout update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Deployment{}, fmt.Errorf("state: abort service rollout commit: %w", err)
	}
	return updated, nil
}
