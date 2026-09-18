package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// UpsertJobRegistryCredential stores or replaces a sealed credential while
// verifying that the job belongs to accountID and is not soft-deleted.
func (s *PgStore) UpsertJobRegistryCredential(ctx context.Context, accountID, jobID, registry, username string, passwordEncrypted []byte) error {
	var id string
	err := s.pool.QueryRow(ctx, `
		insert into job_registry_credentials (account_id, job_id, registry, username, password_encrypted)
		select $1::uuid, j.id, $3, $4, $5
		from jobs j
		where j.id = $2::uuid and j.account_id = $1::uuid and j.status <> 'deleted'
		on conflict (job_id, registry) do update
		set username = excluded.username,
		    password_encrypted = excluded.password_encrypted,
		    updated_at = now()
		returning id`, accountID, jobID, registry, username, passwordEncrypted).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("state: upsert job registry credential: %w", err)
	}
	return nil
}

// GetJobRegistryCredential returns the sealed credential for one job and
// registry, scoped to the owning account.
func (s *PgStore) GetJobRegistryCredential(ctx context.Context, accountID, jobID, registry string) (JobRegistryCredential, error) {
	var r JobRegistryCredential
	err := s.pool.QueryRow(ctx, `
		select c.id, c.account_id, c.job_id, c.registry, c.username,
		       c.password_encrypted, c.created_at, c.updated_at, c.last_used_at
		from job_registry_credentials c
		join jobs j on j.id = c.job_id
		where c.account_id = $1::uuid and c.job_id = $2::uuid
		  and c.registry = $3 and j.status <> 'deleted'`, accountID, jobID, registry).Scan(
		&r.ID, &r.AccountID, &r.JobID, &r.Registry, &r.Username,
		&r.PasswordEncrypted, &r.CreatedAt, &r.UpdatedAt, &r.LastUsedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return JobRegistryCredential{}, ErrNotFound
		}
		return JobRegistryCredential{}, fmt.Errorf("state: get job registry credential: %w", err)
	}
	return r, nil
}

// ListJobRegistryCredentials returns all credentials for an owned job in
// deterministic registry order. The encrypted password remains server-side.
func (s *PgStore) ListJobRegistryCredentials(ctx context.Context, accountID, jobID string) ([]JobRegistryCredential, error) {
	rows, err := s.pool.Query(ctx, `
		select c.id, c.account_id, c.job_id, c.registry, c.username,
		       c.password_encrypted, c.created_at, c.updated_at, c.last_used_at
		from job_registry_credentials c
		join jobs j on j.id = c.job_id
		where c.account_id = $1::uuid and c.job_id = $2::uuid
		  and j.status <> 'deleted'
		order by c.registry asc`, accountID, jobID)
	if err != nil {
		return nil, fmt.Errorf("state: list job registry credentials: %w", err)
	}
	defer rows.Close()
	out := make([]JobRegistryCredential, 0)
	for rows.Next() {
		var r JobRegistryCredential
		if err := rows.Scan(&r.ID, &r.AccountID, &r.JobID, &r.Registry, &r.Username,
			&r.PasswordEncrypted, &r.CreatedAt, &r.UpdatedAt, &r.LastUsedAt); err != nil {
			return nil, fmt.Errorf("state: scan job registry credential: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteJobRegistryCredential removes one credential scoped to an owned job.
func (s *PgStore) DeleteJobRegistryCredential(ctx context.Context, accountID, jobID, registry string) error {
	tag, err := s.pool.Exec(ctx, `
		delete from job_registry_credentials c
		using jobs j
		where c.job_id = j.id and c.account_id = $1::uuid and c.job_id = $2::uuid
		  and c.registry = $3 and j.status <> 'deleted'`, accountID, jobID, registry)
	if err != nil {
		return fmt.Errorf("state: delete job registry credential: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// JobRegistryCredentialQuotaCheck combines the per-job count and existence
// probe so a replacement does not consume quota.
func (s *PgStore) JobRegistryCredentialQuotaCheck(ctx context.Context, accountID, jobID, registry string) (int, bool, error) {
	var count int
	var exists bool
	err := s.pool.QueryRow(ctx, `
		with counts as (
			select count(*) as n, bool_or(c.registry = $3) as exists
			from job_registry_credentials c
			join jobs j on j.id = c.job_id
			where c.account_id = $1::uuid and c.job_id = $2::uuid
			  and j.status <> 'deleted'
		)
		select coalesce(n, 0), coalesce(exists, false) from counts`, accountID, jobID, registry).Scan(&count, &exists)
	if err != nil {
		return 0, false, fmt.Errorf("state: job registry credential quota check: %w", err)
	}
	return count, exists, nil
}

// MarkJobRegistryCredentialUsed stamps the last successful authenticated pull.
func (s *PgStore) MarkJobRegistryCredentialUsed(ctx context.Context, accountID, jobID, registry string) error {
	tag, err := s.pool.Exec(ctx, `
		update job_registry_credentials c
		set last_used_at = now(), updated_at = now()
		from jobs j
		where c.job_id = j.id and c.account_id = $1::uuid and c.job_id = $2::uuid
		  and c.registry = $3 and j.status <> 'deleted'`, accountID, jobID, registry)
	if err != nil {
		return fmt.Errorf("state: mark job registry credential used: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
