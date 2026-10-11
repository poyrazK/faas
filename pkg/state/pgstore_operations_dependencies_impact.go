package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) projectDependencyImpact(ctx context.Context, page *api.OperationMilestonesResponse, account, tenant string, opts api.OperationMilestoneListOptions, operator bool, now time.Time) error {
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
	reference := api.OperationWorkflowDependency{SubjectType: opts.SubjectType, SubjectID: opts.SubjectID, Workflow: instance.Workflow, InstanceID: instance.InstanceID}
	selected, _ := json.Marshal([]api.OperationWorkflowDependency{reference})
	known, terminal, outcome := instance.State != nil, false, ""
	if known {
		terminal, outcome = instance.State.Terminal, instance.State.OutcomeCode
	}
	at := pgtype.Timestamptz{Time: now, Valid: true}
	raw, err := sqlc.New().GetCustomerOperationWorkflowDependencyImpact(ctx, s.pool, sqlc.GetCustomerOperationWorkflowDependencyImpactParams{AccountID: accountID, AppID: appID, TenantID: tenantID, Scope: opts.Scope, SelectedReference: selected, SelectedKnown: known, SelectedTerminal: terminal, SelectedOutcomeCode: outcome, Now: at, EvaluatedAt: at, PageLimit: operationDependencyImpactLimit})
	if err != nil {
		return mapErr(err)
	}
	var impact api.OperationWorkflowDependencyImpact
	if err = json.Unmarshal(raw, &impact); err != nil {
		return err
	}
	for i := range impact.Items {
		impact.Items[i].State.PlatformTenantID = ""
		if operator {
			impact.Items[i].State.PlatformTenantID = tenant
		}
	}
	instance.DependencyImpact = &impact
	return nil
}
