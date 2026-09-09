package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const deployTokenSelectCols = `id, account_id, app_id, token_sha256, coalesce(label,''), scopes,
       status, created_at, expires_at, last_used_at, revoked_at, rotated_from_id`

func scanDeployToken(row pgx.Row) (DeployToken, error) {
	var t DeployToken
	if err := row.Scan(&t.ID, &t.AccountID, &t.AppID, &t.Hash, &t.Label,
		&t.Scopes, &t.Status, &t.CreatedAt, &t.ExpiresAt, &t.LastUsedAt,
		&t.RevokedAt, &t.RotatedFromID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DeployToken{}, ErrNotFound
		}
		return DeployToken{}, mapErr(err)
	}
	return t, nil
}

func (s *PgStore) CreateDeployToken(ctx context.Context, accountID, appID string, hash []byte, label string, scopes []string, expiresAt time.Time) (DeployToken, error) {
	if accountID == "" || appID == "" || len(hash) != 32 || expiresAt.IsZero() || !expiresAt.After(time.Now()) {
		return DeployToken{}, errors.New("pgstore: invalid deploy token")
	}
	if len(scopes) == 0 {
		scopes = []string{"deploy:write"}
	}
	for _, scope := range scopes {
		if scope != "deploy:write" {
			return DeployToken{}, fmt.Errorf("pgstore: unsupported deploy token scope %q", scope)
		}
	}
	row := s.pool.QueryRow(ctx,
		`insert into deploy_tokens (account_id, app_id, token_sha256, label, scopes, expires_at)
		 values ($1::uuid, $2::uuid, $3, $4, $5, $6)
		 returning `+deployTokenSelectCols,
		accountID, appID, hash, label, scopes, expiresAt.UTC())
	t, err := scanDeployToken(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return DeployToken{}, ErrConflict
		}
	}
	return t, err
}

