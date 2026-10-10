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
		sources := append([]api.OperationWorkflowRetainedMilestoneRequirement(nil), edge.RequiredRetainedMilestones...)
		for _, rule := range edge.RetainedMilestoneInvalidations {
			sources = append(sources, rule.InvalidatedBy)
		}
		for _, requirement := range sources {
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
	latestOccurrence := map[api.OperationWorkflowRetainedMilestoneRequirement]api.OperationWorkflowInstanceMilestoneRef{}
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
			evidence := api.OperationWorkflowInstanceMilestoneRef{ID: fact.ID, OperationID: fact.OperationID, OccurredAt: fact.OccurredAt, PublishedAt: fact.CreatedAt}
			if retainedMilestoneAfter(evidence, latest[requirement], false) {
				latest[requirement] = evidence
			}
			if !evidence.OccurredAt.After(now) && retainedMilestoneAfter(evidence, latestOccurrence[requirement], true) {
				latestOccurrence[requirement] = evidence
			}
		}
	}
	for _, requirement := range requirements {
		if evidence, ok := latest[requirement]; ok {
			instance.RetainedMilestoneEvidence = append(instance.RetainedMilestoneEvidence, api.OperationWorkflowRetainedMilestone{Requirement: requirement, Status: "verified", Evidence: &evidence})
		}
	}
	for _, requirement := range requirements {
		if evidence, ok := latestOccurrence[requirement]; ok {
			instance.RetainedMilestoneOccurrenceEvidence = append(instance.RetainedMilestoneOccurrenceEvidence, api.OperationWorkflowRetainedMilestone{Requirement: requirement, Status: "verified", Evidence: &evidence})
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
		result := api.OperationWorkflowRetainedMilestone{
			Requirement: api.OperationWorkflowRetainedMilestoneRequirement{Operation: row.Operation, Milestone: row.Milestone}, Status: "verified",
			Evidence: &api.OperationWorkflowInstanceMilestoneRef{ID: row.ID, OperationID: row.OperationID, OccurredAt: row.OccurredAt.Time, PublishedAt: row.CreatedAt.Time},
		}
		if row.OccurrenceOrdered {
			instance.RetainedMilestoneOccurrenceEvidence = append(instance.RetainedMilestoneOccurrenceEvidence, result)
		} else {
			instance.RetainedMilestoneEvidence = append(instance.RetainedMilestoneEvidence, result)
		}
	}
	return nil
}

// Occurrence order expresses business progression. Publication and identities
// only break ties within one source; invalidation wins ties across sources.
func retainedMilestoneAfter(a, b api.OperationWorkflowInstanceMilestoneRef, occurrence bool) bool {
	if b.ID == "" {
		return true
	}
	if occurrence && !a.OccurredAt.Equal(b.OccurredAt) {
		return a.OccurredAt.After(b.OccurredAt)
	}
	return a.PublishedAt.After(b.PublishedAt) || a.PublishedAt.Equal(b.PublishedAt) && (a.OperationID > b.OperationID || a.OperationID == b.OperationID && a.ID > b.ID)
}

func evaluateRetainedMilestone(instance *api.OperationWorkflowInstanceSnapshot, edge api.OperationWorkflowInstanceTransition, requirement api.OperationWorkflowRetainedMilestoneRequirement) api.OperationWorkflowRetainedMilestone {
	result := api.OperationWorkflowRetainedMilestone{Requirement: requirement, Status: "awaiting_verification"}
	var rules []api.OperationWorkflowRetainedMilestoneInvalidationRule
	for _, rule := range edge.RetainedMilestoneInvalidations {
		if rule.Prerequisite == requirement {
			rules = append(rules, rule)
		}
	}
	facts := instance.RetainedMilestoneEvidence
	if len(rules) > 0 {
		facts = instance.RetainedMilestoneOccurrenceEvidence
	}
	for _, fact := range facts {
		if fact.Requirement == requirement && fact.Status == "verified" && fact.Evidence != nil {
			result = fact
			break
		}
	}
	if result.Evidence == nil {
		return result
	}
	for _, rule := range rules {
		for _, source := range instance.RetainedMilestoneOccurrenceEvidence {
			if source.Requirement != rule.InvalidatedBy || source.Status != "verified" || source.Evidence == nil || source.Evidence.OccurredAt.Before(result.Evidence.OccurredAt) {
				continue
			}
			if result.Invalidation == nil || retainedMilestoneAfter(*source.Evidence, result.Invalidation.Evidence, true) {
				result.Status = "invalidated"
				result.Invalidation = &api.OperationWorkflowRetainedMilestoneInvalidation{Source: source.Requirement, Evidence: *source.Evidence}
			}
		}
	}
	return result
}
