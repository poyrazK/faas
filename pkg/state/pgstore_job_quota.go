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

var _ JobQuotaCreator = (*PgStore)(nil)
