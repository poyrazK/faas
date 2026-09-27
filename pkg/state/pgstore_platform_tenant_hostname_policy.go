package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func scanPlatformTenantHostnamePolicy(row pgx.Row, tenantID string) (PlatformTenantHostnamePolicy, error) {
	var policy PlatformTenantHostnamePolicy
	if err := row.Scan(&policy.AllowedSuffixes, &policy.MaxHostnames, &policy.UpdatedAt); err != nil {
		return PlatformTenantHostnamePolicy{}, err
	}
	policy.TenantID = tenantID
	if policy.AllowedSuffixes == nil {
		policy.AllowedSuffixes = []string{}
	}
	policy.UpdatedAt = policy.UpdatedAt.UTC()
	return policy, nil
}

func (s *PgStore) GetPlatformTenantHostnamePolicy(ctx context.Context, accountID, tenantID string) (PlatformTenantHostnamePolicy, error) {
	if !validPlatformTenantHostnamePolicy(accountID, tenantID, nil, 0) {
		return PlatformTenantHostnamePolicy{}, ErrInvalidArgument
	}
	if _, err := s.GetPlatformTenant(ctx, accountID, tenantID); err != nil {
		return PlatformTenantHostnamePolicy{}, err
	}
	policy, err := scanPlatformTenantHostnamePolicy(s.pool.QueryRow(ctx, `
		select allowed_suffixes, max_hostnames, updated_at
		from platform_tenant_hostname_policies where account_id = $1::uuid and tenant_id = $2::uuid`,
		accountID, tenantID), tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantHostnamePolicy{TenantID: tenantID, AllowedSuffixes: []string{}}, nil
	}
	if err != nil {
		return PlatformTenantHostnamePolicy{}, fmt.Errorf("read platform tenant hostname policy: %w", err)
	}
	return policy, nil
}

func (s *PgStore) SetPlatformTenantHostnamePolicy(ctx context.Context, accountID, tenantID string, suffixes []string, maxHostnames int) (PlatformTenantHostnamePolicy, error) {
	if !validPlatformTenantHostnamePolicy(accountID, tenantID, suffixes, maxHostnames) {
		return PlatformTenantHostnamePolicy{}, ErrInvalidArgument
	}
	_, err := scanPlatformTenantHostnamePolicy(s.pool.QueryRow(ctx, `
		insert into platform_tenant_hostname_policies
		       (account_id, tenant_id, allowed_suffixes, max_hostnames)
		select account_id, id, $3::text[], $4 from platform_tenants
		where account_id = $1::uuid and id = $2::uuid
		on conflict (tenant_id) do update
		set allowed_suffixes = excluded.allowed_suffixes,
		    max_hostnames = excluded.max_hostnames,
		    updated_at = now()
		where (platform_tenant_hostname_policies.allowed_suffixes,
		       platform_tenant_hostname_policies.max_hostnames)
		  is distinct from (excluded.allowed_suffixes, excluded.max_hostnames)
		returning allowed_suffixes, max_hostnames, updated_at`,
		accountID, tenantID, suffixes, maxHostnames), tenantID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantHostnamePolicy{}, fmt.Errorf("set platform tenant hostname policy: %w", err)
	}
	return s.GetPlatformTenantHostnamePolicy(ctx, accountID, tenantID)
}
