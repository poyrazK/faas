package state

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type OperationWorkflowReadinessComparisonStore interface {
	CompareAccountWorkflowReadiness(context.Context, string, string, api.OperationWorkflowReadinessComparisonRequest) (api.OperationWorkflowReadinessComparisonResponse, error)
	ComparePlatformTenantWorkflowReadiness(context.Context, string, string, api.OperationWorkflowReadinessComparisonRequest) (api.OperationWorkflowReadinessComparisonResponse, error)
}

func readinessRequirementCodes() []string {
	return []string{"state_unknown", "terminal", "transition_undeclared", "from_state_mismatch", "revision_mismatch", "contract_version_mismatch", "application_blocked", "dependency_unmet", "milestone_required", "policy_evidence_required", "dependency_required", "invariant_evidence_required", "effect_evidence_required", "retained_milestone_required", "retained_milestone_invalidated", "retained_milestone_expired", "retained_milestone_group_unmet", "path_selection_invalid"}
}
func validReadinessDigest(s string) bool {
	return len(s) == 64 && strings.Trim(s, "0123456789abcdef") == ""
}
func readinessUniqueNames(values []string, max int, allowed []string) bool {
	if len(values) > max {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] || allowed != nil && !slices.Contains(allowed, value) || allowed == nil && (len(value) == 0 || len(value) > api.OperationNameMaxBytes || !operationHistoryName.MatchString(value)) {
			return false
		}
		seen[value] = true
	}
	return true
}
func validateReadinessBaseline(b api.OperationWorkflowReadinessSnapshot, expected api.OperationWorkflowReadinessSnapshot, tenant string, operator bool, at time.Time) error {
	if b.Version != 1 || b.Items == nil || b.Query != expected.Query || b.CapturedAt.IsZero() || b.CapturedAt.After(at) || len(b.Items) > api.OperationHistoryPageMax {
		return ErrInvalidArgument
	}
	seen := map[string]bool{}
	for _, row := range b.Items {
		validName := func(value string) bool {
			return len(value) > 0 && len(value) <= api.OperationNameMaxBytes && operationHistoryName.MatchString(value)
		}
		if row.Reasons == nil || row.Advisories == nil || row.BlockerCodes == nil || row.MissingMilestones == nil || row.MissingDependencyWorkflows == nil || api.ValidateOperationSubject(row.Subject) != nil || api.ValidateOperationWorkflowName(row.Workflow) != nil || api.ValidateOperationWorkflowInstanceID(row.InstanceID) != nil || !validName(row.Operation) || !validName(row.FromState) || !validName(row.ToState) || row.StateRevision < 1 || row.StateRevision > 9007199254740991 || row.ContractVersion < 1 || row.ContractVersion > api.OperationWorkflowContractVersionMax || row.PendingVerificationCount < 0 || row.EvaluatedAt.IsZero() || row.EvaluatedAt.After(b.CapturedAt) || row.Ready != (len(row.Reasons) == 0) {
			return ErrInvalidArgument
		}
		if _, err := uuid.Parse(row.OperationID); err != nil {
			return ErrInvalidArgument
		}
		if operator {
			parsed, err := uuid.Parse(row.PlatformTenantID)
			if err != nil || parsed.String() != row.PlatformTenantID || tenant != "" && tenant != row.PlatformTenantID {
				return ErrInvalidArgument
			}
		} else if row.PlatformTenantID != "" {
			return ErrInvalidArgument
		}
		for _, digest := range []string{row.RequirementsFingerprint, row.BlockersFingerprint, row.PrerequisitesFingerprint, row.VerificationsFingerprint, row.ReportingFingerprint} {
			if !validReadinessDigest(digest) {
				return ErrInvalidArgument
			}
		}
		if !readinessUniqueNames(row.Reasons, len(readinessRequirementCodes()), readinessRequirementCodes()) || !readinessUniqueNames(row.Advisories, 6, []string{"state_stale", "deadline_overdue", "condition_reports_missing", "condition_reports_overdue", "condition_reporting_unknown", "verification_pending"}) || !readinessUniqueNames(row.BlockerCodes, api.OperationMilestoneDeclarationsMax, nil) || !readinessUniqueNames(row.MissingMilestones, api.OperationMilestoneDeclarationsMax, nil) || !readinessUniqueNames(row.MissingDependencyWorkflows, api.OperationMilestoneDeclarationsMax, nil) {
			return ErrInvalidArgument
		}
		key := readinessSnapshotKey(row)
		if seen[key] {
			return ErrInvalidArgument
		}
		seen[key] = true
	}
	return nil
}
func compareWorkflowReadiness(ctx context.Context, store OperationMilestoneStore, account, tenant, app string, req api.OperationWorkflowReadinessComparisonRequest, operator bool) (api.OperationWorkflowReadinessComparisonResponse, error) {
	out := api.OperationWorkflowReadinessComparisonResponse{Items: []api.OperationWorkflowReadinessChange{}, BaselineAt: req.Baseline.CapturedAt}
	if operator && req.AppID != "" || !operator && req.TenantID != "" {
		return out, ErrInvalidArgument
	}
	base, clock, err := prepareOperationAttention(account, tenant, api.OperationWorkflowAttentionOptions{AppID: app, Scope: req.Scope, TenantID: req.TenantID, Limit: req.Limit}, operator, "readiness-comparison")
	if err != nil || !slices.Contains([]string{"", "newly_ready", "newly_blocked", "changed", "unchanged", "unavailable"}, req.Change) {
		return out, ErrInvalidArgument
	}
	out.Snapshot = newReadinessSnapshot(account, tenant, base, operator)
	if err := validateReadinessBaseline(req.Baseline, out.Snapshot, base.TenantID, operator, clock.EvaluatedAt); err != nil {
		return out, err
	}
	out.BaselineTransitionCount = int64(len(req.Baseline.Items))
	projections := map[string]*api.OperationWorkflowInstanceSnapshot{}
	for _, before := range req.Baseline.Items {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		workflowKey := operationAttentionKey(before.PlatformTenantID, before.Subject, before.Workflow, before.InstanceID)
		instance, loaded := projections[workflowKey]
		if !loaded {
			projection := api.OperationMilestoneListOptions{ReadinessOnly: true, AppID: base.AppID, Scope: base.Scope, SubjectType: before.Subject.Type, SubjectID: before.Subject.ID, Workflow: before.Workflow, WorkflowInstanceID: before.InstanceID, Limit: 1}
			var page api.OperationMilestonesResponse
			if operator {
				projection.TenantID = before.PlatformTenantID
				page, err = store.ListAccountOperationMilestones(ctx, account, projection)
			} else {
				page, err = store.ListPlatformTenantOperationMilestones(ctx, account, tenant, projection)
			}
			if err != nil {
				return out, err
			}
			instance = page.WorkflowInstance
			projections[workflowKey] = instance
		}
		change, after := readinessTransitionChange(before, instance, operator)
		if after != nil {
			out.Snapshot.Items = append(out.Snapshot.Items, *after)
		}
		countReadinessChange(&out, change.Status)
		if readinessChangeMatches(change.Status, req.Change) {
			out.MatchingChangeCount++
			if len(out.Items) < base.Limit {
				out.Items = append(out.Items, change)
			}
		}
	}
	out.HasMore = out.MatchingChangeCount > int64(len(out.Items))
	out.EvaluatedAt = time.Now().UTC().Truncate(time.Microsecond)
	out.Snapshot.CapturedAt = out.EvaluatedAt
	return out, nil
}
func readinessTransitionChange(before api.OperationWorkflowReadinessSnapshotEntry, instance *api.OperationWorkflowInstanceSnapshot, operator bool) (api.OperationWorkflowReadinessChange, *api.OperationWorkflowReadinessSnapshotEntry) {
	change := api.OperationWorkflowReadinessChange{Baseline: before, Status: "state_unknown", AddedReasons: []string{}, ResolvedReasons: []string{}, ChangedCategories: []string{}, Explanation: "Current retained state is unavailable; this is not evidence that requirements were resolved."}
	if instance == nil || instance.State == nil {
		return change, nil
	}
	state := instance.State
	change.CurrentOperationID = state.OperationID
	change.CurrentState = state.State
	if state.Terminal {
		change.Status = "terminal"
		change.Explanation = "The workflow is now terminal. The saved transition is not compared as newly ready or blocked."
		return change, nil
	}
	if state.Revision < before.StateRevision {
		change.Status = "baseline_ahead"
		change.Explanation = "Retained state is older than the saved revision; no readiness change is inferred."
		return change, nil
	}
	declared := false
	for _, edge := range instance.AllowedTransitions {
		if edge.Operation == before.Operation && edge.From == before.FromState && edge.To == before.ToState {
			declared = true
			break
		}
	}
	if !declared || state.State != before.FromState {
		change.Status = "transition_unavailable"
		change.Explanation = "The saved edge is no longer a declared transition from the current state. This does not establish readiness."
		return change, nil
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	if instance.ConditionReporting != nil {
		at = instance.ConditionReporting.EvaluatedAt
	}
	if !instance.RetainedMilestoneEvaluatedAt.IsZero() {
		at = instance.RetainedMilestoneEvaluatedAt
	}
	state.Stale = operationWorkflowStateIsStale(at, *state)
	evaluateOperationWorkflowDeadline(at, state)
	r := evaluateOperationWorkflowReadiness(instance, api.OperationWorkflowReadinessRequest{Subject: before.Subject, Workflow: before.Workflow, InstanceID: before.InstanceID, Operation: before.Operation, FromState: before.FromState, ToState: before.ToState, StateRevision: state.Revision, ContractVersion: instance.ContractVersion})
	row := readinessQueueEntry(workflowPerformanceInstance{Subject: before.Subject, Tenant: before.PlatformTenantID}, instance, r, at, operator)
	after := readinessSnapshotEntry(row, instance, r)
	change.Current = &row
	if before.ContractVersion != after.ContractVersion || before.RequirementsFingerprint != after.RequirementsFingerprint {
		change.Status = "contract_changed"
		change.Explanation = "The pinned contract or declared requirements changed. Readiness is not compared across that boundary."
		return change, &after
	}
	change.AddedReasons = readinessDifference(after.Reasons, before.Reasons)
	change.ResolvedReasons = readinessDifference(before.Reasons, after.Reasons)
	for _, category := range []struct{ name, before, after string }{{"blockers", before.BlockersFingerprint, after.BlockersFingerprint}, {"prerequisites", before.PrerequisitesFingerprint, after.PrerequisitesFingerprint}, {"verification", before.VerificationsFingerprint, after.VerificationsFingerprint}, {"reporting", before.ReportingFingerprint, after.ReportingFingerprint}} {
		if category.before != category.after {
			change.ChangedCategories = append(change.ChangedCategories, category.name)
		}
	}
	change.Status = "unchanged"
	change.Explanation = "Declared readiness requirements and observed evidence summaries are unchanged."
	if before.Ready && !after.Ready {
		change.Status = "newly_blocked"
		change.Explanation = "Declared requirements were met in the saved snapshot and are now unmet."
	} else if !before.Ready && after.Ready {
		change.Status = "newly_ready"
		change.Explanation = "Previously unmet declared requirements are now met without planned evidence. The application must still authorize execution."
	} else if len(change.AddedReasons) > 0 || len(change.ResolvedReasons) > 0 || len(change.ChangedCategories) > 0 {
		change.Status = "changed"
		change.Explanation = "Evidence or advisories changed while the declared readiness result stayed the same."
	}
	return change, &after
}
func readinessDifference(a, b []string) []string {
	out := []string{}
	for _, v := range a {
		if !slices.Contains(b, v) {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
func readinessChangeMatches(status, filter string) bool {
	if filter == "" {
		return true
	}
	if filter == "unavailable" {
		return !slices.Contains([]string{"newly_ready", "newly_blocked", "changed", "unchanged"}, status)
	}
	return status == filter
}
func countReadinessChange(out *api.OperationWorkflowReadinessComparisonResponse, status string) {
	switch status {
	case "newly_ready":
		out.NewlyReadyCount++
		out.ComparedTransitionCount++
	case "newly_blocked":
		out.NewlyBlockedCount++
		out.ComparedTransitionCount++
	case "changed":
		out.ChangedCount++
		out.ComparedTransitionCount++
	case "unchanged":
		out.UnchangedCount++
		out.ComparedTransitionCount++
	default:
		out.UnavailableCount++
	}
}
func (m *MemStore) CompareAccountWorkflowReadiness(ctx context.Context, account, app string, req api.OperationWorkflowReadinessComparisonRequest) (api.OperationWorkflowReadinessComparisonResponse, error) {
	return compareWorkflowReadiness(ctx, m, account, req.TenantID, app, req, true)
}
func (m *MemStore) ComparePlatformTenantWorkflowReadiness(ctx context.Context, account, tenant string, req api.OperationWorkflowReadinessComparisonRequest) (api.OperationWorkflowReadinessComparisonResponse, error) {
	return compareWorkflowReadiness(ctx, m, account, tenant, req.AppID, req, false)
}
func (s *PgStore) CompareAccountWorkflowReadiness(ctx context.Context, account, app string, req api.OperationWorkflowReadinessComparisonRequest) (api.OperationWorkflowReadinessComparisonResponse, error) {
	return compareWorkflowReadiness(ctx, s, account, req.TenantID, app, req, true)
}
func (s *PgStore) ComparePlatformTenantWorkflowReadiness(ctx context.Context, account, tenant string, req api.OperationWorkflowReadinessComparisonRequest) (api.OperationWorkflowReadinessComparisonResponse, error) {
	return compareWorkflowReadiness(ctx, s, account, tenant, req.AppID, req, false)
}
