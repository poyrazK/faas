package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) projectDependencyTrace(ctx context.Context, page *api.OperationMilestonesResponse, account, tenant string, opts api.OperationMilestoneListOptions, operator bool, now time.Time) error {
	instance := page.WorkflowInstance
	tenant = operationDependencyImpactTenant(instance, tenant, opts, operator)
	if tenant == "" {
		return nil
	}
	accountID, _ := operationUUID(account)
	appID, _ := operationUUID(opts.AppID)
	tenantID, err := operationUUID(tenant)
	if err != nil {
		return err
	}
	at := pgtype.Timestamptz{Time: now, Valid: true}
	lookup := func(dependencies []api.OperationWorkflowDependency) (map[[4]string]api.OperationWorkflowState, error) {
		raw, err := json.Marshal(dependencies)
		if err != nil {
			return nil, err
		}
		records, err := sqlc.New().GetCustomerOperationRelatedWorkflowStates(ctx, s.pool, sqlc.GetCustomerOperationRelatedWorkflowStatesParams{AccountID: accountID, AppID: appID, TenantID: tenantID, Scope: opts.Scope, Dependencies: raw, Now: at, EvaluatedAt: at})
		if err != nil {
			return nil, mapErr(err)
		}
		states := map[[4]string]api.OperationWorkflowState{}
		for _, record := range records {
			var entry api.OperationWorkflowAttentionEntry
			var dependency api.OperationWorkflowDependency
			if err = json.Unmarshal(record.Entry, &entry); err != nil {
				return nil, err
			}
			if err = json.Unmarshal(record.Dependency, &dependency); err != nil {
				return nil, err
			}
			state := entry.State
			state.PlatformTenantID = ""
			if operator {
				state.PlatformTenantID = tenant
			}
			states[dependencyKey(dependency)] = state
		}
		return states, nil
	}
	root := api.OperationWorkflowDependency{SubjectType: opts.SubjectType, SubjectID: opts.SubjectID, Workflow: instance.Workflow, InstanceID: instance.InstanceID}
	trace, err := traceOperationWorkflowDependencies(root, instance.State, lookup)
	if err != nil {
		return err
	}
	instance.DependencyTrace = trace
	return nil
}
