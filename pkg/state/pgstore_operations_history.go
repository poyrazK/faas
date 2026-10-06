package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ListPlatformTenantOperations(ctx context.Context, account, tenant string, opts api.OperationListOptions) (api.OperationListResponse, error) {
	opts, cursor, err := prepareOperationHistory(account, tenant, opts)
	if err != nil {
		return api.OperationListResponse{}, err
	}
	accountID, _ := operationUUID(account)
	tenantID, _ := operationUUID(tenant)
	appID, _ := operationUUID(opts.AppID)
	params := sqlc.ListPlatformTenantCustomerOperationsParams{AccountID: accountID, TenantID: tenantID, AppID: appID,
		Scope: opts.Scope, OperationName: opts.Name, OperationState: string(opts.State),
		Now: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}, PageLimit: int32(opts.Limit + 1)}
	if cursor.ID != "" {
		params.BeforeID, _ = operationUUID(cursor.ID)
		params.BeforeCreatedAt = pgtype.Timestamptz{Time: cursor.CreatedAt, Valid: true}
	}
	raw, err := sqlc.New().ListPlatformTenantCustomerOperations(ctx, s.pool, params)
	if err != nil {
		return api.OperationListResponse{}, fmt.Errorf("state: list customer operations: %w", mapErr(err))
	}
	rows := make([]api.OperationSummary, 0, len(raw))
	for _, data := range raw {
		var row api.OperationSummary
		if err := json.Unmarshal(data, &row); err != nil {
			return api.OperationListResponse{}, fmt.Errorf("state: decode operation summary: %w", err)
		}
		rows = append(rows, row)
	}
	return operationHistoryPage(rows, opts.Limit, cursor), nil
}
