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
		SlaHistoryLimit: int32(api.OperationWorkflowBottleneckHistoryMax + 1), AccountID: accountID, AppID: appID, Scope: opts.Scope, TenantID: tenant, WorkflowName: opts.Workflow, Reason: opts.Reason, TargetOperation: opts.TargetOperation, BlockerCode: opts.BlockerCode, Priority: opts.Priority, Owner: opts.Owner, Unassigned: opts.Unassigned, DependencyStatus: opts.DependencyStatus, RequiredOutcomeCode: opts.RequiredOutcomeCode,
		EvaluatedAt: pgtype.Timestamptz{Time: cursor.EvaluatedAt, Valid: true}, Now: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		AfterUpdatedAt: pgtype.Timestamptz{Time: after, Valid: true}, SortBy: opts.Sort, AfterDeadline: cursor.DeadlineAt, AfterKey: cursor.Key, PageLimit: int32(opts.Limit + 1),
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
		all := entry.ResolutionVerifications
		entry.ResolutionVerificationCount = int64(len(all))
		entry.ResolutionVerifications, entry.AwaitingVerificationCount = verificationPreview(all, opts)
		entry.Reasons = operationAttentionReasons(entry.State)
		if entry.AwaitingVerificationCount > 0 {
			entry.Reasons = append(entry.Reasons, "awaiting_verification")
		}
		entry.Reasons = append(entry.Reasons, workflowFollowUpReasons(entry.State, cursor.EvaluatedAt)...)
		if len(entry.Escalations) > 0 {
			entry.Reasons = append(entry.Reasons, "escalated")
		}
		if len(entry.DependencyAttention) > 0 {
			entry.Reasons = append(entry.Reasons, "dependency")
		}
		rows = append(rows, operationAttentionRow{Entry: entry, Key: record.CursorKey})
	}
	return operationAttentionPage(rows, opts.Limit, cursor), nil
}
