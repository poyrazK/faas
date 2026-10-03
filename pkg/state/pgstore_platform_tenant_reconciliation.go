package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
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
	changes := platformTenantAppliedReconciliationChanges(snapshot.changes, result)
	changesJSON, err := json.Marshal(changes)
	if err != nil {
		return api.PlatformTenantReconciliationApplyResponse{}, err
	}
	response := api.PlatformTenantReconciliationApplyResponse{TenantID: result.Tenant.ID, ReceiptID: uuid.NewString(),
		PlanHash: snapshot.planHash, Applied: true, Changes: changes}
	if err := tx.QueryRow(ctx, `insert into platform_tenant_reconciliation_receipts
		(account_id, tenant_id, receipt_id, plan_hash, changes)
		values ($1::uuid, $2::uuid, $3::uuid, $4, $5::jsonb)
		returning applied_at`, in.AccountID, in.TenantID, response.ReceiptID, response.PlanHash, changesJSON).Scan(&response.AppliedAt); err != nil {
		return api.PlatformTenantReconciliationApplyResponse{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.PlatformTenantReconciliationApplyResponse{}, err
	}
	return response, nil
}

func (s *PgStore) ListPlatformTenantReconciliationReceipts(ctx context.Context, accountID, tenantID string, pageSize int, pageToken string) ([]api.PlatformTenantReconciliationReceiptSummary, string, error) {
	if accountID == "" || tenantID == "" {
		return nil, "", ErrNotFound
	}
	if pageSize < 1 || pageSize > 100 {
		return nil, "", ErrInvalidArgument
	}
	query := `select r.receipt_id::text, r.plan_hash, r.applied_at, jsonb_array_length(r.changes)
		from platform_tenant_reconciliation_receipts r
		join platform_tenants t on t.id = r.tenant_id and t.account_id = r.account_id
		where r.account_id = $1::uuid and r.tenant_id = $2::uuid`
	args := []any{accountID, tenantID}
	if pageToken != "" {
		ts, id, ok := decodePageToken(pageToken)
		if _, err := uuid.Parse(id); !ok || err != nil {
			return nil, "", ErrInvalidArgument
		}
		query += ` and (r.applied_at, r.receipt_id) < ($3, $4::uuid)`
		args = append(args, ts, id)
	}
	args = append(args, pageSize+1)
	query += ` order by r.applied_at desc, r.receipt_id desc limit $` + fmt.Sprint(len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := make([]api.PlatformTenantReconciliationReceiptSummary, 0)
	for rows.Next() {
		var receipt api.PlatformTenantReconciliationReceiptSummary
		if err := rows.Scan(&receipt.ReceiptID, &receipt.PlanHash, &receipt.AppliedAt, &receipt.ChangeCount); err != nil {
			return nil, "", err
		}
		out = append(out, receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if len(out) == 0 {
		var found string
		if err := s.pool.QueryRow(ctx, `select id::text from platform_tenants where account_id = $1::uuid and id = $2::uuid`, accountID, tenantID).Scan(&found); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, "", ErrNotFound
			}
			return nil, "", err
		}
	}
	var nextToken string
	if len(out) > pageSize {
		last := out[pageSize-1]
		nextToken = encodePageToken(last.AppliedAt, last.ReceiptID)
		out = out[:pageSize]
	}
	return out, nextToken, nil
}

func (s *PgStore) GetPlatformTenantReconciliationReceipt(ctx context.Context, accountID, tenantID, receiptID string) (api.PlatformTenantReconciliationReceiptResponse, error) {
	if accountID == "" || tenantID == "" || receiptID == "" {
		return api.PlatformTenantReconciliationReceiptResponse{}, ErrNotFound
	}
	if _, err := uuid.Parse(receiptID); err != nil {
		return api.PlatformTenantReconciliationReceiptResponse{}, ErrNotFound
	}
	var out api.PlatformTenantReconciliationReceiptResponse
	var changesJSON []byte
	err := s.pool.QueryRow(ctx, `select r.tenant_id::text, r.receipt_id::text, r.plan_hash, r.applied_at, r.changes
		from platform_tenant_reconciliation_receipts r
		join platform_tenants t on t.id = r.tenant_id and t.account_id = r.account_id
		where r.account_id = $1::uuid and r.tenant_id = $2::uuid and r.receipt_id = $3::uuid`,
		accountID, tenantID, receiptID).Scan(&out.TenantID, &out.ReceiptID, &out.PlanHash, &out.AppliedAt, &changesJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.PlatformTenantReconciliationReceiptResponse{}, ErrNotFound
		}
		return api.PlatformTenantReconciliationReceiptResponse{}, err
	}
	if err := json.Unmarshal(changesJSON, &out.Changes); err != nil {
		return api.PlatformTenantReconciliationReceiptResponse{}, err
	}
	if out.Changes == nil {
		out.Changes = []api.PlatformTenantReconciliationPlanChange{}
	}
	return out, nil
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
