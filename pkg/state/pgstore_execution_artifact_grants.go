package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ExecutionArtifactGrantStore = (*PgStore)(nil)

func scanExecutionArtifactGrant(row pgx.Row) (ExecutionArtifactGrant, error) {
	var grant ExecutionArtifactGrant
	var id, accountID, sourceExecutionID pgtype.UUID
	var creatorPrincipalID, redeemedExecutionID pgtype.UUID
	var redeemedAt, revokedAt pgtype.Timestamptz
	if err := row.Scan(
		&id, &accountID, &sourceExecutionID, &grant.ArtifactName, &creatorPrincipalID,
		&grant.TokenHash, &grant.ExpiresAt, &redeemedAt, &redeemedExecutionID, &revokedAt, &grant.CreatedAt,
	); err != nil {
		return ExecutionArtifactGrant{}, err
	}
	grant.ID = pgUUIDString(id)
	grant.AccountID = pgUUIDString(accountID)
	grant.SourceExecutionID = pgUUIDString(sourceExecutionID)
	grant.CreatorPrincipalID = executionUUIDPtr(creatorPrincipalID)
	grant.RedeemedExecutionID = executionUUIDPtr(redeemedExecutionID)
	if redeemedAt.Valid {
		value := redeemedAt.Time.UTC()
		grant.RedeemedAt = &value
	}
	if revokedAt.Valid {
		value := revokedAt.Time.UTC()
		grant.RevokedAt = &value
	}
	grant.TokenHash = append([]byte(nil), grant.TokenHash...)
	grant.ExpiresAt = grant.ExpiresAt.UTC()
	grant.CreatedAt = grant.CreatedAt.UTC()
	return grant, nil
}

const executionArtifactGrantColumns = `id, account_id, source_execution_id, artifact_name,
creator_principal_id, token_hash, expires_at, redeemed_at, redeemed_execution_id, revoked_at, created_at`

func (s *PgStore) CreateExecutionArtifactGrant(ctx context.Context, params CreateExecutionArtifactGrantParams) (ExecutionArtifactGrant, error) {
	if params.ID == "" || params.AccountID == "" || params.SourceExecutionID == "" || params.ArtifactName == "" || len(params.TokenHash) != 32 || !params.ExpiresAt.After(params.CreatedAt) {
		return ExecutionArtifactGrant{}, ErrExecutionInvalid
	}
	source, err := s.ExecutionByID(ctx, params.AccountID, params.SourceExecutionID)
	if err != nil || source.Status != api.ExecutionStatusSucceeded {
		return ExecutionArtifactGrant{}, ErrNotFound
	}
	artifactFound := false
	for _, artifact := range source.Artifacts {
		if artifact.Name == params.ArtifactName {
			if err := api.ValidateExecutionArtifacts([]api.ExecutionArtifact{artifact}); err != nil {
				return ExecutionArtifactGrant{}, ErrExecutionInvalid
			}
			artifactFound = true
			break
		}
	}
	if !artifactFound {
		return ExecutionArtifactGrant{}, ErrNotFound
	}
	creatorID := pgtype.UUID{}
	if params.CreatorPrincipalID != nil {
		creatorID, err = parsePgUUID(*params.CreatorPrincipalID)
		if err != nil {
			return ExecutionArtifactGrant{}, fmt.Errorf("create execution artifact grant: creator principal: %w", err)
		}
	}
	row := s.pool.QueryRow(ctx, `INSERT INTO execution_artifact_grants
		(id, account_id, source_execution_id, artifact_name, creator_principal_id, token_hash, expires_at, created_at)
		SELECT $1, e.account_id, e.id, $4, $5, $6, $7, $8
		FROM executions e
		WHERE e.id = $3 AND e.account_id = $2 AND e.status = 'succeeded'
		RETURNING `+executionArtifactGrantColumns,
		mustPgUUID(params.ID), mustPgUUID(params.AccountID), mustPgUUID(params.SourceExecutionID),
		params.ArtifactName, creatorID, params.TokenHash, executionTime(params.ExpiresAt), executionTime(params.CreatedAt))
	grant, err := scanExecutionArtifactGrant(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ExecutionArtifactGrant{}, ErrNotFound
		}
		return ExecutionArtifactGrant{}, fmt.Errorf("create execution artifact grant: %w", err)
	}
	return grant, nil
}

func (s *PgStore) ExecutionArtifactGrantByToken(ctx context.Context, accountID string, tokenHash []byte, at time.Time) (ExecutionArtifactGrant, error) {
	if len(tokenHash) != 32 {
		return ExecutionArtifactGrant{}, ErrNotFound
	}
	grant, err := scanExecutionArtifactGrant(s.pool.QueryRow(ctx, `SELECT `+executionArtifactGrantColumns+`
		FROM execution_artifact_grants
		WHERE account_id = $1 AND token_hash = $2 AND revoked_at IS NULL
		  AND redeemed_at IS NULL AND expires_at > $3`, mustPgUUID(accountID), tokenHash, executionTime(at)))
	if err != nil {
		if err == pgx.ErrNoRows {
			return ExecutionArtifactGrant{}, ErrNotFound
		}
		return ExecutionArtifactGrant{}, fmt.Errorf("load execution artifact grant: %w", err)
	}
	return grant, nil
}

func (s *PgStore) RevokeExecutionArtifactGrant(ctx context.Context, accountID, grantID string, principalID *string, accountWide bool, at time.Time) (ExecutionArtifactGrant, error) {
	creatorID := pgtype.UUID{}
	var err error
	if principalID != nil {
		creatorID, err = parsePgUUID(*principalID)
		if err != nil {
			return ExecutionArtifactGrant{}, fmt.Errorf("revoke execution artifact grant: principal: %w", err)
		}
	}
	grant, err := scanExecutionArtifactGrant(s.pool.QueryRow(ctx, `UPDATE execution_artifact_grants
		SET revoked_at = COALESCE(revoked_at, $4)
		WHERE id = $1 AND account_id = $2 AND ($3 OR creator_principal_id = $5)
		RETURNING `+executionArtifactGrantColumns,
		mustPgUUID(grantID), mustPgUUID(accountID), accountWide, executionTime(at), creatorID))
	if err != nil {
		if err == pgx.ErrNoRows {
			return ExecutionArtifactGrant{}, ErrNotFound
		}
		return ExecutionArtifactGrant{}, fmt.Errorf("revoke execution artifact grant: %w", err)
	}
	return grant, nil
}
