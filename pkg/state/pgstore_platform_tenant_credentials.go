package state

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PgStore) ApplyPlatformTenantCredentials(ctx context.Context, in ApplyPlatformTenantCredentialsParams) (ApplyPlatformTenantCredentialsResult, error) {
	if err := validatePlatformTenantCredentials(in); err != nil {
		return ApplyPlatformTenantCredentialsResult{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ApplyPlatformTenantCredentialsResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var accountID string
	if err := tx.QueryRow(ctx, `select id from accounts where id = $1::uuid for update`, in.AccountID).Scan(&accountID); err != nil {
		return ApplyPlatformTenantCredentialsResult{}, applyNoRows(err)
	}
	result, err := planPlatformTenantCredentials(ctx, tx, in)
	if err != nil || in.DryRun {
		return result, err
	}
	if err := commitPlatformTenantCredentials(ctx, tx, in, &result); err != nil {
		return ApplyPlatformTenantCredentialsResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ApplyPlatformTenantCredentialsResult{}, err
	}
	return result, nil
}

func planPlatformTenantCredentials(ctx context.Context, tx pgx.Tx, in ApplyPlatformTenantCredentialsParams) (ApplyPlatformTenantCredentialsResult, error) {
	result := ApplyPlatformTenantCredentialsResult{TenantID: in.TenantID, DryRun: in.DryRun,
		Keys: make([]PlatformTenantCredentialResult, 0, len(in.Keys)+len(in.RevokeKeyIDs))}
	var status string
	if err := tx.QueryRow(ctx, `select status from platform_tenants where id = $1::uuid and account_id = $2::uuid for update`,
		in.TenantID, in.AccountID).Scan(&status); err != nil {
		return result, applyNoRows(err)
	}
	if len(in.Keys) > 0 && status != PlatformTenantActive {
		return result, ErrConflict
	}
	var accountCount int
	if err := tx.QueryRow(ctx, `select count(*) from consumer_keys where account_id = $1::uuid and revoked_at is null`,
		in.AccountID).Scan(&accountCount); err != nil {
		return result, err
	}
	appCounts := make(map[string]int)
	loadAppCount := func(appID string) (int, error) {
		if count, ok := appCounts[appID]; ok {
			return count, nil
		}
		var count int
		err := tx.QueryRow(ctx, `select count(*) from consumer_keys where account_id = $1::uuid and app_id = $2::uuid and revoked_at is null`,
			in.AccountID, appID).Scan(&count)
		appCounts[appID] = count
		return count, err
	}
	revoking := make(map[string]bool, len(in.RevokeKeyIDs))
	for _, id := range in.RevokeKeyIDs {
		key, err := scanConsumerKeyRow(tx.QueryRow(ctx, `select `+consumerKeySelectCols+` from consumer_keys
			where id = $1::uuid and account_id = $2::uuid and consumer_id in
			(select id from api_consumers where platform_tenant_id = $3::uuid) for update`, id, in.AccountID, in.TenantID))
		if errors.Is(err, pgx.ErrNoRows) {
			return result, ErrNotFound
		}
		if err != nil {
			return result, err
		}
		revoking[id] = true
		item := PlatformTenantCredentialResult{Key: key, Action: "unchanged"}
		if key.RevokedAt == nil {
			item.Action = "revoke"
			accountCount--
			count, err := loadAppCount(key.AppID)
			if err != nil {
				return result, err
			}
			appCounts[key.AppID] = count - 1
		}
		result.Keys = append(result.Keys, item)
	}
	seenNames := make(map[string]bool, len(in.Keys))
	for _, wanted := range in.Keys {
		consumer, err := scanAPIConsumerRow(tx.QueryRow(ctx, `select `+apiConsumerSelectCols+` from api_consumers
			where id = $1::uuid and account_id = $2::uuid for update`, wanted.ConsumerID, in.AccountID))
		if errors.Is(err, pgx.ErrNoRows) {
			return result, ErrNotFound
		}
		if err != nil {
			return result, err
		}
		if consumer.PlatformTenantID != in.TenantID || !consumer.Active() {
			return result, ErrConflict
		}
		nameKey := consumer.AppID + "\x00" + wanted.Name
		if seenNames[nameKey] {
			return result, ErrConflict
		}
		seenNames[nameKey] = true
		key, err := scanConsumerKeyRow(tx.QueryRow(ctx, `select `+consumerKeySelectCols+` from consumer_keys
			where account_id = $1::uuid and app_id = $2::uuid and name = $3 for update`,
			in.AccountID, consumer.AppID, wanted.Name))
		if errors.Is(err, pgx.ErrNoRows) {
			if wanted.ExpiresAt != nil && !wanted.ExpiresAt.After(time.Now()) {
				return result, ErrInvalidArgument
			}
			var prefixUsed bool
			if err := tx.QueryRow(ctx, `select exists(select 1 from consumer_keys where app_id = $1::uuid and prefix = $2)`,
				consumer.AppID, wanted.Prefix).Scan(&prefixUsed); err != nil {
				return result, err
			}
			if prefixUsed {
				return result, ErrConflict
			}
			key = ConsumerKey{AccountID: in.AccountID, AppID: consumer.AppID, ConsumerID: consumer.ID,
				Name: wanted.Name, Prefix: wanted.Prefix, Hash: append([]byte(nil), wanted.Hash...),
				Scopes: append([]string(nil), wanted.Scopes...), ExpiresAt: wanted.ExpiresAt}
			accountCount++
			count, err := loadAppCount(consumer.AppID)
			if err != nil {
				return result, err
			}
			appCounts[consumer.AppID] = count + 1
			result.Keys = append(result.Keys, PlatformTenantCredentialResult{Key: key, Action: "create"})
		} else if err != nil {
			return result, err
		} else {
			if !sameCredentialIntent(key, wanted) || revoking[key.ID] {
				return result, ErrConflict
			}
			result.Keys = append(result.Keys, PlatformTenantCredentialResult{Key: key, Action: "unchanged"})
		}
	}
	hasCreate := false
	createdByApp := make(map[string]bool)
	for _, item := range result.Keys {
		if item.Action == "create" {
			hasCreate = true
			createdByApp[item.Key.AppID] = true
		}
	}
	if hasCreate && accountCount > in.AccountLimit {
		return result, &PlatformTenantCredentialQuotaError{Scope: "account", Limit: in.AccountLimit, Observed: accountCount - 1}
	}
	for appID, count := range appCounts {
		if createdByApp[appID] && count > in.AppLimit {
			return result, &PlatformTenantCredentialQuotaError{Scope: "app", Limit: in.AppLimit, Observed: count - 1}
		}
	}
	if in.EnforceDelegationPolicy {
		policy, err := lockPlatformTenantCredentialPolicy(ctx, tx, in.AccountID, in.TenantID)
		if err != nil {
			return result, err
		}
		creates, err := validatePlatformTenantCredentialPolicyCreates(policy, result)
		if err != nil {
			return result, err
		}
		if err := enforcePlatformTenantCredentialConsumerLimit(ctx, tx, in, policy, creates, result); err != nil {
			return result, err
		}
	}
	return result, nil
}

func enforcePlatformTenantCredentialConsumerLimit(ctx context.Context, tx pgx.Tx, in ApplyPlatformTenantCredentialsParams,
	policy PlatformTenantCredentialPolicy, creates map[string]int, result ApplyPlatformTenantCredentialsResult) error {
	if len(creates) == 0 {
		return nil
	}
	consumerIDs := make([]string, 0, len(creates))
	for consumerID := range creates {
		consumerIDs = append(consumerIDs, consumerID)
	}
	sort.Strings(consumerIDs)
	now := time.Now().UTC()
	rows, err := tx.Query(ctx, `select consumer_id::text, count(*)
		from consumer_keys
		where account_id = $1::uuid and consumer_id = any($2::uuid[])
		  and revoked_at is null and (expires_at is null or expires_at > $3)
		group by consumer_id`, in.AccountID, consumerIDs, now)
	if err != nil {
		return err
	}
	counts := make(map[string]int, len(consumerIDs))
	for rows.Next() {
		var consumerID string
		var count int
		if err := rows.Scan(&consumerID, &count); err != nil {
			rows.Close()
			return err
		}
		counts[consumerID] = count
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range result.Keys {
		if item.Action == "revoke" && activePlatformTenantCredentialKey(item.Key, now) {
			counts[item.Key.ConsumerID]--
		}
	}
	for consumerID, added := range creates {
		observed := counts[consumerID] + added
		if observed > policy.MaxKeysPerConsumer {
			return &PlatformTenantCredentialPolicyQuotaError{Limit: policy.MaxKeysPerConsumer, Observed: observed}
		}
	}
	return nil
}

func commitPlatformTenantCredentials(ctx context.Context, tx pgx.Tx, in ApplyPlatformTenantCredentialsParams, result *ApplyPlatformTenantCredentialsResult) error {
	for i := range result.Keys {
		item := &result.Keys[i]
		switch item.Action {
		case "create":
			key, err := scanConsumerKeyRow(tx.QueryRow(ctx, `insert into consumer_keys
				(account_id, app_id, consumer_id, name, prefix, hashed_secret, scopes, expires_at)
				values ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8)
				returning `+consumerKeySelectCols, in.AccountID, item.Key.AppID, item.Key.ConsumerID,
				item.Key.Name, item.Key.Prefix, item.Key.Hash, item.Key.Scopes, item.Key.ExpiresAt))
			if err != nil {
				return applyWriteError(err)
			}
			item.Key = key
		case "revoke":
			key, err := scanConsumerKeyRow(tx.QueryRow(ctx, `update consumer_keys set revoked_at = coalesce(revoked_at, now())
				where id = $1::uuid returning `+consumerKeySelectCols, item.Key.ID))
			if err != nil {
				return applyWriteError(err)
			}
			item.Key = key
		}
	}
	return nil
}

func (s *PgStore) ListPlatformTenantCredentials(ctx context.Context, accountID, tenantID string, limit, offset int) ([]ConsumerKey, error) {
	if _, err := s.GetPlatformTenant(ctx, accountID, tenantID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `select `+consumerKeySelectCols+` from consumer_keys where account_id = $1::uuid
		and consumer_id in (select id from api_consumers where account_id = $1::uuid and platform_tenant_id = $2::uuid)
		order by created_at desc, id desc limit $3 offset $4`, accountID, tenantID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ConsumerKey, 0)
	for rows.Next() {
		key, err := scanConsumerKeyRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	return out, rows.Err()
}
