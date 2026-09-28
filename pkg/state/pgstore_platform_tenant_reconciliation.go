package state

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
)

func (s *PgStore) PlanPlatformTenantReconciliation(ctx context.Context, in PlatformTenantReconciliationParams) (api.PlatformTenantReconciliationPlanResponse, error) {
	if err := validatePlatformTenantReconciliation(in); err != nil {
		return api.PlatformTenantReconciliationPlanResponse{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.PlatformTenantReconciliationPlanResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockPlatformTenantAccount(ctx, tx, in.AccountID); err != nil {
		return api.PlatformTenantReconciliationPlanResponse{}, err
	}
	snapshot, err := planPlatformTenantReconciliationTx(ctx, tx, in)
	if err != nil {
		return api.PlatformTenantReconciliationPlanResponse{}, err
	}
	return api.PlatformTenantReconciliationPlanResponse{TenantID: in.TenantID,
		PlanHash: snapshot.planHash, Changes: snapshot.changes}, nil
}

func (s *PgStore) ApplyPlatformTenantReconciliation(ctx context.Context, in PlatformTenantReconciliationParams, expectedPlanHash string) (api.PlatformTenantReconciliationApplyResponse, error) {
	if !validPlatformTenantPlanHash(expectedPlanHash) {
		return api.PlatformTenantReconciliationApplyResponse{}, ErrInvalidArgument
	}
	if err := validatePlatformTenantReconciliation(in); err != nil {
		return api.PlatformTenantReconciliationApplyResponse{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.PlatformTenantReconciliationApplyResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockPlatformTenantAccount(ctx, tx, in.AccountID); err != nil {
		return api.PlatformTenantReconciliationApplyResponse{}, err
	}
	snapshot, err := planPlatformTenantReconciliationTx(ctx, tx, in)
	if err != nil {
		return api.PlatformTenantReconciliationApplyResponse{}, err
	}
	if !platformTenantPlanHashMatches(expectedPlanHash, snapshot.planHash) {
		return api.PlatformTenantReconciliationApplyResponse{}, ErrPlatformTenantPlanStale
	}
	applyInput := in.ApplyPlatformTenantParams
	applyInput.DryRun = false
	result := snapshot.planned
	if err := commitPlatformTenantApply(ctx, tx, applyInput, &result); err != nil {
		return api.PlatformTenantReconciliationApplyResponse{}, err
	}
	for _, change := range snapshot.changes {
		if change.Action != "remove_candidate" {
			continue
		}
		var tag pgconn.CommandTag
		switch change.ResourceType {
		case "consumer":
			tag, err = tx.Exec(ctx, `update api_consumers
				set platform_tenant_id = null, updated_at = now()
				where id = $1::uuid and account_id = $2::uuid and platform_tenant_id = $3::uuid
				  and platform_tenant_managed`, change.ID, in.AccountID, in.TenantID)
		case "surface":
			tag, err = tx.Exec(ctx, `update tenant_surfaces
				set platform_tenant_id = null, updated_at = now()
				where id = $1::uuid and account_id = $2::uuid and platform_tenant_id = $3::uuid
				  and platform_tenant_managed and status <> 'deleted'`, change.ID, in.AccountID, in.TenantID)
		case "hostname":
			tag, err = tx.Exec(ctx, `delete from tenant_hostnames h
				using tenant_surfaces s
				where h.id = $1::uuid and h.surface_id = $2::uuid and h.platform_tenant_managed
				  and s.id = h.surface_id and s.account_id = $3::uuid and s.platform_tenant_id = $4::uuid`,
				change.ID, change.SurfaceID, in.AccountID, in.TenantID)
		default:
			return api.PlatformTenantReconciliationApplyResponse{}, ErrInvalidArgument
		}
		if err != nil {
			return api.PlatformTenantReconciliationApplyResponse{}, err
		}
		if tag.RowsAffected() != 1 {
			return api.PlatformTenantReconciliationApplyResponse{}, ErrPlatformTenantPlanStale
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return api.PlatformTenantReconciliationApplyResponse{}, err
	}
	return api.PlatformTenantReconciliationApplyResponse{TenantID: result.Tenant.ID, PlanHash: snapshot.planHash,
		Applied: true, Changes: platformTenantAppliedReconciliationChanges(snapshot.changes, result)}, nil
}

func planPlatformTenantReconciliationTx(ctx context.Context, tx pgx.Tx, in PlatformTenantReconciliationParams) (platformTenantReconciliationSnapshot, error) {
	planInput := in.ApplyPlatformTenantParams
	planInput.DryRun = true
	planned, err := planPlatformTenantApply(ctx, tx, planInput)
	if err != nil {
		return platformTenantReconciliationSnapshot{}, err
	}
	if planned.Tenant.ID == "" || planned.Tenant.ID != in.TenantID {
		return platformTenantReconciliationSnapshot{}, ErrNotFound
	}
	consumers, err := listPlatformTenantConsumersTx(ctx, tx, in.AccountID, in.TenantID)
	if err != nil {
		return platformTenantReconciliationSnapshot{}, err
	}
	surfaces, err := listPlatformTenantSurfacesTx(ctx, tx, in.AccountID, in.TenantID)
	if err != nil {
		return platformTenantReconciliationSnapshot{}, err
	}
	hostnamesBySurface := make(map[string][]TenantHostname)
	for i := len(planInput.SurfaceIDs); i < len(planned.Surfaces); i++ {
		surfaceID := planned.Surfaces[i].Surface.ID
		if surfaceID == "" {
			continue
		}
		hostnames, err := listTenantHostnamesForSurfaceTx(ctx, tx, surfaceID)
		if err != nil {
			return platformTenantReconciliationSnapshot{}, err
		}
		hostnamesBySurface[surfaceID] = hostnames
	}
	changes := platformTenantReconciliationPlanChanges(planInput, planned, consumers, surfaces, hostnamesBySurface)
	return platformTenantReconciliationSnapshot{planned: planned, changes: changes,
		planHash: platformTenantReconciliationPlanHash(in.AccountID, in.TenantID, planInput, changes)}, nil
}

func listPlatformTenantConsumersTx(ctx context.Context, tx pgx.Tx, accountID, tenantID string) ([]APIConsumer, error) {
	rows, err := tx.Query(ctx, `select `+apiConsumerSelectCols+` from api_consumers
		where account_id = $1::uuid and platform_tenant_id = $2::uuid
		order by app_id, id for update`, accountID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]APIConsumer, 0)
	for rows.Next() {
		consumer, err := scanAPIConsumerRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, consumer)
	}
	return out, rows.Err()
}

func listPlatformTenantSurfacesTx(ctx context.Context, tx pgx.Tx, accountID, tenantID string) ([]TenantSurface, error) {
	rows, err := tx.Query(ctx, `select `+tenantSurfaceCols+` from tenant_surfaces
		where account_id = $1::uuid and platform_tenant_id = $2::uuid and status <> 'deleted'
		order by app_id, id for update`, accountID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TenantSurface, 0)
	for rows.Next() {
		surface, err := scanTenantSurface(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, surface)
	}
	return out, rows.Err()
}

func listTenantHostnamesForSurfaceTx(ctx context.Context, tx pgx.Tx, surfaceID string) ([]TenantHostname, error) {
	rows, err := tx.Query(ctx, `select `+tenantHostnameCols+` from tenant_hostnames
		where surface_id = $1::uuid order by lower(hostname), id for update`, surfaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTenantHostnames(rows)
}
