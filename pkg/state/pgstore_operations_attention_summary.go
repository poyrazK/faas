package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) SummarizeAccountWorkflowAttention(ctx context.Context, account string, opts api.OperationWorkflowAttentionSummaryOptions) (api.OperationWorkflowAttentionSummary, error) {
	return s.summarizeWorkflowAttention(ctx, account, opts.TenantID, opts, true)
}
func (s *PgStore) SummarizePlatformTenantWorkflowAttention(ctx context.Context, account, tenant string, opts api.OperationWorkflowAttentionSummaryOptions) (api.OperationWorkflowAttentionSummary, error) {
	return s.summarizeWorkflowAttention(ctx, account, tenant, opts, false)
}
func (s *PgStore) summarizeWorkflowAttention(ctx context.Context, account, tenant string, opts api.OperationWorkflowAttentionSummaryOptions, operator bool) (api.OperationWorkflowAttentionSummary, error) {
	opts, c, err := prepareOperationAttentionSummary(account, tenant, opts, operator)
	if err != nil {
		return api.OperationWorkflowAttentionSummary{}, err
	}
	if operator {
		tenant = opts.TenantID
	} else {
		tenant = uuid.MustParse(tenant).String()
	}
	accountID, _ := operationUUID(account)
	appID, _ := operationUUID(opts.AppID)
	raw, err := sqlc.New().SummarizeCustomerOperationWorkflowAttention(ctx, s.pool, sqlc.SummarizeCustomerOperationWorkflowAttentionParams{
		AccountID: accountID, AppID: appID, Scope: opts.Scope, TenantID: tenant, WorkflowName: opts.Workflow, BlockerCode: opts.BlockerCode, DependencyStatus: opts.DependencyStatus, RequiredOutcomeCode: opts.RequiredOutcomeCode, TargetOperation: opts.TargetOperation, Reason: opts.Reason,
		GroupBy: opts.GroupBy, AfterGroup: c.After, PageLimit: int32(opts.Limit + 1), EvaluatedAt: pgtype.Timestamptz{Time: c.EvaluatedAt, Valid: true}, Now: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	})
	if err != nil {
		return api.OperationWorkflowAttentionSummary{}, mapErr(err)
	}
	var out api.OperationWorkflowAttentionSummary
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	return finishOperationAttentionSummary(out, opts.Limit, c), nil
}
