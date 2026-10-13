package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type OperationWorkflowCaseStore interface {
	WorkflowCase(context.Context, string, string, api.OperationWorkflowCoordinationTimelineOptions, bool) (api.OperationWorkflowCase, error)
}
type workflowCaseBackend interface {
	readPlaybookProgress(context.Context, workflowPlaybookProgressRecord) ([]api.OperationWorkflowPlaybookProgress, error)
	readinessSubscriptionBackend
	readinessFollowUpBackend
	OperationDefinitionByID(context.Context, string, string) (OperationDefinition, error)
}

func (m *MemStore) WorkflowCase(ctx context.Context, account, tenant string, opts api.OperationWorkflowCoordinationTimelineOptions, operator bool) (api.OperationWorkflowCase, error) {
	return workflowCase(ctx, m, account, tenant, opts, operator)
}
func (s *PgStore) WorkflowCase(ctx context.Context, account, tenant string, opts api.OperationWorkflowCoordinationTimelineOptions, operator bool) (api.OperationWorkflowCase, error) {
	return workflowCase(ctx, s, account, tenant, opts, operator)
}
func workflowCase(ctx context.Context, store workflowCaseBackend, account, tenant string, opts api.OperationWorkflowCoordinationTimelineOptions, operator bool) (api.OperationWorkflowCase, error) {
	out := api.OperationWorkflowCase{FollowUps: []api.OperationWorkflowReadinessFollowUp{}, Actions: []api.OperationWorkflowCaseAction{}, Limitations: []string{
		"Application evidence, follow-ups and timeline are read independently; this is a live view, not a transactionally consistent snapshot.",
		"Coordination retention is bounded. Deleted inboxes and pruned work are unavailable; follow-up counts cover retained matching work only.",
		"Suggestions are advisory. Readiness is not execution permission, terminal state is not inferred success, and completing coordination does not establish a business result.",
	}}
	// Account business reports are customer-owned. Never use a broad operator evidence query for this exact case.
	if operator && opts.TenantID == "" {
		return out, ErrInvalidArgument
	}
	historyTenant := tenant
	if operator {
		historyTenant = opts.TenantID
	}
	history := newWorkflowCaseObligationHistory(historyTenant, opts, operator)
	timeline, err := workflowCoordinationTimeline(ctx, store, account, tenant, opts, operator, history.collect)
	if err != nil {
		return out, err
	}
	out.Timeline = timeline
	if operator {
		tenant = opts.TenantID
	}
	tenant = canonicalReadinessTenant(tenant)
	projection := api.OperationMilestoneListOptions{AppID: opts.AppID, Scope: opts.Scope, SubjectType: opts.SubjectType, SubjectID: opts.SubjectID, Workflow: opts.Workflow, WorkflowInstanceID: opts.InstanceID, Limit: 1}
	var page api.OperationMilestonesResponse
	if operator {
		projection.TenantID = tenant
		page, err = store.ListAccountOperationMilestones(ctx, account, projection)
	} else {
		page, err = store.ListPlatformTenantOperationMilestones(ctx, account, tenant, projection)
	}
	if err != nil {
		return out, err
	}
	out.EvidenceCheckedAt = time.Now().UTC().Truncate(time.Microsecond)
	out.Instance = page.WorkflowInstance
	if out.Instance != nil && out.Instance.State != nil {
		out.Instance.State.Stale = out.Instance.State.Stale || operationWorkflowStateIsStale(out.EvidenceCheckedAt, *out.Instance.State)
		evaluateOperationWorkflowDeadline(out.EvidenceCheckedAt, out.Instance.State)
		projectOperationWorkflowReadiness(&page)
	}
	rows, err := store.readReadinessFollowUps(ctx, account, tenant, projection.AppID, opts.Scope, operator)
	if err != nil {
		return out, err
	}
	out.CoordinationCheckedAt = time.Now().UTC().Truncate(time.Microsecond)
	projectWorkflowCaseNextActions(&out, rows, tenant, opts, operator)
	history.finish(&out)
	for _, row := range rows {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		if row.Subject == nil || *row.Subject != timeline.Subject || row.Workflow != opts.Workflow || row.InstanceID != opts.InstanceID || row.Coordination.FollowUpAt == nil || row.Coordination.Completion != nil || operator && !sameOperationHistoryIdentity(row.PlatformTenantID, tenant) {
			continue
		}
		row.Overdue = !row.Coordination.FollowUpAt.After(out.CoordinationCheckedAt)
		if row.Reminder != nil && (!row.Reminder.DueAt.Equal(*row.Coordination.FollowUpAt) || !row.Overdue) {
			row.Reminder = nil
		}
		if !operator {
			row.PlatformTenantID = ""
			row.SubscriptionTenantID = ""
		}
		if row.Overdue {
			out.OverdueFollowUpCount++
		}
		out.FollowUps = append(out.FollowUps, row)
	}
	sort.Slice(out.FollowUps, func(i, j int) bool {
		a, b := out.FollowUps[i], out.FollowUps[j]
		if !a.Coordination.FollowUpAt.Equal(*b.Coordination.FollowUpAt) {
			return a.Coordination.FollowUpAt.Before(*b.Coordination.FollowUpAt)
		}
		if a.SubscriptionID != b.SubscriptionID {
			return a.SubscriptionID < b.SubscriptionID
		}
		return a.Sequence < b.Sequence
	})
	out.FollowUpCount = len(out.FollowUps)
	out.FollowUpsTruncated = len(out.FollowUps) > api.OperationWorkflowCaseFollowUpsMax
	if out.FollowUpsTruncated {
		out.FollowUps = out.FollowUps[:api.OperationWorkflowCaseFollowUpsMax]
	}
	summarizeWorkflowCase(&out)
	playbooks, err := projectWorkflowCasePlaybooks(ctx, store, account, tenant, opts, &out)
	if err != nil {
		return out, err
	}

	identityOpts := opts
	identityOpts.Offset = 0
	identityOpts.Limit = 0
	identity, err := preparePlaybookProgressIdentity(ctx, store, account, tenant, identityOpts, operator)
	if err != nil {
		return out, err
	}
	out.PlaybookProgress, err = store.readPlaybookProgress(ctx, identity)
	if err != nil {
		return out, err
	}
	projectWorkflowPlaybookApplicability(&out, playbooks)
	projectWorkflowPlaybookEvidence(&out, playbooks)
	projectWorkflowPlaybookBusinessOutcomes(&out)
	projectPlaybookReplacementLinks(out.PlaybookProgress)
	out.Limitations = append(out.Limitations, "Playbook progress records human review of pinned guidance. Evidence references are checked on edit; checklist completion does not establish readiness or business success.")
	projectWorkflowCaseObligationAssessments(&out, rows, tenant, operator)
	out.EvaluatedAt = time.Now().UTC().Truncate(time.Microsecond)
	return out, nil
}
