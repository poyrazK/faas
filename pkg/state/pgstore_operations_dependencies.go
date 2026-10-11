package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) projectRelatedWorkflows(ctx context.Context, page *api.OperationMilestonesResponse, account, tenant string, opts api.OperationMilestoneListOptions, operator bool, now time.Time) error {
	instance := page.WorkflowInstance
	if instance == nil || instance.State == nil || len(instance.State.DependsOn) == 0 {
		return nil
	}
	if operator {
		tenant = instance.State.PlatformTenantID
	}
	if tenant == "" {
		return nil
	}
	accountID, _ := operationUUID(account)
	appID, _ := operationUUID(opts.AppID)
	tenantID, err := operationUUID(tenant)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(instance.State.DependsOn)
	at := pgtype.Timestamptz{Time: now, Valid: true}
	records, err := sqlc.New().GetCustomerOperationRelatedWorkflowStates(ctx, s.pool, sqlc.GetCustomerOperationRelatedWorkflowStatesParams{AccountID: accountID, AppID: appID, TenantID: tenantID, Scope: opts.Scope, Dependencies: raw, Now: at, EvaluatedAt: at})
	if err != nil {
		return mapErr(err)
	}
	states := map[[4]string]api.OperationWorkflowState{}
	for _, record := range records {
		var entry api.OperationWorkflowAttentionEntry
		var dependency api.OperationWorkflowDependency
		if err = json.Unmarshal(record.Entry, &entry); err != nil {
			return err
		}
		if err = json.Unmarshal(record.Dependency, &dependency); err != nil {
			return err
		}
		state := entry.State
		if operator {
			state.PlatformTenantID = tenant
		} else {
			state.PlatformTenantID = ""
		}
		states[dependencyKey(dependency)] = state
	}
	projectRelatedWorkflowInstances(instance, states)
	return nil
}
