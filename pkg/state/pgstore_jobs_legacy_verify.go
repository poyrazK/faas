package state

import (
	"context"
	"fmt"
	"time"
)

// JobClaimLegacyArtifactVerification leases only rows parked by the legacy
// repair migration. It never enters the ordinary OCI materialization queue.
func (s *PgStore) JobClaimLegacyArtifactVerification(ctx context.Context, limit int, owner string, lease time.Duration) ([]Job, error) {
	if limit <= 0 {
		limit = 64
	}
	if owner == "" {
		owner = "imaged"
	}
	if lease <= 0 {
		lease = 15 * time.Minute
	}
	rows, err := s.pool.Query(ctx,
		`with candidates as (
			select id as candidate_id from jobs
			 where status <> 'deleted'
			   and image_materialization_status = 'verifying_legacy'
			   and image_storage_key = image_ref
			   and (image_materialization_next_attempt_at is null or image_materialization_next_attempt_at <= now())
			   and (image_materialization_lease_until is null or image_materialization_lease_until <= now())
			 order by updated_at asc, id asc
			 limit $1
			 for update skip locked
		)
		 update jobs j
		    set image_materialization_attempts = j.image_materialization_attempts + 1,
		        image_materialization_lease_owner = $2,
		        image_materialization_lease_until = now() + $3::interval,
		        updated_at = now()
		   from candidates c
		  where j.id = c.candidate_id
		 returning `+jobSelectCols,
		limit, owner, lease.String())
	if err != nil {
		return nil, fmt.Errorf("state: claim legacy job artifact verification: %w", err)
	}
	defer rows.Close()
	return scanJobs(rows)
}

// JobFinishLegacyArtifactVerification publishes readiness only after an
// authoritative storage existence probe. A missing object is terminally
// failed and loses its stale storage key; neither path invents an OCI digest.
func (s *PgStore) JobFinishLegacyArtifactVerification(ctx context.Context, id, sourceRef, owner string, found bool, reason string) (Job, error) {
	if !found && reason == "" {
		return Job{}, fmt.Errorf("state: missing legacy job artifact requires a reason")
	}
	row := s.pool.QueryRow(ctx,
		`update jobs set
		   image_materialization_status = case when $4::boolean then 'ready' else 'failed' end,
		   image_storage_key = case when $4::boolean then image_storage_key else null end,
		   image_materialization_error = case when $4::boolean then null else $5::text end,
		   image_materialized_at = case when $4::boolean then now() else null end,
		   image_materialization_next_attempt_at = null,
		   image_materialization_lease_owner = null,
		   image_materialization_lease_until = null,
		   updated_at = now()
		 where id = $1::uuid and image_ref = $2 and image_storage_key = $2
		   and status <> 'deleted' and image_materialization_status = 'verifying_legacy'
		   and image_materialization_lease_owner = $3
		   and image_materialization_lease_until > now()
		 returning `+jobSelectCols,
		id, sourceRef, owner, found, reason)
	job, err := scanJob(row)
	if err != nil {
		return Job{}, fmt.Errorf("state: finish legacy job artifact verification %s: %w", id, err)
	}
	return job, nil
}

// JobRetryLegacyArtifactVerification preserves the fail-closed state when
// the storage probe itself is unavailable. It clears the lease for a later
// bounded retry and never permits the job to dispatch in the meantime.
func (s *PgStore) JobRetryLegacyArtifactVerification(ctx context.Context, id, sourceRef, owner, reason string, retryAt time.Time) (Job, error) {
	if reason == "" {
		return Job{}, fmt.Errorf("state: legacy job artifact retry requires a reason")
	}
	row := s.pool.QueryRow(ctx,
		`update jobs set
		   image_materialization_error = $4,
		   image_materialization_next_attempt_at = $5::timestamptz,
		   image_materialization_lease_owner = null,
		   image_materialization_lease_until = null,
		   updated_at = now()
		 where id = $1::uuid and image_ref = $2 and image_storage_key = $2
		   and status <> 'deleted' and image_materialization_status = 'verifying_legacy'
		   and image_materialization_lease_owner = $3
		   and image_materialization_lease_until > now()
		 returning `+jobSelectCols,
		id, sourceRef, owner, reason, retryAt.UTC())
	job, err := scanJob(row)
	if err != nil {
		return Job{}, fmt.Errorf("state: retry legacy job artifact verification %s: %w", id, err)
	}
	return job, nil
}
