package state

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type OperationWorkflowReadinessQueueStore interface {
	ListAccountWorkflowReadinessQueue(context.Context, string, api.OperationWorkflowReadinessQueueOptions) (api.OperationWorkflowReadinessQueueResponse, error)
	ListPlatformTenantWorkflowReadinessQueue(context.Context, string, string, api.OperationWorkflowReadinessQueueOptions) (api.OperationWorkflowReadinessQueueResponse, error)
}
type readinessQueueStore interface {
	OperationMilestoneStore
	readReadinessQueueCandidates(context.Context, string, string, api.OperationWorkflowAttentionOptions, bool, time.Time) ([]workflowPerformanceInstance, error)
}

func prepareReadinessQueue(account, tenant string, opts api.OperationWorkflowReadinessQueueOptions, operator bool) (api.OperationWorkflowAttentionOptions, time.Time, error) {
	base, clock, err := prepareOperationAttention(account, tenant, api.OperationWorkflowAttentionOptions{AppID: opts.AppID, Scope: opts.Scope, TenantID: opts.TenantID, Workflow: opts.Workflow, TargetOperation: opts.TargetOperation, Owner: opts.Owner, Unassigned: opts.Unassigned, Limit: opts.Limit}, operator, "workflow-readiness-queue")
	if err != nil || opts.Requirement != "" && !slices.Contains([]string{"state_unknown", "terminal", "transition_undeclared", "from_state_mismatch", "revision_mismatch", "contract_version_mismatch", "application_blocked", "dependency_unmet", "milestone_required", "policy_evidence_required", "dependency_required", "invariant_evidence_required", "effect_evidence_required", "retained_milestone_required", "retained_milestone_invalidated", "retained_milestone_expired", "retained_milestone_group_unmet", "path_selection_invalid"}, opts.Requirement) {
		return base, clock.EvaluatedAt, ErrInvalidArgument
	}
	return base, clock.EvaluatedAt, nil
}
func listReadinessQueue(ctx context.Context, store readinessQueueStore, account, tenant string, opts api.OperationWorkflowReadinessQueueOptions, operator bool) (api.OperationWorkflowReadinessQueueResponse, error) {
	base, at, err := prepareReadinessQueue(account, tenant, opts, operator)
	out := api.OperationWorkflowReadinessQueueResponse{Items: []api.OperationWorkflowReadinessQueueEntry{}, SampledAt: at}
	if err != nil {
		return out, err
	}
	instances, err := store.readReadinessQueueCandidates(ctx, account, tenant, base, operator, at)
	if err != nil {
		return out, err
	}
	out.SampledWorkflowCount = int64(len(instances))
	if len(instances) > 0 {
		out.MatchingWorkflowCount = instances[0].CohortTotal
	}
	out.WorkflowSampleTruncated = out.MatchingWorkflowCount > out.SampledWorkflowCount
	for _, candidate := range instances {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		projection := api.OperationMilestoneListOptions{ReadinessOnly: true, AppID: base.AppID, Scope: base.Scope, SubjectType: candidate.Subject.Type, SubjectID: candidate.Subject.ID, Workflow: candidate.Current.Workflow, WorkflowInstanceID: candidate.Current.InstanceID, Limit: 1}
		var page api.OperationMilestonesResponse
		if operator {
			projection.TenantID = candidate.Tenant
			page, err = store.ListAccountOperationMilestones(ctx, account, projection)
		} else {
			page, err = store.ListPlatformTenantOperationMilestones(ctx, account, tenant, projection)
		}
		if err != nil {
			return out, err
		}
		appendReadinessQueueWorkflow(&out, candidate, page.WorkflowInstance, base, opts.Requirement, operator)
	}
	sort.SliceStable(out.Items, func(i, j int) bool { return !out.Items[i].Ready && out.Items[j].Ready })
	out.HasMore = out.MatchingTransitionCount > int64(base.Limit)
	if len(out.Items) > base.Limit {
		out.Items = out.Items[:base.Limit]
	}
	out.EvaluatedAt = time.Now().UTC().Truncate(time.Microsecond)
	return out, nil
}
func appendReadinessQueueWorkflow(out *api.OperationWorkflowReadinessQueueResponse, candidate workflowPerformanceInstance, instance *api.OperationWorkflowInstanceSnapshot, opts api.OperationWorkflowAttentionOptions, requirement string, operator bool) {
	if instance == nil || instance.State == nil || instance.State.Terminal || instance.State.OperationID != candidate.Current.OperationID || instance.State.ReportID != candidate.Current.ReportID || instance.State.Revision != candidate.Current.Revision || instance.ContractVersion != candidate.Current.ContractVersion {
		out.ExcludedChangedWorkflowCount++
		return
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	if instance.ConditionReporting != nil {
		at = instance.ConditionReporting.EvaluatedAt
	}
	if !instance.RetainedMilestoneEvaluatedAt.IsZero() {
		at = instance.RetainedMilestoneEvaluatedAt
	}
	instance.State.Stale = operationWorkflowStateIsStale(at, *instance.State)
	evaluateOperationWorkflowDeadline(at, instance.State)
	edges := append([]api.OperationWorkflowInstanceTransition(nil), instance.AllowedTransitions...)
	sort.Slice(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.Operation != b.Operation {
			return a.Operation < b.Operation
		}
		if a.From != b.From {
			return a.From < b.From
		}
		return a.To < b.To
	})
	count := 0
	for _, edge := range edges {
		if edge.From != instance.State.State || opts.TargetOperation != "" && edge.Operation != opts.TargetOperation {
			continue
		}
		count++
		out.DeclaredTransitionCount++
		if count > api.OperationHistoryPageMax {
			out.TransitionSampleTruncated = true
			continue
		}
		out.EvaluatedTransitionCount++
		r := evaluateOperationWorkflowReadiness(instance, api.OperationWorkflowReadinessRequest{Scope: opts.Scope, Subject: candidate.Subject, Workflow: instance.Workflow, InstanceID: instance.InstanceID, Operation: edge.Operation, FromState: edge.From, ToState: edge.To, StateRevision: instance.State.Revision, ContractVersion: instance.ContractVersion})
		row := readinessQueueEntry(candidate, instance, r, at, operator)
		if requirement != "" && !slices.Contains(row.Reasons, requirement) || opts.Owner != "" && !slices.Contains(row.Owners, opts.Owner) || opts.Unassigned && !row.Unassigned {
			continue
		}
		out.MatchingTransitionCount++
		if row.Ready {
			out.ReadyTransitionCount++
		} else {
			out.UnmetTransitionCount++
		}
		// Retain only enough of each readiness class to produce the final limited queue.
		if row.Ready && out.ReadyTransitionCount <= int64(opts.Limit) || !row.Ready && out.UnmetTransitionCount <= int64(opts.Limit) {
			out.Items = append(out.Items, row)
		}
	}
	if count == 0 {
		out.NoTransitionWorkflowCount++
	}
}
func readinessQueueEntry(candidate workflowPerformanceInstance, instance *api.OperationWorkflowInstanceSnapshot, r api.OperationWorkflowTransitionReadiness, at time.Time, operator bool) api.OperationWorkflowReadinessQueueEntry {
	row := api.OperationWorkflowReadinessQueueEntry{PendingVerifications: r.PendingVerifications, PendingVerificationsTruncated: r.PendingVerificationsTruncated, Subject: candidate.Subject, Workflow: instance.Workflow, InstanceID: instance.InstanceID, OperationID: instance.State.OperationID, StateRevision: r.StateRevision, ContractVersion: r.ContractVersion, Transition: r.Transition, EvaluatedAt: at, Ready: r.Ready, Reasons: r.Reasons, Advisories: r.Advisories, Owners: []string{}, Blockers: r.Blockers, MissingMilestones: r.MissingMilestones, MissingDependencyWorkflows: r.MissingDependencyWorkflows, PendingVerificationCount: r.PendingVerificationCount, ReportingStatus: "definition_unavailable"}
	if operator {
		row.PlatformTenantID = candidate.Tenant
	}
	addOwner := func(owner string) {
		if owner == "" {
			row.Unassigned = true
		} else if !slices.Contains(row.Owners, owner) {
			row.Owners = append(row.Owners, owner)
		}
	}
	for _, b := range r.Blockers {
		addOwner(b.Owner)
	}
	for _, v := range selectedVerifications(instance.ReadinessVerifications, api.OperationWorkflowAttentionOptions{TargetOperation: r.Transition.Operation}) {
		addOwner(v.Resolution.VerificationOwner)
	}
	if row.MissingDependencyWorkflows == nil {
		row.MissingDependencyWorkflows = []string{}
	}
	sort.Strings(row.Owners)
	if r.Reporting != nil {
		row.ReportingStatus = r.Reporting.Status
		row.MissingReportCount = r.Reporting.MissingConditionCount
		row.OverdueReportCount = r.Reporting.OverdueConditionCount
	}
	return row
}
func (m *MemStore) ListAccountWorkflowReadinessQueue(ctx context.Context, account string, opts api.OperationWorkflowReadinessQueueOptions) (api.OperationWorkflowReadinessQueueResponse, error) {
	return listReadinessQueue(ctx, m, account, opts.TenantID, opts, true)
}
func (m *MemStore) ListPlatformTenantWorkflowReadinessQueue(ctx context.Context, account, tenant string, opts api.OperationWorkflowReadinessQueueOptions) (api.OperationWorkflowReadinessQueueResponse, error) {
	return listReadinessQueue(ctx, m, account, tenant, opts, false)
}
func (s *PgStore) ListAccountWorkflowReadinessQueue(ctx context.Context, account string, opts api.OperationWorkflowReadinessQueueOptions) (api.OperationWorkflowReadinessQueueResponse, error) {
	return listReadinessQueue(ctx, s, account, opts.TenantID, opts, true)
}
func (s *PgStore) ListPlatformTenantWorkflowReadinessQueue(ctx context.Context, account, tenant string, opts api.OperationWorkflowReadinessQueueOptions) (api.OperationWorkflowReadinessQueueResponse, error) {
	return listReadinessQueue(ctx, s, account, tenant, opts, false)
}
func (m *MemStore) readReadinessQueueCandidates(ctx context.Context, account, tenant string, opts api.OperationWorkflowAttentionOptions, operator bool, at time.Time) ([]workflowPerformanceInstance, error) {
	if operator {
		tenant = opts.TenantID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	instances := []workflowPerformanceInstance{}
	for _, record := range data.workflowStates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !sameOperationHistoryIdentity(record.AccountID, account) || !sameOperationHistoryIdentity(record.AppID, opts.AppID) || record.Scope != opts.Scope || tenant != "" && !sameOperationHistoryIdentity(record.TenantID, tenant) || opts.Workflow != "" && record.State.Workflow != opts.Workflow || record.State.Terminal || record.State.UpdatedAt.After(at) {
			continue
		}
		receipt, ok := data.workflowStateReports[record.OperationID+"/"+record.ReportID]
		if !ok || receipt.History.PublishedAt.After(at) {
			continue
		}
		op, ok := data.operations[record.OperationID]
		if !ok || !operationRetained(op, at) || op.Subject == nil {
			continue
		}
		instances = append(instances, workflowPerformanceInstance{Current: record.State, Tenant: record.TenantID, Subject: *op.Subject, Key: operationAttentionKey(record.TenantID, *op.Subject, record.State.Workflow, record.State.InstanceID)})
	}
	sort.Slice(instances, func(i, j int) bool {
		if !instances[i].Current.UpdatedAt.Equal(instances[j].Current.UpdatedAt) {
			return instances[i].Current.UpdatedAt.After(instances[j].Current.UpdatedAt)
		}
		return instances[i].Key > instances[j].Key
	})
	total := int64(len(instances))
	if len(instances) > api.OperationWorkflowPerformanceCohortMax {
		instances = instances[:api.OperationWorkflowPerformanceCohortMax]
	}
	for i := range instances {
		instances[i].CohortTotal = total
	}
	return instances, nil
}
