package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// JobCreateIfUnderQuota serializes the JobMaxPerAccount check and insert on
// the owning account row. Locking the account (rather than the jobs count)
// gives concurrent creates a stable admission key without changing the
// jobs table's customer-facing indexes.
func (s *PgStore) JobCreateIfUnderQuota(ctx context.Context, accountID, name, kind, imageRef string, command []string, ramMB, taskTimeoutSec, maxParallelism, retryMax int, envOverrides json.RawMessage, limit int) (Job, error) {
	if len(envOverrides) == 0 {
		envOverrides = json.RawMessage("{}")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Job{}, fmt.Errorf("state: begin job quota tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var lockedAccount string
	if err := tx.QueryRow(ctx,
		`select id from accounts where id = $1::uuid for update`, accountID).Scan(&lockedAccount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Job{}, ErrNotFound
		}
		return Job{}, fmt.Errorf("state: lock account for job quota: %w", err)
	}

	var count int
	if err := tx.QueryRow(ctx,
		`select count(*) from jobs where account_id = $1::uuid and status <> 'deleted'`, accountID).Scan(&count); err != nil {
		return Job{}, fmt.Errorf("state: count jobs for account %s: %w", accountID, err)
	}
	if count >= limit {
		return Job{}, &JobQuotaError{Scope: JobQuotaScopePerAccount, Limit: limit, Observed: count}
	}

	row := tx.QueryRow(ctx,
		`insert into jobs (account_id, kind, name, image_ref, ram_mb,
		                  task_timeout_s, max_parallelism, retry_max,
		                  env_overrides, command)
		 values ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10)
		 returning `+jobSelectCols,
		accountID, kind, name, imageRef, ramMB, taskTimeoutSec, maxParallelism, retryMax, []byte(envOverrides), command)
	created, err := scanJob(row)
	if err != nil {
		return Job{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Job{}, fmt.Errorf("state: commit job quota tx: %w", err)
	}
	return created, nil
}

// JobCreateScheduledIfUnderQuota persists the recurring definition together
// with the job while holding the same account lock used for quota admission.
func (s *PgStore) JobCreateScheduledIfUnderQuota(ctx context.Context, job Job, limit int) (Job, error) {
	if len(job.EnvOverrides) == 0 {
		job.EnvOverrides = json.RawMessage("{}")
	}
	if job.CronTimezone == "" {
		job.CronTimezone = "UTC"
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Job{}, fmt.Errorf("state: begin scheduled job quota tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var lockedAccount string
	if err := tx.QueryRow(ctx,
		`select id from accounts where id = $1::uuid for update`, job.AccountID).Scan(&lockedAccount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Job{}, ErrNotFound
		}
		return Job{}, fmt.Errorf("state: lock account for scheduled job quota: %w", err)
	}
	var count int
	if err := tx.QueryRow(ctx,
		`select count(*) from jobs where account_id = $1::uuid and status <> 'deleted'`, job.AccountID).Scan(&count); err != nil {
		return Job{}, fmt.Errorf("state: count jobs for account %s: %w", job.AccountID, err)
	}
	if count >= limit {
		return Job{}, &JobQuotaError{Scope: JobQuotaScopePerAccount, Limit: limit, Observed: count}
	}
	row := tx.QueryRow(ctx,
		`insert into jobs (account_id, kind, name, image_ref, ram_mb,
		                  task_timeout_s, max_parallelism, retry_max,
		                  env_overrides, command, cron_schedule, cron_timezone)
		 values ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10, $11, $12)
		 returning `+jobSelectCols,
		job.AccountID, job.Kind, job.Name, job.ImageRef, job.RAMMB,
		job.TaskTimeoutS, job.MaxParallelism, job.RetryMax,
		[]byte(job.EnvOverrides), job.Command, job.CronSchedule, job.CronTimezone)
	created, err := scanJob(row)
	if err != nil {
		return Job{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Job{}, fmt.Errorf("state: commit scheduled job create: %w", err)
	}
	return created, nil
}

var _ JobQuotaCreator = (*PgStore)(nil)
