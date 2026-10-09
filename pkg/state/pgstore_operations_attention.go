package state

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

func (s *PgStore) ListAccountWorkflowAttention(ctx context.Context, account string, opts api.OperationWorkflowAttentionOptions) (api.OperationWorkflowAttentionResponse, error) {
	return s.listWorkflowAttention(ctx, account, opts.TenantID, opts, true)
}
func (s *PgStore) ListPlatformTenantWorkflowAttention(ctx context.Context, account, tenant string, opts api.OperationWorkflowAttentionOptions) (api.OperationWorkflowAttentionResponse, error) {
	return s.listWorkflowAttention(ctx, account, tenant, opts, false)
}
func (s *PgStore) listWorkflowAttention(ctx context.Context, account, tenant string, opts api.OperationWorkflowAttentionOptions, operator bool) (api.OperationWorkflowAttentionResponse, error) {
	opts, cursor, err := prepareOperationAttention(account, tenant, opts, operator)
	if err != nil {
		return api.OperationWorkflowAttentionResponse{}, err
	}
	if operator {
		tenant = opts.TenantID
	} else {
		tenant = uuid.MustParse(tenant).String()
	}
	accountID, _ := operationUUID(account)
	appID, _ := operationUUID(opts.AppID)
	after := cursor.UpdatedAt
	if after.IsZero() {
		after = cursor.EvaluatedAt
	}
	records, err := sqlc.New().ListCustomerOperationWorkflowAttention(ctx, s.pool, sqlc.ListCustomerOperationWorkflowAttentionParams{
		AccountID: accountID, AppID: appID, Scope: opts.Scope, TenantID: tenant, WorkflowName: opts.Workflow, Reason: opts.Reason, TargetOperation: opts.TargetOperation, BlockerCode: opts.BlockerCode, DependencyStatus: opts.DependencyStatus, RequiredOutcomeCode: opts.RequiredOutcomeCode,
		EvaluatedAt: pgtype.Timestamptz{Time: cursor.EvaluatedAt, Valid: true}, Now: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		AfterUpdatedAt: pgtype.Timestamptz{Time: after, Valid: true}, AfterKey: cursor.Key, PageLimit: int32(opts.Limit + 1),
	})
	if err != nil {
		return api.OperationWorkflowAttentionResponse{}, mapErr(err)
	}
	rows := make([]operationAttentionRow, 0, len(records))
	for _, record := range records {
		var entry api.OperationWorkflowAttentionEntry
		if err := json.Unmarshal(record.Entry, &entry); err != nil {
			return api.OperationWorkflowAttentionResponse{}, err
		}
		if !operator {
			entry.PlatformTenantID = ""
			entry.State.PlatformTenantID = ""
		}
		entry.Reasons = operationAttentionReasons(entry.State)
		if len(entry.DependencyAttention) > 0 {
			entry.Reasons = append(entry.Reasons, "dependency")
		}
		rows = append(rows, operationAttentionRow{Entry: entry, Key: record.CursorKey})
	}
	return operationAttentionPage(rows, opts.Limit, cursor), nil
}