func (s *PgStore) ListDeployTokensForApp(ctx context.Context, accountID, appID string) ([]DeployToken, error) {
	if accountID == "" || appID == "" {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx,
		`select `+deployTokenSelectCols+` from deploy_tokens
		 where account_id = $1::uuid and app_id = $2::uuid
		 order by created_at desc`, accountID, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeployToken
	for rows.Next() {
		var t DeployToken
		if err := rows.Scan(&t.ID, &t.AccountID, &t.AppID, &t.Hash, &t.Label,
			&t.Scopes, &t.Status, &t.CreatedAt, &t.ExpiresAt, &t.LastUsedAt,
			&t.RevokedAt, &t.RotatedFromID); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *PgStore) GetDeployToken(ctx context.Context, accountID, appID, tokenID string) (DeployToken, error) {
	if accountID == "" || appID == "" || tokenID == "" {
		return DeployToken{}, ErrNotFound
	}
	return scanDeployToken(s.pool.QueryRow(ctx,
		`select `+deployTokenSelectCols+` from deploy_tokens
		 where id = $1::uuid and account_id = $2::uuid and app_id = $3::uuid`,
		tokenID, accountID, appID))
}

func (s *PgStore) RevokeDeployToken(ctx context.Context, accountID, appID, tokenID string) (DeployToken, error) {
	if accountID == "" || appID == "" || tokenID == "" {
		return DeployToken{}, ErrNotFound
	}
	return scanDeployToken(s.pool.QueryRow(ctx,
		`update deploy_tokens
		 set status = 'revoked', revoked_at = coalesce(revoked_at, now())
		 where id = $1::uuid and account_id = $2::uuid and app_id = $3::uuid
		 returning `+deployTokenSelectCols,
		tokenID, accountID, appID))
}

func (s *PgStore) RotateDeployToken(ctx context.Context, accountID, appID, tokenID string, hash []byte, label string, expiresAt time.Time, graceWindow time.Duration) (DeployToken, DeployToken, error) {
	if len(hash) != 32 || !expiresAt.After(time.Now()) {
		return DeployToken{}, DeployToken{}, errors.New("pgstore: invalid rotated deploy token")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DeployToken{}, DeployToken{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	old, err := scanDeployToken(tx.QueryRow(ctx,
		`select `+deployTokenSelectCols+` from deploy_tokens
		 where id = $1::uuid and account_id = $2::uuid and app_id = $3::uuid for update`,
		tokenID, accountID, appID))
	if err != nil {
		return DeployToken{}, DeployToken{}, err
	}
	if old.Status == string(APIKeyStatusRevoked) {
		return DeployToken{}, DeployToken{}, ErrAPIKeyRevoked
	}
	if old.ExpiresAt == nil || !old.ExpiresAt.After(time.Now()) {
		return DeployToken{}, DeployToken{}, ErrAPIKeyExpired
	}
	if label == "" {
		label = old.Label
	}
	newToken, err := scanDeployToken(tx.QueryRow(ctx,
		`insert into deploy_tokens (account_id, app_id, token_sha256, label, scopes, expires_at, rotated_from_id)
		 values ($1::uuid, $2::uuid, $3, $4, $5, $6, $7::uuid)
		 returning `+deployTokenSelectCols,
		accountID, appID, hash, label, old.Scopes, expiresAt.UTC(), old.ID))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return DeployToken{}, DeployToken{}, ErrConflict
		}
		return DeployToken{}, DeployToken{}, err
	}
	var oldUpdated DeployToken
	if graceWindow <= 0 {
		oldUpdated, err = scanDeployToken(tx.QueryRow(ctx,
			`update deploy_tokens set status = 'revoked', revoked_at = coalesce(revoked_at, now())
			 where id = $1::uuid returning `+deployTokenSelectCols, old.ID))
	} else {
		oldUpdated, err = scanDeployToken(tx.QueryRow(ctx,
			`update deploy_tokens set status = 'grace', expires_at = now() + ($1)::interval
			 where id = $2::uuid returning `+deployTokenSelectCols, graceWindow.String(), old.ID))
	}
	if err != nil {
		return DeployToken{}, DeployToken{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DeployToken{}, DeployToken{}, err
	}
	return newToken, oldUpdated, nil
}

func (s *PgStore) AuthenticateDeployToken(ctx context.Context, hash []byte) (Account, APIKey, error) {
	if len(hash) != 32 {
		return Account{}, APIKey{}, ErrNotFound
	}
	t, err := scanDeployToken(s.pool.QueryRow(ctx,
		`select `+deployTokenSelectCols+` from deploy_tokens where token_sha256 = $1`, hash))
	if err != nil {
		return Account{}, APIKey{}, err
	}
	if t.Status == string(APIKeyStatusRevoked) {
		return Account{}, APIKey{}, ErrAPIKeyRevoked
	}
	if t.ExpiresAt == nil || !t.ExpiresAt.After(time.Now()) {
		_, _ = s.pool.Exec(ctx, `update deploy_tokens set status = 'revoked', revoked_at = coalesce(revoked_at, now()) where id = $1::uuid and status <> 'revoked'`, t.ID)
		return Account{}, APIKey{}, ErrAPIKeyExpired
	}
	acct, err := s.AccountByID(ctx, t.AccountID)
	if err != nil {
		return Account{}, APIKey{}, err
	}
	return acct, APIKey{ID: t.ID, AccountID: t.AccountID, AppID: t.AppID,
		Hash: t.Hash, Label: t.Label, Scopes: t.Scopes,
		CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, Status: t.Status}, nil
}

func (s *PgStore) TouchDeployTokenLastUsed(ctx context.Context, tokenID string) error {
	if tokenID == "" {
		return nil
	}
	_, err := s.pool.Exec(ctx, `update deploy_tokens set last_used_at = now() where id = $1::uuid and status in ('active','grace')`, tokenID)
	return err
}
