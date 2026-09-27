package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const platformTenantAccessTokenCols = `id, account_id, platform_tenant_id, name, prefix,
	token_hash, scopes, created_at, expires_at, last_used_at, revoked_at`

func scanPlatformTenantAccessToken(row pgx.Row) (PlatformTenantAccessToken, error) {
	var token PlatformTenantAccessToken
	err := row.Scan(&token.ID, &token.AccountID, &token.TenantID, &token.Name, &token.Prefix,
		&token.TokenHash, &token.Scopes, &token.CreatedAt, &token.ExpiresAt, &token.LastUsedAt, &token.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantAccessToken{}, ErrNotFound
	}
	if err != nil {
		return PlatformTenantAccessToken{}, err
	}
	return token, nil
}

func (s *PgStore) CreatePlatformTenantAccessToken(ctx context.Context, in PlatformTenantAccessTokenInput) (PlatformTenantAccessToken, error) {
	if err := validatePlatformTenantAccessTokenInput(in); err != nil {
		return PlatformTenantAccessToken{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PlatformTenantAccessToken{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tenantID string
	if err := tx.QueryRow(ctx, `select id from platform_tenants where account_id=$1::uuid and id=$2::uuid for update`,
		in.AccountID, in.TenantID).Scan(&tenantID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PlatformTenantAccessToken{}, ErrNotFound
		}
		return PlatformTenantAccessToken{}, err
	}
	var active int
	if err := tx.QueryRow(ctx, `select count(*) from platform_tenant_access_tokens where account_id=$1::uuid and platform_tenant_id=$2::uuid and revoked_at is null and expires_at>now()`,
		in.AccountID, in.TenantID).Scan(&active); err != nil {
		return PlatformTenantAccessToken{}, err
	}
	if active >= PlatformTenantAccessTokenLimit {
		return PlatformTenantAccessToken{}, &PlatformTenantAccessTokenQuotaError{Limit: PlatformTenantAccessTokenLimit, Observed: active}
	}
	var nameExists bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from platform_tenant_access_tokens where account_id=$1::uuid and platform_tenant_id=$2::uuid and revoked_at is null and expires_at>now() and lower(name)=lower($3))`,
		in.AccountID, in.TenantID, in.Name).Scan(&nameExists); err != nil {
		return PlatformTenantAccessToken{}, err
	}
	if nameExists {
		return PlatformTenantAccessToken{}, ErrConflict
	}
	token, err := scanPlatformTenantAccessToken(tx.QueryRow(ctx, `insert into platform_tenant_access_tokens
		(account_id, platform_tenant_id, name, prefix, token_hash, scopes, expires_at)
		values ($1::uuid, $2::uuid, $3, $4, $5, $6, $7) returning `+platformTenantAccessTokenCols,
		in.AccountID, in.TenantID, in.Name, in.Prefix, in.TokenHash, in.Scopes, in.ExpiresAt.UTC()))
	if err != nil {
		if isUniqueViolation(err) {
			return PlatformTenantAccessToken{}, ErrConflict
		}
		return PlatformTenantAccessToken{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PlatformTenantAccessToken{}, err
	}
	return token, nil
}

func (s *PgStore) ListPlatformTenantAccessTokens(ctx context.Context, accountID, tenantID string) ([]PlatformTenantAccessToken, error) {
	if accountID == "" || tenantID == "" {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `select `+platformTenantAccessTokenCols+` from platform_tenant_access_tokens
		where account_id=$1::uuid and platform_tenant_id=$2::uuid order by created_at desc, id desc`, accountID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PlatformTenantAccessToken, 0)
	for rows.Next() {
		token, err := scanPlatformTenantAccessToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, token)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		if _, err := s.GetPlatformTenant(ctx, accountID, tenantID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *PgStore) RevokePlatformTenantAccessToken(ctx context.Context, accountID, tenantID, tokenID string) (PlatformTenantAccessToken, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PlatformTenantAccessToken{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	token, err := scanPlatformTenantAccessToken(tx.QueryRow(ctx, `select `+platformTenantAccessTokenCols+`
		from platform_tenant_access_tokens where account_id=$1::uuid and platform_tenant_id=$2::uuid and id=$3::uuid for update`,
		accountID, tenantID, tokenID))
	if err != nil {
		return PlatformTenantAccessToken{}, false, err
	}
	if token.RevokedAt != nil {
		if err := tx.Commit(ctx); err != nil {
			return PlatformTenantAccessToken{}, false, err
		}
		return token, false, nil
	}
	token, err = scanPlatformTenantAccessToken(tx.QueryRow(ctx, `update platform_tenant_access_tokens set revoked_at=$4
		where account_id=$1::uuid and platform_tenant_id=$2::uuid and id=$3::uuid returning `+platformTenantAccessTokenCols,
		accountID, tenantID, tokenID, time.Now().UTC()))
	if err != nil {
		return PlatformTenantAccessToken{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PlatformTenantAccessToken{}, false, err
	}
	return token, true, nil
}

func (s *PgStore) AuthenticatePlatformTenantAccessToken(ctx context.Context, hash []byte) (Account, PlatformTenantAccessToken, error) {
	if len(hash) != 32 {
		return Account{}, PlatformTenantAccessToken{}, ErrNotFound
	}
	token, err := scanPlatformTenantAccessToken(s.pool.QueryRow(ctx, `update platform_tenant_access_tokens set last_used_at=now()
		where token_hash=$1 and revoked_at is null and expires_at>now() returning `+platformTenantAccessTokenCols, hash))
	if err != nil {
		return Account{}, PlatformTenantAccessToken{}, err
	}
	account, err := s.AccountByID(ctx, token.AccountID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Account{}, PlatformTenantAccessToken{}, ErrNotFound
		}
		return Account{}, PlatformTenantAccessToken{}, err
	}
	return account, token, nil
}
