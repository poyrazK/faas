package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func scanPlatformTenantConsumerProvisioningPolicy(row pgx.Row, tenantID string) (PlatformTenantConsumerProvisioningPolicy, error) {
	var policy PlatformTenantConsumerProvisioningPolicy
	if err := row.Scan(&policy.Enabled, &policy.MaxConsumers, &policy.UpdatedAt); err != nil {
		return PlatformTenantConsumerProvisioningPolicy{}, err
	}
	policy.TenantID = tenantID
	policy.UpdatedAt = policy.UpdatedAt.UTC()
	return policy, nil
}

func (s *PgStore) GetPlatformTenantConsumerProvisioningPolicy(ctx context.Context, accountID, tenantID string) (PlatformTenantConsumerProvisioningPolicy, error) {
	if !validPlatformTenantConsumerProvisioningPolicy(accountID, tenantID, false, 0) {
		return PlatformTenantConsumerProvisioningPolicy{}, ErrInvalidArgument
	}
	if _, err := s.GetPlatformTenant(ctx, accountID, tenantID); err != nil {
		return PlatformTenantConsumerProvisioningPolicy{}, err
	}
	policy, err := scanPlatformTenantConsumerProvisioningPolicy(s.pool.QueryRow(ctx, `
		select enabled, max_consumers, updated_at
		from platform_tenant_consumer_provisioning_policies
		where account_id = $1::uuid and tenant_id = $2::uuid`, accountID, tenantID), tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantConsumerProvisioningPolicy{TenantID: tenantID}, nil
	}
	if err != nil {
		return PlatformTenantConsumerProvisioningPolicy{}, fmt.Errorf("read platform tenant consumer policy: %w", err)
	}
	return policy, nil
}

func (s *PgStore) SetPlatformTenantConsumerProvisioningPolicy(ctx context.Context, accountID, tenantID string, enabled bool, maxConsumers int) (PlatformTenantConsumerProvisioningPolicy, error) {
	if !validPlatformTenantConsumerProvisioningPolicy(accountID, tenantID, enabled, maxConsumers) {
		return PlatformTenantConsumerProvisioningPolicy{}, ErrInvalidArgument
	}
	_, err := scanPlatformTenantConsumerProvisioningPolicy(s.pool.QueryRow(ctx, `
		insert into platform_tenant_consumer_provisioning_policies
		       (account_id, tenant_id, enabled, max_consumers)
		select account_id, id, $3, $4 from platform_tenants
		where account_id = $1::uuid and id = $2::uuid
		on conflict (tenant_id) do update
		set enabled = excluded.enabled,
		    max_consumers = excluded.max_consumers,
		    updated_at = now()
		where (platform_tenant_consumer_provisioning_policies.enabled,
		       platform_tenant_consumer_provisioning_policies.max_consumers)
		  is distinct from (excluded.enabled, excluded.max_consumers)
		returning enabled, max_consumers, updated_at`, accountID, tenantID, enabled, maxConsumers), tenantID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantConsumerProvisioningPolicy{}, fmt.Errorf("set platform tenant consumer policy: %w", err)
	}
	return s.GetPlatformTenantConsumerProvisioningPolicy(ctx, accountID, tenantID)
}
