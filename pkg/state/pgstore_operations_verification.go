package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) projectResolutionVerifications(ctx context.Context, page *api.OperationMilestonesResponse, account, tenant string, opts api.OperationMilestoneListOptions, now time.Time) error {
	if page.WorkflowInstance == nil || page.WorkflowInstance.State == nil || opts.SubjectType == "" {
		return nil
	}
	if tenant == "" {
		tenant = page.WorkflowInstance.State.PlatformTenantID
	}
	accountID, err := operationUUID(account)
	if err != nil {
		return err
	}
	appID, err := operationUUID(opts.AppID)
	if err != nil {
		return err
	}
	tenantID, err := operationUUID(tenant)
	if err != nil {
		return err
	}
	raw, err := sqlc.New().GetCustomerOperationResolutionVerifications(ctx, s.pool, sqlc.GetCustomerOperationResolutionVerificationsParams{
		AccountID: accountID, AppID: appID, TenantID: tenantID, Scope: opts.Scope, SubjectType: opts.SubjectType, SubjectID: opts.SubjectID, WorkflowName: opts.Workflow, InstanceID: opts.WorkflowInstanceID,
		EvaluatedAt: pgtype.Timestamptz{Time: now, Valid: true}, Now: pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil {
		return mapErr(err)
	}
	var all []api.OperationWorkflowResolutionVerification
	if err = json.Unmarshal(raw, &all); err != nil {
		return err
	}
	page.WorkflowInstance.ResolutionVerifications, page.WorkflowInstance.AwaitingVerificationCount = verificationPreview(all, api.OperationWorkflowAttentionOptions{})
	page.WorkflowInstance.ResolutionVerificationCount = int64(len(all))
	attachResolutionHistoryVerifications(page.WorkflowStateHistory, all)
	attachResolutionHistoryVerifications(page.WorkflowInstance.Transitions, all)
	noteResolutionVerificationAttention(page.WorkflowInstance)
	if !opts.ReadinessOnly {
		rows, err := sqlc.New().GetCustomerOperationWorkflowBottleneckHistory(ctx, s.pool, sqlc.GetCustomerOperationWorkflowBottleneckHistoryParams{
			AccountID: accountID, AppID: appID, TenantID: tenantID, Scope: opts.Scope, SubjectType: opts.SubjectType, SubjectID: opts.SubjectID, WorkflowName: opts.Workflow, InstanceID: opts.WorkflowInstanceID,
			EvaluatedAt: pgtype.Timestamptz{Time: now, Valid: true}, Now: pgtype.Timestamptz{Time: now, Valid: true}, WindowLimit: int32(api.OperationWorkflowBottleneckHistoryMax + 1),
		})
		if err != nil {
			return mapErr(err)
		}
		observations := make([]workflowBottleneckObservation, 0, len(rows))
		for _, row := range rows {
			var observation workflowBottleneckObservation
			if err := json.Unmarshal(row, &observation); err != nil {
				return err
			}
			observations = append(observations, observation)
		}
		operationID, err := operationUUID(page.WorkflowInstance.State.OperationID)
		if err != nil {
			return err
		}
		budget, err := sqlc.New().GetCustomerOperationWorkflowSLABudget(ctx, s.pool, sqlc.GetCustomerOperationWorkflowSLABudgetParams{AccountID: accountID, AppID: appID, TenantID: tenantID, OperationID: operationID, Scope: opts.Scope, WorkflowName: opts.Workflow, StateName: page.WorkflowInstance.State.State, ContractVersion: int32(effectiveWorkflowContractVersion(page.WorkflowInstance.State.ContractVersion)), Now: pgtype.Timestamptz{Time: now, Valid: true}})
		if err != nil {
			return mapErr(err)
		}
		page.WorkflowInstance.State.SLA = workflowStateSLA(*page.WorkflowInstance.State, observations, budget.Budget, budget.WarningPercent, now)
		noteWorkflowSLAAttention(page.WorkflowInstance)
		page.WorkflowInstance.Bottlenecks = workflowBottlenecks(observations, page.WorkflowInstance.State, all, now)
	}
	return nil
}
