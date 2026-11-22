package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ExecutionAdmissionStore = (*PgStore)(nil)

func (s *PgStore) AdmitExecution(ctx context.Context, identity ExecutionAdmissionIdentity, prepare PrepareExecutionAdmission) (Execution, bool, error) {
	if err := validateExecutionAdmission(identity, prepare); err != nil {
		return Execution{}, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Execution{}, false, fmt.Errorf("admit execution: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	accountID := mustPgUUID(identity.AccountID)
	// Share CreateExecution's per-account quota lock. A waiter observes the
	// first transaction's committed identity before doing any preparation.
	if _, err := q.ExecutionLockAccount(ctx, tx, accountID); err != nil {
		return Execution{}, false, mapErr(err)
	}
	if identity.TaskCallback != nil {
		previous, e := q.RunTaskCallbackReplay(ctx, tx, sqlc.RunTaskCallbackReplayParams{AccountID: accountID, CallbackKey: identity.TaskCallback.Key()})
		if e == nil {
			if err := compareExecutionAdmission(previous.RequestDigest, identity.Fingerprint); err != nil {
				return Execution{}, false, err
			}
			row, e := (executionAdmissionTxReader{tx}).ExecutionByID(ctx, identity.AccountID, pgUUIDString(previous.ExecutionID))
			return row, true, e
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return Execution{}, false, e
		}
	}
	stored, err := q.ExecutionAdmissionGet(ctx, tx, sqlc.ExecutionAdmissionGetParams{
		AccountID: accountID, ScopeHash: identity.ScopeHash[:], KeyHash: identity.KeyHash[:],
	})
	if err == nil {
		if err := compareExecutionAdmission(stored.RequestDigest, identity.Fingerprint); err != nil {
			return Execution{}, false, err
		}
		row, err := (executionAdmissionTxReader{tx}).ExecutionByID(ctx, identity.AccountID, pgUUIDString(stored.ExecutionID))
		return row, true, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, false, fmt.Errorf("admit execution: lookup: %w", err)
	}
	legacy, err := q.ExecutionAdmissionLegacyExists(ctx, tx, sqlc.ExecutionAdmissionLegacyExistsParams{
		AccountID: accountID, LegacyKey: identity.LegacyKey, RetentionSeconds: api.ExecutionIdempotencyRetentionSeconds,
	})
	if err != nil {
		return Execution{}, false, fmt.Errorf("admit execution: legacy lookup: %w", err)
	}
	if legacy {
		return Execution{}, false, ErrExecutionAdmissionLegacy
	}
	params, err := prepare(ctx, executionAdmissionTxReader{tx})
	if err != nil {
		return Execution{}, false, err
	}
	if err := validateTaskAdmissionIdentity(identity, params); err != nil {
		return Execution{}, false, err
	}
	if params.AccountID != identity.AccountID {
		return Execution{}, false, ErrExecutionInvalid
	}
	row, err := createExecutionInTx(ctx, tx, params)
	if err != nil {
		return Execution{}, false, err
	}
	if err := q.ExecutionAdmissionDeleteExpiredKey(ctx, tx, sqlc.ExecutionAdmissionDeleteExpiredKeyParams{
		AccountID: accountID, ScopeHash: identity.ScopeHash[:], KeyHash: identity.KeyHash[:],
	}); err != nil {
		return Execution{}, false, fmt.Errorf("admit execution: expire key: %w", err)
	}
	if err := q.ExecutionAdmissionInsert(ctx, tx, sqlc.ExecutionAdmissionInsertParams{
		AccountID: accountID, ScopeHash: identity.ScopeHash[:], KeyHash: identity.KeyHash[:],
		RequestDigest: identity.Fingerprint[:], ExecutionID: mustPgUUID(row.ID), RetentionSeconds: api.ExecutionIdempotencyRetentionSeconds,
	}); err != nil {
		return Execution{}, false, fmt.Errorf("admit execution: persist key: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Execution{}, false, fmt.Errorf("admit execution: commit: %w", err)
	}
	return row, false, nil
}

type executionAdmissionTxReader struct{ tx pgx.Tx }

func (r executionAdmissionTxReader) ExecutionByID(ctx context.Context, accountID, executionID string) (Execution, error) {
	row, err := sqlc.New().ExecutionGetForAccount(ctx, r.tx, sqlc.ExecutionGetForAccountParams{
		AccountID: mustPgUUID(accountID), ExecutionID: mustPgUUID(executionID),
	})
	if err != nil {
		return Execution{}, mapErr(err)
	}
	out := executionFromSQL(row)
	if err := attachTaskRef(ctx, r.tx, &out); err != nil {
		return Execution{}, err
	}
	return out, nil
}

func (r executionAdmissionTxReader) ExecutionArtifactGrantByToken(ctx context.Context, accountID string, tokenHash []byte, at time.Time) (ExecutionArtifactGrant, error) {
	if len(tokenHash) != 32 {
		return ExecutionArtifactGrant{}, ErrNotFound
	}
	return firstCredential(ctx, credentialLookups(credTagArtifactGrant, tokenHash), func(ctx context.Context, value []byte) (ExecutionArtifactGrant, error) {
		row, err := sqlc.New().ExecutionAdmissionArtifactGrant(ctx, r.tx, sqlc.ExecutionAdmissionArtifactGrantParams{
			AccountID: mustPgUUID(accountID), TokenHash: value, At: executionTime(at),
		})
		if err != nil {
			return ExecutionArtifactGrant{}, mapErr(err)
		}
		return ExecutionArtifactGrant{
			ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), SourceExecutionID: pgUUIDString(row.SourceExecutionID),
			ArtifactName: row.ArtifactName, CreatorPrincipalID: executionUUIDPtr(row.CreatorPrincipalID), TokenHash: append([]byte(nil), row.TokenHash...),
			ExpiresAt: row.ExpiresAt.Time.UTC(), CreatedAt: row.CreatedAt.Time.UTC(),
		}, nil
	})
}
