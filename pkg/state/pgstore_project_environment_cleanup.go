package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PgStore) ClaimNextProjectEnvironmentCleanup(
	ctx context.Context,
	leaseToken string,
	now time.Time,
	leaseDuration time.Duration,
) (ProjectEnvironmentCleanupJob, error) {
	if leaseToken == "" || leaseDuration <= 0 {
		return ProjectEnvironmentCleanupJob{}, ErrInvalidArgument
	}
	now = now.UTC()
	row := s.pool.QueryRow(ctx, `
		with candidate as (
			select id
			  from project_environment_cleanup_jobs
			 where next_attempt_at <= $1
			   and (lease_until is null or lease_until <= $1)
			 order by next_attempt_at, created_at, id
			 for update skip locked
			 limit 1
		)
		update project_environment_cleanup_jobs j
		   set lease_token = $2,
		       lease_until = $3,
		       attempt_count = j.attempt_count + 1
		  from candidate c
		 where j.id = c.id
		returning j.id, j.account_id, j.project_id, j.environment_slug, j.resources,
		          j.attempt_count, j.next_attempt_at, j.lease_token, j.lease_until, j.created_at
	`, now, leaseToken, now.Add(leaseDuration))
	return scanProjectEnvironmentCleanupJob(row)
}

func (s *PgStore) RetryProjectEnvironmentCleanup(ctx context.Context, id, leaseToken string, nextAttemptAt time.Time) error {
	if id == "" || leaseToken == "" {
		return ErrInvalidArgument
	}
	tag, err := s.pool.Exec(ctx, `
		update project_environment_cleanup_jobs
		   set next_attempt_at = $3, lease_token = '', lease_until = null
		 where id = $1 and lease_token = $2
	`, id, leaseToken, nextAttemptAt.UTC())
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) CompleteProjectEnvironmentCleanup(ctx context.Context, id, leaseToken string) error {
	if id == "" || leaseToken == "" {
		return ErrInvalidArgument
	}
	tag, err := s.pool.Exec(ctx, `
		delete from project_environment_cleanup_jobs
		 where id = $1 and lease_token = $2
	`, id, leaseToken)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

func scanProjectEnvironmentCleanupJob(row pgx.Row) (ProjectEnvironmentCleanupJob, error) {
	var job ProjectEnvironmentCleanupJob
	var resources []byte
	err := row.Scan(
		&job.ID, &job.AccountID, &job.ProjectID, &job.EnvironmentSlug, &resources,
		&job.AttemptCount, &job.NextAttemptAt, &job.LeaseToken, &job.LeaseUntil, &job.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProjectEnvironmentCleanupJob{}, ErrNotFound
		}
		return ProjectEnvironmentCleanupJob{}, mapErr(err)
	}
	if err := json.Unmarshal(resources, &job.Resources); err != nil {
		return ProjectEnvironmentCleanupJob{}, fmt.Errorf("state: decode project environment cleanup resources: %w", err)
	}
	return job, nil
}
