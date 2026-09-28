package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
)

func (s *PgStore) PlanPlatformTenantOffboarding(ctx context.Context, accountID, tenantID string) (api.PlatformTenantOffboardingPlanResponse, error) {
	if _, err := uuid.Parse(accountID); err != nil {
		return api.PlatformTenantOffboardingPlanResponse{}, ErrNotFound
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return api.PlatformTenantOffboardingPlanResponse{}, ErrNotFound
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.PlatformTenantOffboardingPlanResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenant, err := scanPlatformTenant(tx.QueryRow(ctx, `select `+platformTenantCols+`
		from platform_tenants where account_id = $1::uuid and id = $2::uuid`, accountID, tenantID))
	if err != nil {
		return api.PlatformTenantOffboardingPlanResponse{}, err
	}
	snapshot := platformTenantOffboardingSnapshot{AccountID: accountID, TenantID: tenantID, Status: tenant.Status}
	now := time.Now().UTC()

	consumerRows, err := tx.Query(ctx, `select `+apiConsumerSelectCols+` from api_consumers
		where account_id = $1::uuid and platform_tenant_id = $2::uuid order by id`, accountID, tenantID)
	if err != nil {
		return api.PlatformTenantOffboardingPlanResponse{}, err
	}
	for consumerRows.Next() {
		consumer, scanErr := scanAPIConsumerRow(consumerRows)
		if scanErr != nil {
			consumerRows.Close()
			return api.PlatformTenantOffboardingPlanResponse{}, scanErr
		}
		snapshot.Consumers = append(snapshot.Consumers, platformTenantOffboardingConsumer{ID: consumer.ID,
			Active: consumer.Active(), Managed: consumer.PlatformTenantManaged})
	}
	if err := consumerRows.Err(); err != nil {
		consumerRows.Close()
		return api.PlatformTenantOffboardingPlanResponse{}, err
	}
	consumerRows.Close()

	surfaceRows, err := tx.Query(ctx, `select `+tenantSurfaceCols+` from tenant_surfaces
		where account_id = $1::uuid and platform_tenant_id = $2::uuid and status <> 'deleted' order by id`, accountID, tenantID)
	if err != nil {
		return api.PlatformTenantOffboardingPlanResponse{}, err
	}
	for surfaceRows.Next() {
		surface, scanErr := scanTenantSurface(surfaceRows)
		if scanErr != nil {
			surfaceRows.Close()
			return api.PlatformTenantOffboardingPlanResponse{}, scanErr
		}
		snapshot.Surfaces = append(snapshot.Surfaces, platformTenantOffboardingSurface{ID: surface.ID,
			Status: surface.Status, Managed: surface.PlatformTenantManaged})
	}
	if err := surfaceRows.Err(); err != nil {
		surfaceRows.Close()
		return api.PlatformTenantOffboardingPlanResponse{}, err
	}
	surfaceRows.Close()

	hostnameRows, err := tx.Query(ctx, `select h.id::text, h.surface_id::text, h.hostname, h.platform_tenant_managed
		from tenant_hostnames h join tenant_surfaces s on s.id = h.surface_id
		where s.account_id = $1::uuid and s.platform_tenant_id = $2::uuid and s.status <> 'deleted'
		order by lower(h.hostname), h.id`, accountID, tenantID)
	if err != nil {
		return api.PlatformTenantOffboardingPlanResponse{}, err
	}
	for hostnameRows.Next() {
		var hostname platformTenantOffboardingHostname
		if err := hostnameRows.Scan(&hostname.ID, &hostname.SurfaceID, &hostname.Hostname, &hostname.Managed); err != nil {
			hostnameRows.Close()
			return api.PlatformTenantOffboardingPlanResponse{}, err
		}
		snapshot.Hostnames = append(snapshot.Hostnames, hostname)
	}
	if err := hostnameRows.Err(); err != nil {
		hostnameRows.Close()
		return api.PlatformTenantOffboardingPlanResponse{}, err
	}
	hostnameRows.Close()

	keyRows, err := tx.Query(ctx, `select k.id::text, k.consumer_id::text, k.app_id::text, k.revoked_at, k.expires_at
		from consumer_keys k join api_consumers c on c.id = k.consumer_id
		where k.account_id = $1::uuid and c.account_id = $1::uuid and c.platform_tenant_id = $2::uuid
		order by k.id`, accountID, tenantID)
	if err != nil {
		return api.PlatformTenantOffboardingPlanResponse{}, err
	}
	for keyRows.Next() {
		var credential platformTenantOffboardingCredential
		var revokedAt, expiresAt pgtype.Timestamptz
		if err := keyRows.Scan(&credential.ID, &credential.ConsumerID, &credential.AppID, &revokedAt, &expiresAt); err != nil {
			keyRows.Close()
			return api.PlatformTenantOffboardingPlanResponse{}, err
		}
		credential.Revoked = revokedAt.Valid
		credential.Active = !revokedAt.Valid && (!expiresAt.Valid || expiresAt.Time.After(now))
		if expiresAt.Valid {
			credential.ExpiresAt = expiresAt.Time.UTC().Format(time.RFC3339Nano)
		}
		snapshot.ConsumerKeys = append(snapshot.ConsumerKeys, credential)
	}
	if err := keyRows.Err(); err != nil {
		keyRows.Close()
		return api.PlatformTenantOffboardingPlanResponse{}, err
	}
	keyRows.Close()

	tokenRows, err := tx.Query(ctx, `select id::text, revoked_at, expires_at
		from platform_tenant_access_tokens where account_id = $1::uuid and platform_tenant_id = $2::uuid order by id`, accountID, tenantID)
	if err != nil {
		return api.PlatformTenantOffboardingPlanResponse{}, err
	}
	for tokenRows.Next() {
		var token platformTenantOffboardingCredential
		var revokedAt pgtype.Timestamptz
		var expiresAt time.Time
		if err := tokenRows.Scan(&token.ID, &revokedAt, &expiresAt); err != nil {
			tokenRows.Close()
			return api.PlatformTenantOffboardingPlanResponse{}, err
		}
		token.Revoked = revokedAt.Valid
		token.Active = !revokedAt.Valid && expiresAt.After(now)
		token.ExpiresAt = expiresAt.UTC().Format(time.RFC3339Nano)
		snapshot.AccessTokens = append(snapshot.AccessTokens, token)
	}
	if err := tokenRows.Err(); err != nil {
		tokenRows.Close()
		return api.PlatformTenantOffboardingPlanResponse{}, err
	}
	tokenRows.Close()

	if err := tx.QueryRow(ctx, `select allowed_scopes, max_keys_per_consumer
		from platform_tenant_credential_policies where account_id = $1::uuid and tenant_id = $2::uuid`, accountID, tenantID).
		Scan(&snapshot.CredentialScopes, &snapshot.MaxKeysPerConsumer); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return api.PlatformTenantOffboardingPlanResponse{}, err
	}
	if err := tx.QueryRow(ctx, `select enabled, max_consumers
		from platform_tenant_consumer_provisioning_policies where account_id = $1::uuid and tenant_id = $2::uuid`, accountID, tenantID).
		Scan(&snapshot.CustomerProvisioningEnabled, &snapshot.MaxConsumers); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return api.PlatformTenantOffboardingPlanResponse{}, err
	}
	if err := tx.QueryRow(ctx, `select allowed_suffixes, max_hostnames
		from platform_tenant_hostname_policies where account_id = $1::uuid and tenant_id = $2::uuid`, accountID, tenantID).
		Scan(&snapshot.AllowedHostnameSuffixes, &snapshot.MaxHostnames); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return api.PlatformTenantOffboardingPlanResponse{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.PlatformTenantOffboardingPlanResponse{}, err
	}
	return buildPlatformTenantOffboardingPlan(snapshot)
}
