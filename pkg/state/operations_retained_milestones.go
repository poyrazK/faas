package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func retainedMilestoneRequirements(instance *api.OperationWorkflowInstanceSnapshot) ([]api.OperationWorkflowRetainedMilestoneRequirement, error) {
	if instance == nil {
		return nil, nil
	}
	seen := map[api.OperationWorkflowRetainedMilestoneRequirement]bool{}
	for _, edge := range instance.AllowedTransitions {
		for _, requirement := range edge.RequiredRetainedMilestones {
			seen[requirement] = true
			if len(seen) > api.OperationWorkflowRetainedMilestoneLookupMax {
				return nil, ErrInvalidArgument
			}
		}
	}
	out := make([]api.OperationWorkflowRetainedMilestoneRequirement, 0, len(seen))
	for requirement := range seen {
		out = append(out, requirement)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Operation != out[j].Operation {
			return out[i].Operation < out[j].Operation
		}
		return out[i].Milestone < out[j].Milestone
	})
	return out, nil
}

func retainedMilestoneTenant(instance *api.OperationWorkflowInstanceSnapshot, tenant string) string {
	if tenant == "" && instance != nil && instance.State != nil {
		return instance.State.PlatformTenantID
	}
	return tenant
}

func (m *MemStore) projectRetainedMilestonesLocked(page *api.OperationMilestonesResponse, account, tenant string, opts api.OperationMilestoneListOptions, now time.Time) error {
	instance := page.WorkflowInstance
	requirements, err := retainedMilestoneRequirements(instance)
	if err != nil || len(requirements) == 0 {
		return err
	}
	tenant = retainedMilestoneTenant(instance, tenant)
	// An account-wide view without a selected tenant cannot borrow another
	// tenant's evidence, even when business keys happen to match.
	if tenant == "" {
		return nil
	}
	wanted := map[api.OperationWorkflowRetainedMilestoneRequirement]bool{}
	for _, requirement := range requirements {
		wanted[requirement] = true
	}
	latest := map[api.OperationWorkflowRetainedMilestoneRequirement]api.OperationWorkflowInstanceMilestoneRef{}
	data := m.operationMemoryLocked()
	for operationID, receipts := range data.milestones {
		op := data.operations[operationID]
		if !sameOperationHistoryIdentity(op.AccountID, account) || !sameOperationHistoryIdentity(op.AppID, opts.AppID) || !sameOperationHistoryIdentity(op.PlatformTenantID, tenant) || op.Scope != opts.Scope || !operationRetained(op, now) || op.Subject == nil || op.Subject.Type != opts.SubjectType || op.Subject.ID != opts.SubjectID {
			continue
		}
		definition, ok := data.definitions[op.DefinitionID]
		if !ok || !sameOperationHistoryIdentity(definition.AccountID, account) || !sameOperationHistoryIdentity(definition.AppID, opts.AppID) || definition.Scope != opts.Scope || definition.Spec.HTTPTransactionVersion != api.OperationHTTPTransactionVersion {
			continue
		}
		for _, receipt := range receipts {
			fact := receipt.Milestone
			requirement := api.OperationWorkflowRetainedMilestoneRequirement{Operation: definition.Spec.Name, Milestone: fact.Name}
			if !wanted[requirement] || fact.CreatedAt.After(now) {
				continue
			}
			matches := false
			for _, step := range fact.WorkflowSteps {
				if step.Workflow == instance.Workflow && step.InstanceID == instance.InstanceID && effectiveWorkflowContractVersion(step.Version) == instance.ContractVersion && step.Milestone == requirement.Milestone {
					matches = true
					break
				}
			}
			if !matches {
				continue
			}
			prior := latest[requirement]
			if prior.ID == "" || fact.CreatedAt.After(prior.PublishedAt) || fact.CreatedAt.Equal(prior.PublishedAt) && (fact.OperationID > prior.OperationID || fact.OperationID == prior.OperationID && fact.ID > prior.ID) {
				latest[requirement] = api.OperationWorkflowInstanceMilestoneRef{ID: fact.ID, OperationID: fact.OperationID, OccurredAt: fact.OccurredAt, PublishedAt: fact.CreatedAt}
			}
		}
	}
	for _, requirement := range requirements {
		if evidence, ok := latest[requirement]; ok {
			instance.RetainedMilestoneEvidence = append(instance.RetainedMilestoneEvidence, api.OperationWorkflowRetainedMilestone{Requirement: requirement, Status: "verified", Evidence: &evidence})
		}
	}
	return nil
}

func (s *PgStore) projectRetainedMilestones(ctx context.Context, page *api.OperationMilestonesResponse, account, tenant string, opts api.OperationMilestoneListOptions, now time.Time) error {
	instance := page.WorkflowInstance
	requirements, err := retainedMilestoneRequirements(instance)
	if err != nil || len(requirements) == 0 {
		return err
	}
	tenant = retainedMilestoneTenant(instance, tenant)
	if tenant == "" {
		return nil
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
	raw, err := json.Marshal(requirements)
	if err != nil {
		return err
	}
	rows, err := sqlc.New().ListCustomerOperationRetainedMilestonePrerequisites(ctx, s.pool, sqlc.ListCustomerOperationRetainedMilestonePrerequisitesParams{
		AccountID: accountID, AppID: appID, TenantID: tenantID, Scope: opts.Scope, SubjectType: opts.SubjectType, SubjectID: opts.SubjectID,
		WorkflowName: instance.Workflow, InstanceID: instance.InstanceID, ContractVersion: int32(instance.ContractVersion), Requirements: raw,
		Now: pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("state: read retained milestone prerequisites: %w", mapErr(err))
	}
	for _, row := range rows {
		instance.RetainedMilestoneEvidence = append(instance.RetainedMilestoneEvidence, api.OperationWorkflowRetainedMilestone{
			Requirement: api.OperationWorkflowRetainedMilestoneRequirement{Operation: row.Operation, Milestone: row.Milestone}, Status: "verified",
			Evidence: &api.OperationWorkflowInstanceMilestoneRef{ID: row.ID, OperationID: row.OperationID, OccurredAt: row.OccurredAt.Time, PublishedAt: row.CreatedAt.Time},
		})
	}
	return nil
}
