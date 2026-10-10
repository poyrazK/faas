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

func (s *PgStore) SummarizeAccountWorkflowPerformance(ctx context.Context, account string, opts api.OperationWorkflowPerformanceOptions) (api.OperationWorkflowPerformanceSummary, error) {
	return s.summarizeWorkflowPerformance(ctx, account, opts.TenantID, opts, true)
}
func (s *PgStore) SummarizePlatformTenantWorkflowPerformance(ctx context.Context, account, tenant string, opts api.OperationWorkflowPerformanceOptions) (api.OperationWorkflowPerformanceSummary, error) {
	return s.summarizeWorkflowPerformance(ctx, account, tenant, opts, false)
}
func (s *PgStore) summarizeWorkflowPerformance(ctx context.Context, account, tenant string, opts api.OperationWorkflowPerformanceOptions, operator bool) (api.OperationWorkflowPerformanceSummary, error) {
	opts, at, err := prepareWorkflowPerformance(account, tenant, opts, operator)
	if err != nil {
		return api.OperationWorkflowPerformanceSummary{}, err
	}
	instances, err := s.readWorkflowPerformance(ctx, account, tenant, opts, operator, at)
	if err != nil {
		return api.OperationWorkflowPerformanceSummary{}, err
	}
	out := workflowPerformanceSummary(instances, opts, at)
	out.CohortToken = workflowPerformanceToken(account, tenant, opts, operator, instances, at)
	return out, nil
}
func (s *PgStore) readWorkflowPerformance(ctx context.Context, account, tenant string, opts api.OperationWorkflowPerformanceOptions, operator bool, at time.Time) ([]workflowPerformanceInstance, error) {
	if operator {
		tenant = opts.TenantID
	} else {
		tenant = uuid.MustParse(tenant).String()
	}
	accountID, err := operationUUID(account)
	if err != nil {
		return nil, err
	}
	appID, err := operationUUID(opts.AppID)
	if err != nil {
		return nil, err
	}
	raw, err := sqlc.New().SummarizeCustomerOperationWorkflowPerformance(ctx, s.pool, sqlc.SummarizeCustomerOperationWorkflowPerformanceParams{
		AccountID: accountID, AppID: appID, TenantID: tenant, Scope: opts.Scope, WorkflowName: opts.Workflow,
		EvaluatedAt: pgtype.Timestamptz{Time: at, Valid: true}, Now: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}, CohortLimit: int32(api.OperationWorkflowPerformanceCohortMax), WindowLimit: int32(api.OperationWorkflowBottleneckHistoryMax + 1),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	instances := make([]workflowPerformanceInstance, 0, len(raw))
	for _, row := range raw {
		var instance workflowPerformanceInstance
		if err := json.Unmarshal(row, &instance); err != nil {
			return nil, err
		}
		instance.Current.Stale = operationWorkflowStateIsStale(at, instance.Current)
		evaluateOperationWorkflowDeadline(at, &instance.Current)
		instance.Current.SLA = workflowStateSLA(instance.Current, instance.Observations, instance.CurrentSLABudgetSeconds, instance.CurrentSLAWarningPercent, at)
		instances = append(instances, instance)
	}
	return instances, nil
}
