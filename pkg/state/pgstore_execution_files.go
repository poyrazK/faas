package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ExecutionFileStore = (*PgStore)(nil)

func fileUUID(p *string) pgtype.UUID {
	if p == nil {
		return pgtype.UUID{}
	}
	return mustPgUUID(*p)
}
func fileTime(p *time.Time) pgtype.Timestamptz {
	if p == nil {
		return pgtype.Timestamptz{}
	}
	return executionTime(*p)
}
func executionFileFromSQL(f sqlc.ExecutionFile) ExecutionFile {
	return ExecutionFile{ID: pgUUIDString(f.ID), AccountID: pgUUIDString(f.AccountID), PrincipalID: executionUUIDPtr(f.PrincipalID), ExecutionID: executionUUIDPtr(f.ExecutionID), Name: f.Name, Kind: f.Kind, Status: f.Status, SizeBytes: f.SizeBytes, SHA256: f.Sha256, StorageKey: f.StorageKey, SealedKey: append([]byte(nil), f.SealedKey...), Token: executionUUIDPtr(f.Token), LeaseExpiresAt: timestamptzToTimePtr(f.LeaseExpiresAt), ExpiresAt: f.ExpiresAt.Time, CreatedAt: f.CreatedAt.Time, UpdatedAt: f.UpdatedAt.Time}
}
func getExecutionFile(ctx context.Context, db sqlc.DBTX, account, id string) (ExecutionFile, error) {
	if _, err := parsePgUUID(id); err != nil {
		return ExecutionFile{}, ErrNotFound
	}
	f, err := sqlc.New().ExecutionFileGet(ctx, db, sqlc.ExecutionFileGetParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id)})
	if err != nil {
		return ExecutionFile{}, mapErr(err)
	}
	return executionFileFromSQL(f), nil
}
func (s *PgStore) ExecutionFileByID(ctx context.Context, account, id string) (ExecutionFile, error) {
	return getExecutionFile(ctx, s.pool, account, id)
}
func (r executionAdmissionTxReader) ExecutionFileByID(ctx context.Context, account, id string) (ExecutionFile, error) {
	return getExecutionFile(ctx, r.tx, account, id)
}
func insertExecutionFile(ctx context.Context, tx pgx.Tx, f ExecutionFile) error {
	return sqlc.New().ExecutionFileInsert(ctx, tx, sqlc.ExecutionFileInsertParams{ID: mustPgUUID(f.ID), AccountID: mustPgUUID(f.AccountID), PrincipalID: fileUUID(f.PrincipalID), ExecutionID: fileUUID(f.ExecutionID), Name: f.Name, Kind: f.Kind, Status: f.Status, SizeBytes: f.SizeBytes, Sha256: f.SHA256, StorageKey: f.StorageKey, SealedKey: f.SealedKey, Token: fileUUID(f.Token), LeaseExpiresAt: fileTime(f.LeaseExpiresAt), ExpiresAt: executionTime(f.ExpiresAt), CreatedAt: executionTime(f.CreatedAt), UpdatedAt: executionTime(f.UpdatedAt)})
}
func executionFilesQuota(ctx context.Context, tx pgx.Tx, account string, limits api.ExecutionFileLimits, extra int64, extraCount int) error {
	q := sqlc.New()
	files, err := q.ExecutionFilesAccount(ctx, tx, mustPgUUID(account))
	if err != nil {
		return err
	}
	reserved, err := q.ExecutionFileUsageBytes(ctx, tx, mustPgUUID(account))
	if err != nil {
		return err
	}
	return validateExecutionFileQuota(limits, reserved+extra, len(files)+extraCount)
}
func (s *PgStore) ClaimExecutionFileUpload(ctx context.Context, f ExecutionFile) (ExecutionFile, bool, error) {
	if err := validateExecutionFileUpload(f); err != nil {
		return ExecutionFile{}, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ExecutionFile{}, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	acct, err := sqlc.New().ExecutionLockAccount(ctx, tx, mustPgUUID(f.AccountID))
	if err != nil {
		return ExecutionFile{}, false, mapErr(err)
	}
	old, err := getExecutionFile(ctx, tx, f.AccountID, f.ID)
	if err == nil {
		if !executionFileOwner(old, f.PrincipalID, false) || old.SizeBytes != f.SizeBytes || old.SHA256 != f.SHA256 {
			return ExecutionFile{}, false, ErrExecutionInvalid
		}
		if old.Status == "ready" && old.ExpiresAt.After(f.UpdatedAt) {
			return old, true, nil
		}
		return ExecutionFile{}, false, ErrExecutionFileBusy
	}
	if !errors.Is(err, ErrNotFound) {
		return ExecutionFile{}, false, err
	}
	limits := api.Plan(acct.Plan).ExecutionFileLimits()
	if f.SizeBytes > limits.MaxFileBytes {
		return ExecutionFile{}, false, ErrExecutionInvalid
	}
	if err = executionFilesQuota(ctx, tx, f.AccountID, limits, f.SizeBytes, 1); err != nil {
		return ExecutionFile{}, false, err
	}
	if err = insertExecutionFile(ctx, tx, f); err != nil {
		return ExecutionFile{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ExecutionFile{}, false, err
	}
	return cloneExecutionFile(f), false, nil
}
func (s *PgStore) FinishExecutionFileUpload(ctx context.Context, account, id, token string, at time.Time) (ExecutionFile, error) {
	f, err := sqlc.New().ExecutionFileUploadFinish(ctx, s.pool, sqlc.ExecutionFileUploadFinishParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id), Token: mustPgUUID(token), At: executionTime(at)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ExecutionFile{}, ErrExecutionFileLeaseLost
	}
	if err != nil {
		return ExecutionFile{}, err
	}
	return executionFileFromSQL(f), nil
}
func (s *PgStore) FailExecutionFileUpload(ctx context.Context, account, id, token string, at time.Time) error {
	return sqlc.New().ExecutionFileUploadFail(ctx, s.pool, sqlc.ExecutionFileUploadFailParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id), Token: mustPgUUID(token), At: executionTime(at)})
}
func (s *PgStore) RevokeExecutionFile(ctx context.Context, account, id string, principal *string, wide bool, at time.Time) error {
	if _, err := parsePgUUID(id); err != nil {
		return ErrNotFound
	}
	n, err := sqlc.New().ExecutionFileRevoke(ctx, s.pool, sqlc.ExecutionFileRevokeParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id), PrincipalID: fileUUID(principal), AccountWide: wide, At: executionTime(at)})
	if err == nil && n != 1 {
		return ErrNotFound
	}
	return err
}
func (s *PgStore) ClaimExpiredExecutionFiles(ctx context.Context, at time.Time, limit int) ([]ExecutionFile, error) {
	if limit < 1 || limit > api.ExecutionFileGCBatchSize {
		limit = api.ExecutionFileGCBatchSize
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	rows, err := q.ExecutionFileGCCandidates(ctx, tx, sqlc.ExecutionFileGCCandidatesParams{At: executionTime(at), SafeAt: executionTime(at.Add(-api.ExecutionFileGCGrace)), BatchSize: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := []ExecutionFile{}
	for _, f := range rows {
		if _, err = q.ExecutionLockAccount(ctx, tx, f.AccountID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, mapErr(err)
		}
		eligible, err := q.ExecutionFileGCEligible(ctx, tx, sqlc.ExecutionFileGCEligibleParams{AccountID: f.AccountID, ID: f.ID, At: executionTime(at), SafeAt: executionTime(at.Add(-api.ExecutionFileGCGrace))})
		if err != nil {
			return nil, err
		}
		if !eligible {
			continue
		}
		claimed, err := q.ExecutionFileGCClaim(ctx, tx, sqlc.ExecutionFileGCClaimParams{AccountID: f.AccountID, ID: f.ID, Token: mustPgUUID(uuid.NewString()), LeaseUntil: executionTime(at.Add(api.ExecutionFileUploadTimeout)), At: executionTime(at), PreviousAt: f.UpdatedAt})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, executionFileFromSQL(claimed))
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *PgStore) DeleteExecutionFileMetadata(ctx context.Context, account, id, token string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ExecutionLockAccount(ctx, tx, mustPgUUID(account)); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return mapErr(err)
	}
	f, err := getExecutionFile(ctx, tx, account, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if f.Status != "deleting" || f.Token == nil || *f.Token != token {
		return ErrExecutionFileLeaseLost
	}
	if err = q.ExecutionFileUnlink(ctx, tx, mustPgUUID(id)); err != nil {
		return err
	}
	n, err := q.ExecutionFileDelete(ctx, tx, sqlc.ExecutionFileDeleteParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id), Token: mustPgUUID(token)})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrExecutionFileLeaseLost
	}
	return tx.Commit(ctx)
}
func validateExecutionFilesTx(ctx context.Context, tx pgx.Tx, p CreateExecutionParams, plan api.Plan) error {
	if err := validateStoredOutputs(p); err != nil {
		return err
	}
	limits := plan.ExecutionFileLimits()
	if p.Request.Limits.MaxArtifactBytes > limits.MaxOutputBytes || storedInputBytes(p.Request.StoredInputs) > limits.MaxInputBytes {
		return ErrExecutionInvalid
	}
	for _, ref := range p.Request.StoredInputs {
		f, err := getExecutionFile(ctx, tx, p.AccountID, ref.ID)
		if err != nil || !validAdmittedInput(f, ref, p) {
			return ErrExecutionFileNotReady
		}
		if !executionFileOwner(f, p.RunsPrincipalID, p.FilesAccountWide) {
			allowed := false
			for _, r := range p.ArtifactGrantRedemptions {
				g, err := (executionAdmissionTxReader{tx}).ExecutionArtifactGrantByToken(ctx, p.AccountID, r.TokenHash, p.AdmittedAt)
				if err == nil && g.ID == r.GrantID && f.ExecutionID != nil && g.SourceExecutionID == *f.ExecutionID && g.ArtifactName == f.Name {
					allowed = true
				}
			}
			if !allowed {
				return ErrExecutionArtifactGrantUnavailable
			}
		}
	}
	return executionFilesQuota(ctx, tx, p.AccountID, limits, p.Request.Limits.MaxArtifactBytes, len(p.Request.StoredOutputs))
}
func addExecutionFilesTx(ctx context.Context, tx pgx.Tx, p CreateExecutionParams, id string) error {
	for _, f := range outputExecutionFiles(p, id) {
		if err := insertExecutionFile(ctx, tx, f); err != nil {
			return err
		}
	}
	for _, f := range p.Request.StoredInputs {
		if err := sqlc.New().ExecutionFileLink(ctx, tx, sqlc.ExecutionFileLinkParams{ExecutionID: mustPgUUID(id), FileID: mustPgUUID(f.ID)}); err != nil {
			return err
		}
	}
	return nil
}
func completeExecutionFilesTx(ctx context.Context, tx pgx.Tx, row Execution, p CompleteExecutionParams) error {
	q := sqlc.New()
	rows, err := q.ExecutionFileOutputList(ctx, tx, sqlc.ExecutionFileOutputListParams{AccountID: mustPgUUID(row.AccountID), ExecutionID: mustPgUUID(row.ID)})
	if err != nil {
		return err
	}
	files := make([]ExecutionFile, len(rows))
	for i, f := range rows {
		files[i] = executionFileFromSQL(f)
	}
	if err := validateStoredCompletion(row, p, files); err != nil {
		return err
	}
	if p.Status == api.ExecutionStatusSucceeded {
		for _, a := range p.Artifacts {
			if a.FileID == "" {
				continue
			}
			n, err := q.ExecutionFileOutputReady(ctx, tx, sqlc.ExecutionFileOutputReadyParams{AccountID: mustPgUUID(row.AccountID), ExecutionID: mustPgUUID(row.ID), ID: mustPgUUID(a.FileID), Name: a.Name, SizeBytes: int64(a.SizeBytes), Sha256: a.SHA256, At: executionTime(p.FinishedAt)})
			if err != nil {
				return err
			}
			if n != 1 {
				return ErrExecutionInvalidTerminal
			}
		}
	}
	return nil
}
