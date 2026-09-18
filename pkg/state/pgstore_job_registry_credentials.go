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
