package state

import (
	"context"
	"time"

	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) readReadinessQueueCandidates(ctx context.Context, account, tenant string, opts api.OperationWorkflowAttentionOptions, operator bool, at time.Time) ([]workflowPerformanceInstance, error) {
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
	raw, err := sqlc.New().ListCustomerOperationWorkflowReadinessCandidates(ctx, s.pool, sqlc.ListCustomerOperationWorkflowReadinessCandidatesParams{AccountID: accountID, AppID: appID, TenantID: tenant, Scope: opts.Scope, WorkflowName: opts.Workflow, EvaluatedAt: pgtype.Timestamptz{Time: at, Valid: true}, Now: pgtype.Timestamptz{Time: at, Valid: true}, CohortLimit: int32(api.OperationWorkflowPerformanceCohortMax)})
	if err != nil {
		return nil, mapErr(err)
	}
	instances := make([]workflowPerformanceInstance, 0, len(raw))
	for _, row := range raw {
		var instance workflowPerformanceInstance
		if err := json.Unmarshal(row, &instance); err != nil {
			return nil, err
		}
		instances = append(instances, instance)
	}
	return instances, nil
}
