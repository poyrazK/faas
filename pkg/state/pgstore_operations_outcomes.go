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

func (s *PgStore) ListAccountWorkflowOutcomes(ctx context.Context, account string, opts api.OperationWorkflowOutcomeOptions) (api.OperationWorkflowOutcomesResponse, error) {
	return s.listWorkflowOutcomes(ctx, account, opts.TenantID, opts, true)
}
func (s *PgStore) ListPlatformTenantWorkflowOutcomes(ctx context.Context, account, tenant string, opts api.OperationWorkflowOutcomeOptions) (api.OperationWorkflowOutcomesResponse, error) {
	return s.listWorkflowOutcomes(ctx, account, tenant, opts, false)
}
func (s *PgStore) listWorkflowOutcomes(ctx context.Context, account, tenant string, opts api.OperationWorkflowOutcomeOptions, operator bool) (api.OperationWorkflowOutcomesResponse, error) {
	opts, c, err := prepareOperationOutcomes(account, tenant, opts, operator)
	if err != nil {
		return api.OperationWorkflowOutcomesResponse{}, err
	}
	if operator {
		tenant = opts.TenantID
	} else {
		tenant = uuid.MustParse(tenant).String()
	}
	accountID, _ := operationUUID(account)
	appID, _ := operationUUID(opts.AppID)
	after := c.UpdatedAt
	if after.IsZero() {
		after = c.EvaluatedAt
	}
	records, err := sqlc.New().ListCustomerOperationWorkflowOutcomes(ctx, s.pool, sqlc.ListCustomerOperationWorkflowOutcomesParams{AccountID: accountID, AppID: appID, Scope: opts.Scope, TenantID: tenant, WorkflowName: opts.Workflow, OutcomeCode: opts.Code, EvaluatedAt: pgtype.Timestamptz{Time: c.EvaluatedAt, Valid: true}, Now: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}, AfterUpdatedAt: pgtype.Timestamptz{Time: after, Valid: true}, AfterKey: c.Key, PageLimit: int32(opts.Limit + 1)})
	if err != nil {
		return api.OperationWorkflowOutcomesResponse{}, mapErr(err)
	}
	rows := make([]operationAttentionRow, 0, len(records))
	for _, record := range records {
		var entry api.OperationWorkflowAttentionEntry
		if err = json.Unmarshal(record.Entry, &entry); err != nil {
			return api.OperationWorkflowOutcomesResponse{}, err
		}
		if !operator {
			entry.PlatformTenantID = ""
			entry.State.PlatformTenantID = ""
		}
		rows = append(rows, operationAttentionRow{Entry: entry, Key: record.CursorKey})
	}
	return operationOutcomePage(rows, opts.Limit, c), nil
}
func (s *PgStore) SummarizeAccountWorkflowOutcomes(ctx context.Context, account string, opts api.OperationWorkflowOutcomeSummaryOptions) (api.OperationWorkflowOutcomeSummary, error) {
	return s.summarizeWorkflowOutcomes(ctx, account, opts.TenantID, opts, true)
}
func (s *PgStore) SummarizePlatformTenantWorkflowOutcomes(ctx context.Context, account, tenant string, opts api.OperationWorkflowOutcomeSummaryOptions) (api.OperationWorkflowOutcomeSummary, error) {
	return s.summarizeWorkflowOutcomes(ctx, account, tenant, opts, false)
}
func (s *PgStore) summarizeWorkflowOutcomes(ctx context.Context, account, tenant string, opts api.OperationWorkflowOutcomeSummaryOptions, operator bool) (api.OperationWorkflowOutcomeSummary, error) {
	opts, c, err := prepareOperationOutcomeSummary(account, tenant, opts, operator)
	if err != nil {
		return api.OperationWorkflowOutcomeSummary{}, err
	}
	if operator {
		tenant = opts.TenantID
	} else {
		tenant = uuid.MustParse(tenant).String()
	}
	accountID, _ := operationUUID(account)
	appID, _ := operationUUID(opts.AppID)
	raw, err := sqlc.New().SummarizeCustomerOperationWorkflowOutcomes(ctx, s.pool, sqlc.SummarizeCustomerOperationWorkflowOutcomesParams{AccountID: accountID, AppID: appID, Scope: opts.Scope, TenantID: tenant, WorkflowName: opts.Workflow, OutcomeCode: opts.Code, GroupBy: opts.GroupBy, AfterGroup: c.After, PageLimit: int32(opts.Limit + 1), EvaluatedAt: pgtype.Timestamptz{Time: c.EvaluatedAt, Valid: true}, Now: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}})
	if err != nil {
		return api.OperationWorkflowOutcomeSummary{}, mapErr(err)
	}
	var out api.OperationWorkflowOutcomeSummary
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	return finishOperationOutcomeSummary(out, opts.Limit, c), nil
}
