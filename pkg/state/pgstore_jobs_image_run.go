package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// imageRunIdleJobsSQL selects live runner jobs without queued or claimed
// tasks and without a live task VM, least recently used first. The predicate
// matches JobSoftDelete's active-work guard.
const imageRunIdleJobsSQL = `select id from jobs j
	 where j.account_id = $1::uuid and j.kind = 'image_run' and j.status <> 'deleted'
	   and not exists (select 1 from job_runs r join job_tasks t on t.run_id = r.id
	                    where r.job_id = j.id and t.status in ('queued','claimed'))
	   and not exists (select 1 from instances i where i.job_id = j.id and i.kind = 'job_task'
	                    and i.state in ('waking','cold_booting','running'))
	 order by j.updated_at, j.id
	 limit $2
	 for update of j skip locked`

// JobEnsureImageRun implements ImageRunJobStore (ADR-1161). The account row
// lock serializes concurrent ensures for one account, so the cache count,
// eviction and insert cannot interleave.
func (s *PgStore) JobEnsureImageRun(ctx context.Context, in ImageRunEnsure) (ImageRunEnsureResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ImageRunEnsureResult{}, fmt.Errorf("state: begin image run tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var lockedAccount string
	if err := tx.QueryRow(ctx, `select id from accounts where id = $1::uuid for update`, in.AccountID).Scan(&lockedAccount); err != nil {
		return ImageRunEnsureResult{}, mapErr(err)
	}
	existing, err := scanJob(tx.QueryRow(ctx, `select `+jobSelectCols+` from jobs
		 where account_id = $1::uuid and name = $2 and status <> 'deleted' for update`, in.AccountID, in.Name))
	var res ImageRunEnsureResult
	switch {
	case err == nil:
		res, err = touchImageRunJob(ctx, tx, existing, in)
	case errors.Is(err, ErrNotFound):
		res, err = createImageRunJob(ctx, tx, in)
	}
	if err != nil {
		return ImageRunEnsureResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ImageRunEnsureResult{}, fmt.Errorf("state: commit image run tx: %w", err)
	}
	return res, nil
}

func touchImageRunJob(ctx context.Context, tx pgx.Tx, job Job, in ImageRunEnsure) (ImageRunEnsureResult, error) {
	if job.Kind != JobKindImageRun || job.ImageRef != in.ImageRef {
		return ImageRunEnsureResult{}, fmt.Errorf("%w: job name %q is taken", ErrConflict, in.Name)
	}
	requeue := imageRunNeedsRequeue(job, in.PullAlways)
	updated, err := scanJob(tx.QueryRow(ctx, `update jobs set
		   ram_mb = $2, task_timeout_s = $3, updated_at = now(),
		   image_resolved_digest = case when $4 then null else image_resolved_digest end,
		   image_storage_key = case when $4 then null else image_storage_key end,
		   image_materialization_status = case when $4 then 'pending' else image_materialization_status end,
		   image_materialization_error = case when $4 then null else image_materialization_error end,
		   image_materialized_at = case when $4 then null else image_materialized_at end,
		   image_materialization_attempts = case when $4 then 0 else image_materialization_attempts end,
		   image_materialization_next_attempt_at = case when $4 then null else image_materialization_next_attempt_at end,
		   image_materialization_lease_owner = case when $4 then null else image_materialization_lease_owner end,
		   image_materialization_lease_until = case when $4 then null else image_materialization_lease_until end,
		   image_entrypoint = case when $4 then null else image_entrypoint end,
		   image_cmd = case when $4 then null else image_cmd end
		 where id = $1::uuid
		 returning `+jobSelectCols, job.ID, in.RAMMB, in.TaskTimeoutSec, requeue))
	if err != nil {
		return ImageRunEnsureResult{}, fmt.Errorf("state: touch image run job %s: %w", job.ID, err)
	}
	return ImageRunEnsureResult{Job: updated, Requeued: requeue}, nil
}

func createImageRunJob(ctx context.Context, tx pgx.Tx, in ImageRunEnsure) (ImageRunEnsureResult, error) {
	if err := financialComputeJobCreationAllowed(ctx, tx, in.AccountID); err != nil {
		return ImageRunEnsureResult{}, err
	}
	var count int
	if err := tx.QueryRow(ctx, `select count(*) from jobs
		 where account_id = $1::uuid and kind = 'image_run' and status <> 'deleted'`, in.AccountID).Scan(&count); err != nil {
		return ImageRunEnsureResult{}, fmt.Errorf("state: count image run jobs: %w", err)
	}
	var res ImageRunEnsureResult
	if excess := count - in.MaxCached + 1; excess > 0 {
		evicted, err := evictImageRunJobs(ctx, tx, in.AccountID, excess)
		if err != nil {
			return ImageRunEnsureResult{}, err
		}
		if len(evicted) < excess {
			return ImageRunEnsureResult{}, &ImageRunCacheFullError{Limit: in.MaxCached, Observed: count}
		}
		res.Evicted = evicted
	}
	created, err := scanJob(tx.QueryRow(ctx, `insert into jobs (account_id, kind, name, image_ref, ram_mb,
		                  task_timeout_s, max_parallelism, retry_max, env_overrides, command)
		 values ($1::uuid, 'image_run', $2, $3, $4, $5, 1, 0, '{}'::jsonb, '{}'::text[])
		 returning `+jobSelectCols, in.AccountID, in.Name, in.ImageRef, in.RAMMB, in.TaskTimeoutSec))
	if err != nil {
		return ImageRunEnsureResult{}, mapErr(err)
	}
	res.Job, res.Created = created, true
	return res, nil
}

func evictImageRunJobs(ctx context.Context, tx pgx.Tx, accountID string, limit int) ([]Job, error) {
	rows, err := tx.Query(ctx, imageRunIdleJobsSQL, accountID, limit)
	if err != nil {
		return nil, fmt.Errorf("state: select idle image run jobs: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("state: scan idle image run job: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list idle image run jobs: %w", err)
	}
	evicted := make([]Job, 0, len(ids))
	for _, id := range ids {
		var flipped bool
		if err := tx.QueryRow(ctx, `select soft_delete_job_if_no_live_instances($1::uuid)`, id).Scan(&flipped); err != nil {
			return nil, fmt.Errorf("state: evict image run job %s: %w", id, mapErr(err))
		}
		if !flipped {
			continue
		}
		job, err := scanJob(tx.QueryRow(ctx, `select `+jobSelectCols+` from jobs where id = $1::uuid`, id))
		if err != nil {
			return nil, fmt.Errorf("state: read evicted image run job %s: %w", id, err)
		}
		evicted = append(evicted, job)
	}
	return evicted, nil
}

// JobRecordImageConfig implements JobImageConfigRecorder with the publisher's
// claim fence: the row must still be pending for this source, attempt and
// live lease owner.
func (s *PgStore) JobRecordImageConfig(ctx context.Context, id, sourceRef, owner string, attempt int, entrypoint, cmd []string) error {
	tag, err := s.pool.Exec(ctx, `update jobs set image_entrypoint = $5, image_cmd = $6
		 where id = $1::uuid and image_ref = $2 and status <> 'deleted'
		   and image_materialization_status = 'pending'
		   and image_materialization_attempts = $4
		   and image_materialization_lease_owner = $3
		   and image_materialization_lease_until > now()`,
		id, sourceRef, owner, attempt, nonNilArgs(entrypoint), nonNilArgs(cmd))
	if err != nil {
		return fmt.Errorf("state: record job %s image config: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}
