package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	now, err := platformTenantOffboardingDatabaseTime(ctx, tx)
	if err != nil {
		return api.PlatformTenantOffboardingPlanResponse{}, err
	}
	snapshot, err := platformTenantOffboardingSnapshotTx(ctx, tx, accountID, tenantID, now, false)
	if err != nil {
		return api.PlatformTenantOffboardingPlanResponse{}, err
	}
	return buildPlatformTenantOffboardingPlan(snapshot)
}

func (s *PgStore) ApplyPlatformTenantOffboarding(ctx context.Context, accountID, tenantID, expectedPlanHash string) (api.PlatformTenantOffboardingApplyResponse, error) {
	if _, err := uuid.Parse(accountID); err != nil {
		return api.PlatformTenantOffboardingApplyResponse{}, ErrNotFound
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return api.PlatformTenantOffboardingApplyResponse{}, ErrNotFound
	}
	if !validPlatformTenantPlanHash(expectedPlanHash) {
		return api.PlatformTenantOffboardingApplyResponse{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.PlatformTenantOffboardingApplyResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockPlatformTenantAccount(ctx, tx, accountID); err != nil {
		return api.PlatformTenantOffboardingApplyResponse{}, err
	}
	now, err := platformTenantOffboardingDatabaseTime(ctx, tx)
	if err != nil {
		return api.PlatformTenantOffboardingApplyResponse{}, err
	}
	snapshot, err := platformTenantOffboardingSnapshotTx(ctx, tx, accountID, tenantID, now, true)
	if err != nil {
		return api.PlatformTenantOffboardingApplyResponse{}, err
	}
	plan, err := buildPlatformTenantOffboardingPlan(snapshot)
	if err != nil {
		return api.PlatformTenantOffboardingApplyResponse{}, err
	}
	if !platformTenantPlanHashMatches(expectedPlanHash, plan.PlanHash) {
		return api.PlatformTenantOffboardingApplyResponse{}, ErrPlatformTenantPlanStale
	}
	if err := applyPlatformTenantOffboardingTx(ctx, tx, accountID, tenantID, now, plan.Actions); err != nil {
		return api.PlatformTenantOffboardingApplyResponse{}, err
	}
	actionsJSON, err := json.Marshal(plan.Actions)
	if err != nil {
		return api.PlatformTenantOffboardingApplyResponse{}, err
	}
	response := api.PlatformTenantOffboardingApplyResponse{TenantID: tenantID, ReceiptID: uuid.NewString(),
		PlanHash: plan.PlanHash, Applied: true, Actions: plan.Actions}
	if err := tx.QueryRow(ctx, `insert into platform_tenant_offboarding_receipts
		(account_id, tenant_id, receipt_id, plan_hash, actions)
		values ($1::uuid, $2::uuid, $3::uuid, $4, $5::jsonb)
		returning applied_at`, accountID, tenantID, response.ReceiptID, response.PlanHash, actionsJSON).Scan(&response.AppliedAt); err != nil {
		return api.PlatformTenantOffboardingApplyResponse{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.PlatformTenantOffboardingApplyResponse{}, err
	}
	return response, nil
}

func platformTenantOffboardingDatabaseTime(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var now time.Time
	if err := tx.QueryRow(ctx, `select now()`).Scan(&now); err != nil {
		return time.Time{}, err
	}
	return now.UTC(), nil
}

func platformTenantOffboardingSnapshotTx(ctx context.Context, tx pgx.Tx, accountID, tenantID string, now time.Time, lock bool) (platformTenantOffboardingSnapshot, error) {
	lockSuffix := ""
	if lock {
		lockSuffix = " for update"
	}
	tenant, err := scanPlatformTenant(tx.QueryRow(ctx, `select `+platformTenantCols+`
		from platform_tenants where account_id = $1::uuid and id = $2::uuid`+lockSuffix, accountID, tenantID))
	if err != nil {
		return platformTenantOffboardingSnapshot{}, err
	}
	snapshot := platformTenantOffboardingSnapshot{AccountID: accountID, TenantID: tenantID, Status: tenant.Status}
	consumerRows, err := tx.Query(ctx, `select `+apiConsumerSelectCols+` from api_consumers
		where account_id = $1::uuid and platform_tenant_id = $2::uuid order by id`+lockSuffix, accountID, tenantID)
	if err != nil {
		return platformTenantOffboardingSnapshot{}, err
	}
	for consumerRows.Next() {
		consumer, scanErr := scanAPIConsumerRow(consumerRows)
		if scanErr != nil {
			consumerRows.Close()
			return platformTenantOffboardingSnapshot{}, scanErr
		}
		snapshot.Consumers = append(snapshot.Consumers, platformTenantOffboardingConsumer{ID: consumer.ID,
			Active: consumer.Active(), Managed: consumer.PlatformTenantManaged})
	}
	if err := consumerRows.Err(); err != nil {
		consumerRows.Close()
		return platformTenantOffboardingSnapshot{}, err
	}
	consumerRows.Close()

	surfaceRows, err := tx.Query(ctx, `select `+tenantSurfaceCols+` from tenant_surfaces
		where account_id = $1::uuid and platform_tenant_id = $2::uuid and status <> 'deleted' order by id`+lockSuffix,
		accountID, tenantID)
	if err != nil {
		return platformTenantOffboardingSnapshot{}, err
	}
	for surfaceRows.Next() {
		surface, scanErr := scanTenantSurface(surfaceRows)
		if scanErr != nil {
			surfaceRows.Close()
			return platformTenantOffboardingSnapshot{}, scanErr
		}
		snapshot.Surfaces = append(snapshot.Surfaces, platformTenantOffboardingSurface{ID: surface.ID,
			Status: surface.Status, Managed: surface.PlatformTenantManaged})
	}
	if err := surfaceRows.Err(); err != nil {
		surfaceRows.Close()
		return platformTenantOffboardingSnapshot{}, err
	}
	surfaceRows.Close()

	hostnameLock := ""
	if lock {
		hostnameLock = " for update of h"
	}
	hostnameRows, err := tx.Query(ctx, `select h.id::text, h.surface_id::text, h.hostname, h.platform_tenant_managed
		from tenant_hostnames h join tenant_surfaces s on s.id = h.surface_id
		where s.account_id = $1::uuid and s.platform_tenant_id = $2::uuid and s.status <> 'deleted'
		order by lower(h.hostname), h.id`+hostnameLock, accountID, tenantID)
	if err != nil {
		return platformTenantOffboardingSnapshot{}, err
	}
	for hostnameRows.Next() {
		var hostname platformTenantOffboardingHostname
		if err := hostnameRows.Scan(&hostname.ID, &hostname.SurfaceID, &hostname.Hostname, &hostname.Managed); err != nil {
			hostnameRows.Close()
			return platformTenantOffboardingSnapshot{}, err
		}
		snapshot.Hostnames = append(snapshot.Hostnames, hostname)
	}
	if err := hostnameRows.Err(); err != nil {
		hostnameRows.Close()
		return platformTenantOffboardingSnapshot{}, err
	}
	hostnameRows.Close()

	keyLock := ""
	if lock {
		keyLock = " for update of k"
	}
	keyRows, err := tx.Query(ctx, `select k.id::text, k.consumer_id::text, k.app_id::text, k.revoked_at, k.expires_at
		from consumer_keys k join api_consumers c on c.id = k.consumer_id
		where k.account_id = $1::uuid and c.account_id = $1::uuid and c.platform_tenant_id = $2::uuid
		order by k.id`+keyLock, accountID, tenantID)
	if err != nil {
		return platformTenantOffboardingSnapshot{}, err
	}
	for keyRows.Next() {
		var credential platformTenantOffboardingCredential
		var revokedAt, expiresAt pgtype.Timestamptz
		if err := keyRows.Scan(&credential.ID, &credential.ConsumerID, &credential.AppID, &revokedAt, &expiresAt); err != nil {
			keyRows.Close()
			return platformTenantOffboardingSnapshot{}, err
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
		return platformTenantOffboardingSnapshot{}, err
	}
	keyRows.Close()

	tokenLock := ""
	if lock {
		tokenLock = " for update"
	}
	tokenRows, err := tx.Query(ctx, `select id::text, revoked_at, expires_at
		from platform_tenant_access_tokens where account_id = $1::uuid and platform_tenant_id = $2::uuid order by id`+tokenLock,
		accountID, tenantID)
	if err != nil {
		return platformTenantOffboardingSnapshot{}, err
	}
	for tokenRows.Next() {
		var token platformTenantOffboardingCredential
		var revokedAt pgtype.Timestamptz
		var expiresAt time.Time
		if err := tokenRows.Scan(&token.ID, &revokedAt, &expiresAt); err != nil {
			tokenRows.Close()
			return platformTenantOffboardingSnapshot{}, err
		}
		token.Revoked = revokedAt.Valid
		token.Active = !revokedAt.Valid && expiresAt.After(now)
		token.ExpiresAt = expiresAt.UTC().Format(time.RFC3339Nano)
		snapshot.AccessTokens = append(snapshot.AccessTokens, token)
	}
	if err := tokenRows.Err(); err != nil {
		tokenRows.Close()
		return platformTenantOffboardingSnapshot{}, err
	}
	tokenRows.Close()

	policyLock := ""
	if lock {
		policyLock = " for update"
	}
	if err := tx.QueryRow(ctx, `select allowed_scopes, max_keys_per_consumer
		from platform_tenant_credential_policies where account_id = $1::uuid and tenant_id = $2::uuid`+policyLock,
		accountID, tenantID).Scan(&snapshot.CredentialScopes, &snapshot.MaxKeysPerConsumer); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return platformTenantOffboardingSnapshot{}, err
	}
	if err := tx.QueryRow(ctx, `select enabled, max_consumers
		from platform_tenant_consumer_provisioning_policies where account_id = $1::uuid and tenant_id = $2::uuid`+policyLock,
		accountID, tenantID).Scan(&snapshot.CustomerProvisioningEnabled, &snapshot.MaxConsumers); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return platformTenantOffboardingSnapshot{}, err
	}
	if err := tx.QueryRow(ctx, `select allowed_suffixes, max_hostnames
		from platform_tenant_hostname_policies where account_id = $1::uuid and tenant_id = $2::uuid`+policyLock,
		accountID, tenantID).Scan(&snapshot.AllowedHostnameSuffixes, &snapshot.MaxHostnames); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return platformTenantOffboardingSnapshot{}, err
	}
	return snapshot, nil
}

func applyPlatformTenantOffboardingTx(ctx context.Context, tx pgx.Tx, accountID, tenantID string, now time.Time, actions api.PlatformTenantOffboardingPlanActions) error {
	var tag pgconn.CommandTag
	err := tx.QueryRow(ctx, `update platform_tenants set status = 'suspended', updated_at = $3
		where account_id = $1::uuid and id = $2::uuid returning id`, accountID, tenantID, now).Scan(new(string))
	if err != nil {
		return applyNoRows(err)
	}
	if actions.RevokeConsumerKeys > 0 {
		tag, err = tx.Exec(ctx, `update consumer_keys k set revoked_at = $3
			from api_consumers c
			where k.account_id = $1::uuid and c.account_id = $1::uuid and c.platform_tenant_id = $2::uuid
			  and c.id = k.consumer_id and k.revoked_at is null and (k.expires_at is null or k.expires_at > $3)`,
			accountID, tenantID, now)
		if err != nil {
			return err
		}
		if int(tag.RowsAffected()) != actions.RevokeConsumerKeys {
			return ErrPlatformTenantPlanStale
		}
	}
	if actions.RevokeAccessTokens > 0 {
		tag, err = tx.Exec(ctx, `update platform_tenant_access_tokens set revoked_at = $3
			where account_id = $1::uuid and platform_tenant_id = $2::uuid
			  and revoked_at is null and expires_at > $3`, accountID, tenantID, now)
		if err != nil {
			return err
		}
		if int(tag.RowsAffected()) != actions.RevokeAccessTokens {
			return ErrPlatformTenantPlanStale
		}
	}
	if _, err := tx.Exec(ctx, `update platform_tenant_credential_policies set
		allowed_scopes = ARRAY[]::text[], max_keys_per_consumer = 0, updated_at = $3
		where account_id = $1::uuid and tenant_id = $2::uuid
		  and (allowed_scopes is distinct from ARRAY[]::text[] or max_keys_per_consumer is distinct from 0)`,
		accountID, tenantID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update platform_tenant_consumer_provisioning_policies set
		enabled = false, max_consumers = 0, updated_at = $3
		where account_id = $1::uuid and tenant_id = $2::uuid
		  and (enabled is distinct from false or max_consumers is distinct from 0)`, accountID, tenantID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update platform_tenant_hostname_policies set
		allowed_suffixes = ARRAY[]::text[], max_hostnames = 0, updated_at = $3
		where account_id = $1::uuid and tenant_id = $2::uuid
		  and (allowed_suffixes is distinct from ARRAY[]::text[] or max_hostnames is distinct from 0)`, accountID, tenantID, now); err != nil {
		return err
	}
	tag, err = tx.Exec(ctx, `delete from tenant_hostnames h using tenant_surfaces s
		where h.surface_id = s.id and s.account_id = $1::uuid and s.platform_tenant_id = $2::uuid
		  and s.status <> 'deleted' and h.platform_tenant_managed`, accountID, tenantID)
	if err != nil {
		return err
	}
	if int(tag.RowsAffected()) != actions.RemoveManagedHostnames {
		return ErrPlatformTenantPlanStale
	}
	tag, err = tx.Exec(ctx, `update api_consumers set platform_tenant_id = null, updated_at = $3
		where account_id = $1::uuid and platform_tenant_id = $2::uuid and platform_tenant_managed`, accountID, tenantID, now)
	if err != nil {
		return err
	}
	if int(tag.RowsAffected()) != actions.DetachManagedConsumers {
		return ErrPlatformTenantPlanStale
	}
	tag, err = tx.Exec(ctx, `update tenant_surfaces set platform_tenant_id = null, updated_at = $3
		where account_id = $1::uuid and platform_tenant_id = $2::uuid and platform_tenant_managed and status <> 'deleted'`, accountID, tenantID, now)
	if err != nil {
		return err
	}
	if int(tag.RowsAffected()) != actions.DetachManagedSurfaces {
		return ErrPlatformTenantPlanStale
	}
	return nil
}

func (s *PgStore) ListPlatformTenantOffboardingReceipts(ctx context.Context, accountID, tenantID string, pageSize int, pageToken string) ([]api.PlatformTenantOffboardingReceiptSummary, string, error) {
	if _, err := uuid.Parse(accountID); err != nil {
		return nil, "", ErrNotFound
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return nil, "", ErrNotFound
	}
	if pageSize < 1 || pageSize > 100 {
		return nil, "", ErrInvalidArgument
	}
	if pageToken != "" {
		_, id, ok := decodePageToken(pageToken)
		if _, err := uuid.Parse(id); !ok || err != nil {
			return nil, "", ErrInvalidArgument
		}
	}
	var exists string
	if err := s.pool.QueryRow(ctx, `select id::text from platform_tenants where account_id = $1::uuid and id = $2::uuid`, accountID, tenantID).Scan(&exists); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", ErrNotFound
		}
		return nil, "", err
	}
	query := `select receipt_id::text, plan_hash, applied_at from platform_tenant_offboarding_receipts
		where account_id = $1::uuid and tenant_id = $2::uuid`
	args := []any{accountID, tenantID}
	if pageToken != "" {
		timestamp, id, _ := decodePageToken(pageToken)
		query += ` and (applied_at, receipt_id) < ($3, $4::uuid)`
		args = append(args, timestamp, id)
	}
	args = append(args, pageSize+1)
	query += ` order by applied_at desc, receipt_id desc limit $` + fmt.Sprint(len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := make([]api.PlatformTenantOffboardingReceiptSummary, 0)
	for rows.Next() {
		var receipt api.PlatformTenantOffboardingReceiptSummary
		if err := rows.Scan(&receipt.ReceiptID, &receipt.PlanHash, &receipt.AppliedAt); err != nil {
			return nil, "", err
		}
		out = append(out, receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	var nextToken string
	if len(out) > pageSize {
		last := out[pageSize-1]
		nextToken = encodePageToken(last.AppliedAt, last.ReceiptID)
		out = out[:pageSize]
	}
	return out, nextToken, nil
}

func (s *PgStore) GetPlatformTenantOffboardingReceipt(ctx context.Context, accountID, tenantID, receiptID string) (api.PlatformTenantOffboardingReceiptResponse, error) {
	if _, err := uuid.Parse(accountID); err != nil {
		return api.PlatformTenantOffboardingReceiptResponse{}, ErrNotFound
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return api.PlatformTenantOffboardingReceiptResponse{}, ErrNotFound
	}
	if _, err := uuid.Parse(receiptID); err != nil {
		return api.PlatformTenantOffboardingReceiptResponse{}, ErrNotFound
	}
	var out api.PlatformTenantOffboardingReceiptResponse
	var actionsJSON []byte
	err := s.pool.QueryRow(ctx, `select r.tenant_id::text, r.receipt_id::text, r.plan_hash, r.applied_at, r.actions
		from platform_tenant_offboarding_receipts r
		join platform_tenants t on t.id = r.tenant_id and t.account_id = r.account_id
		where r.account_id = $1::uuid and r.tenant_id = $2::uuid and r.receipt_id = $3::uuid`,
		accountID, tenantID, receiptID).Scan(&out.TenantID, &out.ReceiptID, &out.PlanHash, &out.AppliedAt, &actionsJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.PlatformTenantOffboardingReceiptResponse{}, ErrNotFound
		}
		return api.PlatformTenantOffboardingReceiptResponse{}, err
	}
	if err := json.Unmarshal(actionsJSON, &out.Actions); err != nil {
		return api.PlatformTenantOffboardingReceiptResponse{}, err
	}
	return out, nil
}
