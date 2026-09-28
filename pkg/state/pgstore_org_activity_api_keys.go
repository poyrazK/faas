package state

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var _ OrgActivityAPIKeyMutationStore = (*PgStore)(nil)

func (s *PgStore) CreateOrgAPIKeyWithActivity(ctx context.Context, orgID, accountID string, hash []byte, label string, scopes []string, expiresAt *time.Time, createdIP, createdUA string, parent *string, activity OrgActivity) (APIKey, int64, error) {
	activity, err := normalizeOrgActivity(activity, time.Now())
	if err != nil {
		return APIKey{}, 0, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIKey{}, 0, fmt.Errorf("state: begin org api key activity create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	key, err := scanAPIKeyRow(ctx, tx,
		`insert into api_keys (account_id, key_sha256, label, scopes, expires_at, org_id, created_ip, created_ua, parent_key_id)
		 values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		           last_used_at, expires_at, status, revoked_at, rotated_from_id,
		           coalesce(host(created_ip),''), coalesce(created_ua,''), parent_key_id`,
		accountID, hash, nullString(label), scopes, nullableTimestamptzPtr(expiresAt), orgID,
		nullString(createdIP), nullString(createdUA), parent)
	if err != nil {
		return APIKey{}, 0, err
	}
	activity, err = bindOrgActivityToAPIKey(activity, key)
	if err != nil {
		return APIKey{}, 0, err
	}
	outboxID, err := enqueueOrgActivityOutboxTx(ctx, tx, activity)
	if err != nil {
		return APIKey{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return APIKey{}, 0, fmt.Errorf("state: commit org api key activity create: %w", err)
	}
	return key, outboxID, nil
}

func (s *PgStore) RevokeOrgAPIKeyWithActivity(ctx context.Context, orgID, keyID string, activity OrgActivity) (APIKey, int64, error) {
	activity, err := normalizeOrgActivity(activity, time.Now())
	if err != nil {
		return APIKey{}, 0, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIKey{}, 0, fmt.Errorf("state: begin org api key activity revoke: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	key, err := scanAPIKeyRow(ctx, tx,
		`select id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		        last_used_at, expires_at, status, revoked_at, rotated_from_id,
		        coalesce(host(created_ip),''), coalesce(created_ua,''), parent_key_id
		   from api_keys where id = $1 for update`, keyID)
	if err != nil {
		return APIKey{}, 0, err
	}
	if key.OrgID != orgID {
		return APIKey{}, 0, ErrNotFound
	}
	if key.Status == string(APIKeyStatusRevoked) {
		if err := tx.Commit(ctx); err != nil {
			return APIKey{}, 0, fmt.Errorf("state: commit idempotent org api key revoke: %w", err)
		}
		return key, 0, nil
	}
	key, err = scanAPIKeyRow(ctx, tx,
		`update api_keys
		    set status = 'revoked', revoked_at = coalesce(revoked_at, now())
		  where id = $1 and org_id = $2
		  returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		            last_used_at, expires_at, status, revoked_at, rotated_from_id,
		            coalesce(host(created_ip),''), coalesce(created_ua,''), parent_key_id`,
		keyID, orgID)
	if err != nil {
		return APIKey{}, 0, err
	}
	activity, err = bindOrgActivityToAPIKey(activity, key)
	if err != nil {
		return APIKey{}, 0, err
	}
	outboxID, err := enqueueOrgActivityOutboxTx(ctx, tx, activity)
	if err != nil {
		return APIKey{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return APIKey{}, 0, fmt.Errorf("state: commit org api key activity revoke: %w", err)
	}
	return key, outboxID, nil
}

func (s *PgStore) RotateOrgAPIKeyWithActivity(ctx context.Context, orgID, oldKeyID string, newHash []byte, newLabel string, graceWindow time.Duration, createdIP, createdUA string, parent *string, activity OrgActivity) (APIKey, APIKey, int64, error) {
	activity, err := normalizeOrgActivity(activity, time.Now())
	if err != nil {
		return APIKey{}, APIKey{}, 0, err
	}
	if graceWindow < 0 {
		graceWindow = 0
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIKey{}, APIKey{}, 0, fmt.Errorf("state: begin org api key activity rotate: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	old, err := scanAPIKeyRow(ctx, tx,
		`select id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		        last_used_at, expires_at, status, revoked_at, rotated_from_id,
		        coalesce(host(created_ip),''), coalesce(created_ua,''), parent_key_id
		   from api_keys where id = $1 for update`, oldKeyID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return APIKey{}, APIKey{}, 0, ErrNotFound
		}
		return APIKey{}, APIKey{}, 0, err
	}
	if old.OrgID != orgID {
		return APIKey{}, APIKey{}, 0, ErrNotFound
	}
	if old.Status == string(APIKeyStatusRevoked) {
		return APIKey{}, APIKey{}, 0, ErrAPIKeyRevoked
	}
	if newLabel == "" {
		newLabel = old.Label
	}
	newKey, err := scanAPIKeyRow(ctx, tx,
		`insert into api_keys (account_id, key_sha256, label, scopes, status, rotated_from_id, org_id, created_ip, created_ua, parent_key_id)
		 values ($1, $2, $3, $4, 'active', $5, $6, $7, $8, $9)
		 returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		           last_used_at, expires_at, status, revoked_at, rotated_from_id,
		           coalesce(host(created_ip),''), coalesce(created_ua,''), parent_key_id`,
		old.AccountID, newHash, newLabel, old.Scopes, oldKeyID, old.OrgID,
		nullString(createdIP), nullString(createdUA), parent)
	if err != nil {
		return APIKey{}, APIKey{}, 0, err
	}
	if graceWindow == 0 {
		old, err = scanAPIKeyRow(ctx, tx,
			`update api_keys
			    set status = 'revoked', expires_at = now(), revoked_at = coalesce(revoked_at, now())
			  where id = $1
			  returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
			            last_used_at, expires_at, status, revoked_at, rotated_from_id,
			            coalesce(host(created_ip),''), coalesce(created_ua,''), parent_key_id`, oldKeyID)
		if err != nil {
			return APIKey{}, APIKey{}, 0, err
		}
	} else {
		old, err = scanAPIKeyRow(ctx, tx,
			`update api_keys
			    set status = 'grace', expires_at = now() + ($1)::interval
			  where id = $2
			  returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
			            last_used_at, expires_at, status, revoked_at, rotated_from_id,
			            coalesce(host(created_ip),''), coalesce(created_ua,''), parent_key_id`,
			graceWindow.String(), oldKeyID)
		if err != nil {
			return APIKey{}, APIKey{}, 0, err
		}
	}
	activity, err = bindOrgActivityToAPIKey(activity, newKey)
	if err != nil {
		return APIKey{}, APIKey{}, 0, err
	}
	outboxID, err := enqueueOrgActivityOutboxTx(ctx, tx, activity)
	if err != nil {
		return APIKey{}, APIKey{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return APIKey{}, APIKey{}, 0, fmt.Errorf("state: commit org api key activity rotate: %w", err)
	}
	return newKey, old, outboxID, nil
}
