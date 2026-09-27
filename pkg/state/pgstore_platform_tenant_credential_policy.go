package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func lockPlatformTenantCredentialPolicy(ctx context.Context, tx pgx.Tx, accountID, tenantID string) (PlatformTenantCredentialPolicy, error) {
	policy, err := scanPlatformTenantCredentialPolicy(tx.QueryRow(ctx, `
		select allowed_scopes, max_keys_per_consumer, updated_at
		from platform_tenant_credential_policies
		where account_id = $1::uuid and tenant_id = $2::uuid
		for update`, accountID, tenantID), tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantCredentialPolicy{TenantID: tenantID, AllowedScopes: []string{}}, nil
	}
	return policy, err
}

func scanPlatformTenantCredentialPolicy(row pgx.Row, tenantID string) (PlatformTenantCredentialPolicy, error) {
	var policy PlatformTenantCredentialPolicy
	if err := row.Scan(&policy.AllowedScopes, &policy.MaxKeysPerConsumer, &policy.UpdatedAt); err != nil {
		return PlatformTenantCredentialPolicy{}, err
	}
	policy.TenantID = tenantID
	if policy.AllowedScopes == nil {
		policy.AllowedScopes = []string{}
	}
	policy.UpdatedAt = policy.UpdatedAt.UTC()
	return policy, nil
}

func (s *PgStore) GetPlatformTenantCredentialPolicy(ctx context.Context, accountID, tenantID string) (PlatformTenantCredentialPolicy, error) {
	if !validPlatformTenantCredentialPolicy(accountID, tenantID, nil, 0) {
		return PlatformTenantCredentialPolicy{}, ErrInvalidArgument
	}
	if _, err := s.GetPlatformTenant(ctx, accountID, tenantID); err != nil {
		return PlatformTenantCredentialPolicy{}, err
	}
	policy, err := scanPlatformTenantCredentialPolicy(s.pool.QueryRow(ctx, `
		select allowed_scopes, max_keys_per_consumer, updated_at
		from platform_tenant_credential_policies where account_id = $1::uuid and tenant_id = $2::uuid`,
		accountID, tenantID), tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantCredentialPolicy{TenantID: tenantID, AllowedScopes: []string{}}, nil
	}
	if err != nil {
		return PlatformTenantCredentialPolicy{}, fmt.Errorf("read platform tenant credential policy: %w", err)
	}
	return policy, nil
}

func (s *PgStore) SetPlatformTenantCredentialPolicy(ctx context.Context, accountID, tenantID string, scopes []string, maxKeysPerConsumer int) (PlatformTenantCredentialPolicy, error) {
	if !validPlatformTenantCredentialPolicy(accountID, tenantID, scopes, maxKeysPerConsumer) {
		return PlatformTenantCredentialPolicy{}, ErrInvalidArgument
	}
	_, err := scanPlatformTenantCredentialPolicy(s.pool.QueryRow(ctx, `
		insert into platform_tenant_credential_policies
		       (account_id, tenant_id, allowed_scopes, max_keys_per_consumer)
		select account_id, id, coalesce($3::text[], ARRAY[]::text[]), $4 from platform_tenants
		where account_id = $1::uuid and id = $2::uuid
		on conflict (tenant_id) do update
		set allowed_scopes = excluded.allowed_scopes,
		    max_keys_per_consumer = excluded.max_keys_per_consumer,
		    updated_at = now()
		where (platform_tenant_credential_policies.allowed_scopes,
		       platform_tenant_credential_policies.max_keys_per_consumer)
		  is distinct from (excluded.allowed_scopes, excluded.max_keys_per_consumer)
		returning allowed_scopes, max_keys_per_consumer, updated_at`,
		accountID, tenantID, scopes, maxKeysPerConsumer), tenantID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantCredentialPolicy{}, fmt.Errorf("set platform tenant credential policy: %w", err)
	}
	return s.GetPlatformTenantCredentialPolicy(ctx, accountID, tenantID)
}
