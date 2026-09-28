package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
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
		select account_id, id, coalesce($3::text[], ARRAY[]::text[]), $4 from platform_tenants
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

func (s *PgStore) CreatePlatformTenantDelegatedHostname(ctx context.Context, accountID, tenantID, surfaceID, hostname, challengeToken string, limits api.Limits) (PlatformTenantDelegatedHostnameResult, error) {
	hostname = surfaceHostnameCanonical(hostname)
	if !validPlatformTenantHostnamePolicy(accountID, tenantID, nil, 0) || !validPlatformTenantHostname(hostname) ||
		!validUUID(surfaceID) || challengeToken == "" || limits.TenantHostnamesPerSurface < 1 {
		return PlatformTenantDelegatedHostnameResult{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PlatformTenantDelegatedHostnameResult{}, fmt.Errorf("begin delegated platform tenant hostname: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var tenantActive bool
	if err := tx.QueryRow(ctx, `select status = 'active' from platform_tenants
		where account_id = $1::uuid and id = $2::uuid for share`, accountID, tenantID).Scan(&tenantActive); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PlatformTenantDelegatedHostnameResult{}, ErrNotFound
		}
		return PlatformTenantDelegatedHostnameResult{}, fmt.Errorf("check delegated hostname tenant status: %w", err)
	}
	if !tenantActive {
		return PlatformTenantDelegatedHostnameResult{}, ErrPlatformTenantSuspended
	}
	var locked int
	err = tx.QueryRow(ctx, `select 1 from tenant_surfaces
		where account_id = $1::uuid and platform_tenant_id = $2::uuid and id = $3::uuid and status <> 'deleted'
		for update`, accountID, tenantID, surfaceID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantDelegatedHostnameResult{}, ErrNotFound
	}
	if err != nil {
		return PlatformTenantDelegatedHostnameResult{}, fmt.Errorf("lock linked tenant surface: %w", err)
	}

	var suffixes []string
	var maxHostnames int
	err = tx.QueryRow(ctx, `select allowed_suffixes, max_hostnames
		from platform_tenant_hostname_policies
		where account_id = $1::uuid and tenant_id = $2::uuid for update`, accountID, tenantID).
		Scan(&suffixes, &maxHostnames)
	policyConfigured := err == nil
	if errors.Is(err, pgx.ErrNoRows) {
		suffixes = []string{}
	} else if err != nil {
		return PlatformTenantDelegatedHostnameResult{}, fmt.Errorf("lock delegated hostname policy: %w", err)
	}
	var existing TenantHostname
	existing, err = scanTenantHostname(tx.QueryRow(ctx, `select `+tenantHostnameCols+` from tenant_hostnames where hostname = $1`, hostname))
	if err == nil {
		if existing.SurfaceID != surfaceID {
			return PlatformTenantDelegatedHostnameResult{}, ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return PlatformTenantDelegatedHostnameResult{}, fmt.Errorf("commit delegated hostname replay: %w", err)
		}
		return PlatformTenantDelegatedHostnameResult{Hostname: existing, Action: "unchanged"}, nil
	}
	// scanTenantHostname uses mapErr, which translates pgx.ErrNoRows to
	// ErrNotFound. Treat that result as an absent hostname so a first
	// delegated registration can proceed.
	if !errors.Is(err, ErrNotFound) {
		return PlatformTenantDelegatedHostnameResult{}, fmt.Errorf("read delegated hostname replay: %w", err)
	}
	if !policyConfigured || len(suffixes) == 0 || maxHostnames == 0 {
		return PlatformTenantDelegatedHostnameResult{}, ErrPlatformTenantHostnameDelegationDisabled
	}
	if !platformTenantHostnameAllowed(hostname, suffixes) {
		return PlatformTenantDelegatedHostnameResult{}, ErrPlatformTenantHostnameSuffixNotAllowed
	}
	var tenantObserved int
	if err := tx.QueryRow(ctx, `select count(*) from tenant_hostnames h
		join tenant_surfaces sf on sf.id = h.surface_id
		where sf.account_id = $1::uuid and sf.platform_tenant_id = $2::uuid and sf.status <> 'deleted'`, accountID, tenantID).Scan(&tenantObserved); err != nil {
		return PlatformTenantDelegatedHostnameResult{}, fmt.Errorf("count delegated tenant hostnames: %w", err)
	}
	if tenantObserved >= maxHostnames {
		return PlatformTenantDelegatedHostnameResult{}, &PlatformTenantDelegatedHostnameQuotaError{Limit: maxHostnames, Observed: tenantObserved}
	}
	var surfaceVerified int
	if err := tx.QueryRow(ctx, `select count(*) from tenant_hostnames where surface_id = $1::uuid and verified_at is not null`, surfaceID).Scan(&surfaceVerified); err != nil {
		return PlatformTenantDelegatedHostnameResult{}, fmt.Errorf("count delegated surface hostnames: %w", err)
	}
	if surfaceVerified >= limits.TenantHostnamesPerSurface {
		return PlatformTenantDelegatedHostnameResult{}, &TenantHostnameQuotaError{Limit: limits.TenantHostnamesPerSurface,
			Observed: surfaceVerified, SurfaceID: surfaceID}
	}
	created, err := scanTenantHostname(tx.QueryRow(ctx, `insert into tenant_hostnames (surface_id, hostname, challenge_token)
		values ($1::uuid, $2, $3) returning `+tenantHostnameCols, surfaceID, hostname, challengeToken))
	if err != nil {
		return PlatformTenantDelegatedHostnameResult{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PlatformTenantDelegatedHostnameResult{}, fmt.Errorf("commit delegated hostname: %w", err)
	}
	return PlatformTenantDelegatedHostnameResult{Hostname: created, Action: "created"}, nil
}

func validUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}
