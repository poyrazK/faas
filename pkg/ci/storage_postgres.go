package ci

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

type storageClaim struct {
	Row   sqlc.CiManagedObject
	Token string
	Ready bool
}

func (c storageClaim) PhysicalKey() string {
	return "ci-v1/" + uuidString(c.Row.AccountID) + "/" + uuidString(c.Row.ID)
}

func (s *PostgresStore) storageTx(ctx context.Context) (pgx.Tx, time.Time, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, time.Time{}, storeError(err)
	}
	if _, err = s.q.LockCIStorageBudget(ctx, tx); err == nil {
		var now time.Time
		now, err = s.executionNow(ctx, tx)
		if err == nil {
			return tx, now, nil
		}
	}
	_ = tx.Rollback(ctx)
	return nil, time.Time{}, storeError(err)
}

func (s *PostgresStore) reserveStorageWrite(ctx context.Context, key string, parsed storageKey, object RuntimeObject) (storageClaim, error) {
	tx, now, err := s.storageTx(ctx)
	if err != nil {
		return storageClaim{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	template, err := s.storageWriteOwner(ctx, tx, parsed, object, now)
	if err != nil {
		return storageClaim{}, err
	}
	row, err := s.q.LockCIManagedObject(ctx, tx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := s.storageQuota(ctx, tx, parsed.Account, object.Bytes); err != nil {
			return storageClaim{}, err
		}
		if parsed.Kind == "source" {
			if _, err := s.q.ReadCICachedSource(ctx, tx, dbUUID(parsed.Attempt)); err == nil {
				return storageClaim{}, ErrConflict
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return storageClaim{}, storeError(err)
			}
		}
		err = s.q.InsertCIManagedObject(ctx, tx, sqlc.InsertCIManagedObjectParams{ObjectKey: key, ID: dbUUID(uuid.NewString()), AccountID: dbUUID(parsed.Account), ExecutionID: dbUUID(parsed.Execution), AttemptID: dbUUID(parsed.Attempt), JobID: dbUUID(template.ID), Kind: parsed.Kind, Digest: object.Digest, Bytes: object.Bytes, CreatedAt: timestamp(now), ExpiresAt: timestamp(storageExpiry(now))})
		if err == nil {
			row, err = s.q.LockCIManagedObject(ctx, tx, key)
		}
	}
	if err != nil {
		return storageClaim{}, storeError(err)
	}
	if row.Digest != object.Digest || row.Bytes != object.Bytes || row.Kind != parsed.Kind {
		return storageClaim{}, ErrConflict
	}
	if row.State == "ready" {
		return storageClaim{Row: row, Ready: true}, storeError(tx.Commit(ctx))
	}
	if row.State != "pending" {
		return storageClaim{}, ErrNotFound
	}
	claim, err := s.beginStorageOperation(ctx, tx, row, now, "pending")
	if err != nil {
		return storageClaim{}, err
	}
	return claim, storeError(tx.Commit(ctx))
}

func storageExpiry(now time.Time) time.Time {
	return now.Add(time.Duration(api.CIStorageRetentionDays) * 24 * time.Hour)
}

func (s *PostgresStore) storageQuota(ctx context.Context, tx pgx.Tx, account string, size int64) error {
	stats, err := s.q.CIStorageStats(ctx, tx, dbUUID(account))
	if err != nil {
		return storeError(err)
	}
	limits := []struct {
		name            string
		observed, limit int64
	}{
		{"storage_bytes", stats.BytesAccount + size, api.CIStorageMaxBytesPerAccount},
		{"global_storage_bytes", stats.BytesTotal + size, api.CIStorageMaxBytesGlobal},
		{"storage_objects", stats.ObjectsAccount + 1, api.CIStorageMaxObjectsPerAccount},
		{"global_storage_objects", stats.ObjectsTotal + 1, api.CIStorageMaxObjectsGlobal},
		{"storage_journal", stats.JournalAccount + 1, api.CIStorageMaxJournalPerAccount},
		{"global_storage_journal", stats.JournalTotal + 1, api.CIStorageMaxJournalGlobal},
	}
	for _, limit := range limits {
		if limit.observed > limit.limit {
			return &LimitError{Resource: limit.name, Limit: int(limit.limit), Observed: int(limit.observed)}
		}
	}
	return nil
}

func (s *PostgresStore) beginStorageOperation(ctx context.Context, tx pgx.Tx, row sqlc.CiManagedObject, now time.Time, state string) (storageClaim, error) {
	if row.OpUntil.Valid && now.Before(row.OpUntil.Time) {
		return storageClaim{}, ErrLeaseBusy
	}
	activity, err := s.q.CIStorageActivity(ctx, tx, sqlc.CIStorageActivityParams{AccountID: row.AccountID, Now: timestamp(now)})
	if err != nil {
		return storageClaim{}, storeError(err)
	}
	if activity.Account >= api.CIStorageMaxIOPerAccount || activity.Total >= api.CIStorageMaxIOGlobal {
		return storageClaim{}, ErrLeaseBusy
	}
	claim := storageClaim{Row: row, Token: uuid.NewString()}
	err = s.q.BeginCIStorageOperation(ctx, tx, sqlc.BeginCIStorageOperationParams{ObjectKey: row.ObjectKey, State: state, OpToken: dbUUID(claim.Token), OpUntil: timestamp(now.Add(time.Duration(api.CIStorageOperationSeconds) * time.Second))})
	return claim, storeError(err)
}

func (s *PostgresStore) commitStorageWrite(ctx context.Context, claim storageClaim) error {
	now, err := s.executionNow(ctx, s.pool)
	if err != nil {
		return err
	}
	count, err := s.q.CommitCIStorageWrite(ctx, s.pool, sqlc.CommitCIStorageWriteParams{ObjectKey: claim.Row.ObjectKey, OpToken: dbUUID(claim.Token), ReadyAt: timestamp(now), ExpiresAt: timestamp(storageExpiry(now))})
	if err == nil && count != 1 {
		return ErrLeaseLost
	}
	return storeError(err)
}

func (s *PostgresStore) releaseStorage(ctx context.Context, claim storageClaim) error {
	return storeError(s.q.ReleaseCIStorageOperation(ctx, s.pool, sqlc.ReleaseCIStorageOperationParams{ObjectKey: claim.Row.ObjectKey, OpToken: dbUUID(claim.Token)}))
}

func (s *PostgresStore) claimStorageRead(ctx context.Context, key string) (storageClaim, error) {
	tx, now, err := s.storageTx(ctx)
	if err != nil {
		return storageClaim{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.q.LockCIManagedObject(ctx, tx, key)
	if err != nil || row.State != "ready" {
		return storageClaim{}, ErrNotFound
	}
	if !now.Before(row.ExpiresAt.Time) {
		execution, err := s.q.GetCIExecution(ctx, tx, sqlc.GetCIExecutionParams{AccountID: row.AccountID, ID: row.ExecutionID})
		if err != nil || terminal(execution.Status) {
			return storageClaim{}, ErrNotFound
		}
	}
	claim, err := s.beginStorageOperation(ctx, tx, row, now, "ready")
	if err != nil {
		return storageClaim{}, err
	}
	return claim, storeError(tx.Commit(ctx))
}

func (s *PostgresStore) storageWriteOwner(ctx context.Context, tx pgx.Tx, key storageKey, object RuntimeObject, now time.Time) (JobsTemplate, error) {
	if _, err := s.q.LockCIAccount(ctx, tx, dbUUID(key.Account)); err != nil {
		return JobsTemplate{}, ErrStorageUnavailable
	}
	stored, err := s.q.LockCIExecution(ctx, tx, sqlc.LockCIExecutionParams{AccountID: dbUUID(key.Account), ID: dbUUID(key.Execution)})
	if err != nil {
		return JobsTemplate{}, ErrStorageUnavailable
	}
	row, err := executionRow(stored)
	if err != nil || row.Execution.Mode != ExecutionJobs || terminal(row.Execution.Status) {
		return JobsTemplate{}, ErrStorageUnavailable
	}
	attempt, err := s.q.LockCIJobsAttempt(ctx, tx, sqlc.LockCIJobsAttemptParams{AccountID: dbUUID(key.Account), ExecutionID: dbUUID(key.Execution), AttemptID: dbUUID(key.Attempt)})
	if err != nil {
		return JobsTemplate{}, ErrStorageUnavailable
	}
	req, template, err := jobsAttemptFor(row.Execution, AttemptKey{AccountID: key.Account, ExecutionID: key.Execution, AttemptID: key.Attempt, JobName: attempt.JobName, Fence: row.Fence})
	if err != nil {
		return template, ErrStorageUnavailable
	}
	if key.Kind == "source" {
		if row.Execution.CancelRequested || attempt.CancelRequested || !now.Before(req.Deadline) || s.jobsAccount(ctx, tx, key.Account) != nil || s.jobsBinding(ctx, tx, row.Execution.Compute.Binding) != nil {
			return template, ErrStorageUnavailable
		}
		current, err := s.jobsTemplate(ctx, tx, key.Account, template.ID, req.Job)
		if err != nil || current != template {
			return template, ErrStorageUnavailable
		}
	} else if err := s.storageOutputReservation(ctx, tx, key, object, now); err != nil {
		return template, err
	}
	return template, nil
}

func (s *PostgresStore) storageOutputReservation(ctx context.Context, tx pgx.Tx, key storageKey, object RuntimeObject, now time.Time) error {
	grant, err := s.q.ReadCIJobsRuntime(ctx, tx, dbUUID(key.Attempt))
	if err != nil || grant.SealedAt.Valid || !now.Before(grant.ExpiresAt.Time) {
		return ErrStorageUnavailable
	}
	task, err := s.q.CIJobsRuntimeTask(ctx, tx, dbUUID(key.Attempt))
	if err != nil || !task.BoundInstance || !task.InstanceState.Valid || task.InstanceState.String != "running" ||
		(task.Status != "claimed" && task.Status != "cancelled") || task.InstanceID != grant.InstanceID {
		return ErrStorageUnavailable
	}
	rows, err := s.q.ListCIJobsRuntimeObjects(ctx, tx, dbUUID(key.Attempt))
	if err != nil {
		return storeError(err)
	}
	for _, row := range rows {
		if "output/"+row.Name == object.Name && row.Bytes == object.Bytes && row.Digest == object.Digest {
			return nil
		}
	}
	return ErrStorageUnavailable
}

func (s *PostgresStore) cachedStorageSource(ctx context.Context, req JobsSourceRequest) (SourceArchive, bool, error) {
	if !validJobsSourceRequest(req) {
		return SourceArchive{}, false, ErrStorageUnavailable
	}
	row, err := s.q.ReadCICachedSource(ctx, s.pool, dbUUID(req.Key.AttemptID))
	if errors.Is(err, pgx.ErrNoRows) {
		return SourceArchive{}, false, nil
	}
	if err != nil || uuidString(row.AccountID) != req.Key.AccountID || uuidString(row.ExecutionID) != req.Key.ExecutionID {
		return SourceArchive{}, false, ErrStorageUnavailable
	}
	if row.State != "ready" && row.State != "pending" {
		return SourceArchive{}, false, ErrNotFound
	}
	parsed, err := parseStorageKey(row.ObjectKey)
	if err != nil {
		return SourceArchive{}, false, err
	}
	tx, now, err := s.storageTx(ctx)
	if err != nil {
		return SourceArchive{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	saved, err := s.q.GetCIExecution(ctx, tx, sqlc.GetCIExecutionParams{AccountID: row.AccountID, ID: row.ExecutionID})
	if err != nil {
		return SourceArchive{}, false, ErrStorageUnavailable
	}
	execution, err := executionRow(saved)
	if err != nil || execution.Execution.CommitSHA != req.CommitSHA || execution.Execution.Plan.Repository != req.Repository || execution.Execution.Compute == nil || !sameJobsBinding(execution.Execution.Compute.Binding, req.Binding) {
		return SourceArchive{}, false, ErrStorageUnavailable
	}
	if _, err := s.storageWriteOwner(ctx, tx, parsed, RuntimeObject{}, now); err != nil {
		return SourceArchive{}, false, err
	}
	return SourceArchive{Key: row.ObjectKey, Digest: row.Digest, Bytes: row.Bytes}, row.State == "ready", storeError(tx.Commit(ctx))
}

func (s *PostgresStore) Artifact(ctx context.Context, cfg Config, account, execution, job, name string) (JobsArtifact, error) {
	if !cfg.TransportEnabled || !cfg.Allows(account) || !ValidID(execution) || !ValidName(job) || !validArtifact(name) || strings.ContainsAny(name, "*?[") {
		return JobsArtifact{}, ErrNotFound
	}
	active, err := s.q.CIStorageAccountActive(ctx, s.pool, dbUUID(account))
	if err != nil || !active {
		return JobsArtifact{}, ErrNotFound
	}
	row, err := s.Execution(ctx, account, execution)
	if err != nil || row.Mode != ExecutionJobs || !terminal(row.Status) {
		return JobsArtifact{}, ErrNotFound
	}
	for _, planned := range row.Jobs {
		if planned.Name != job || planned.Attempt == nil {
			continue
		}
		key := AttemptKey{AccountID: account, ExecutionID: execution, AttemptID: planned.Attempt.ID}
		attempt, err := s.q.CIStorageAttempt(ctx, s.pool, sqlc.CIStorageAttemptParams{AccountID: dbUUID(account), ExecutionID: dbUUID(execution), AttemptID: dbUUID(key.AttemptID)})
		if err != nil || len(attempt.Output) == 0 {
			return JobsArtifact{}, ErrNotFound
		}
		var verified Observation
		if json.Unmarshal(attempt.Output, &verified) != nil {
			return JobsArtifact{}, ErrNotFound
		}
		for _, artifact := range verified.Artifacts {
			if artifact.Path == name {
				objectKey := jobsObjectPrefix(key) + "output/artifacts/" + name
				stored, err := s.q.ReadCIManagedObject(ctx, s.pool, objectKey)
				now, timeErr := s.executionNow(ctx, s.pool)
				if err != nil || timeErr != nil || stored.State != "ready" || !now.Before(stored.ExpiresAt.Time) || stored.Digest != artifact.Digest || stored.Bytes != artifact.Bytes {
					return JobsArtifact{}, ErrNotFound
				}
				return JobsArtifact{ArtifactRef: artifact, Key: objectKey}, nil
			}
		}
	}
	return JobsArtifact{}, ErrNotFound
}
