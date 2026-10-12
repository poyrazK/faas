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

func (m *MemStore) workflowProgressDefinitions(ctx context.Context, account, tenant string, opts api.OperationWorkflowPerformanceOptions, operator bool, ids []string) (map[string][]api.OperationWorkflowSpec, error) {
	out := map[string][]api.OperationWorkflowSpec{}
	if operator {
		tenant = opts.TenantID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	now := time.Now().UTC()
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		op, found := data.operations[id]
		if !found || !sameOperationHistoryIdentity(op.AccountID, account) || !sameOperationHistoryIdentity(op.AppID, opts.AppID) || op.Scope != opts.Scope || tenant != "" && !sameOperationHistoryIdentity(op.PlatformTenantID, tenant) || !operationRetained(op, now) {
			continue
		}
		definition, found := data.definitions[op.DefinitionID]
		if found {
			out[id] = definition.Spec.WorkflowSteps
		}
	}
	return out, nil
}
func (s *PgStore) workflowProgressDefinitions(ctx context.Context, account, tenant string, opts api.OperationWorkflowPerformanceOptions, operator bool, ids []string) (map[string][]api.OperationWorkflowSpec, error) {
	out := map[string][]api.OperationWorkflowSpec{}
	if len(ids) == 0 {
		return out, nil
	}
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
	operationIDs := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		value, err := operationUUID(id)
		if err != nil {
			return nil, err
		}
		operationIDs = append(operationIDs, value)
	}
	rows, err := sqlc.New().ListCustomerOperationWorkflowProgressDefinitions(ctx, s.pool, sqlc.ListCustomerOperationWorkflowProgressDefinitionsParams{WorkflowName: opts.Workflow, AccountID: accountID, AppID: appID, Scope: opts.Scope, OperationIds: operationIDs, TenantID: tenant, Now: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}})
	if err != nil {
		return nil, mapErr(err)
	}
	for _, row := range rows {
		var steps []api.OperationWorkflowSpec
		if err := json.Unmarshal(row.WorkflowSteps, &steps); err != nil {
			return nil, err
		}
		out[uuid.UUID(row.OperationID.Bytes).String()] = steps
	}
	return out, nil
}
