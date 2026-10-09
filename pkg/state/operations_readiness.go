package state

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type OperationWorkflowReadinessStore interface {
	CheckAccountWorkflowReadiness(context.Context, string, string, api.OperationWorkflowReadinessRequest) (api.OperationWorkflowReadinessResponse, error)
	CheckPlatformTenantWorkflowReadiness(context.Context, string, string, api.OperationWorkflowReadinessRequest) (api.OperationWorkflowReadinessResponse, error)
}

func validateOperationWorkflowReadiness(req api.OperationWorkflowReadinessRequest, operator bool) error {
	validName := func(value string) bool {
		return len(value) > 0 && len(value) <= 64 && operationHistoryName.MatchString(value)
	}
	if api.ValidateScope(req.Scope) != nil || api.ValidateOperationSubject(req.Subject) != nil || api.ValidateOperationWorkflowName(req.Workflow) != nil || api.ValidateOperationWorkflowInstanceID(req.InstanceID) != nil || !validName(req.Operation) || !validName(req.FromState) || !validName(req.ToState) || req.StateRevision < 0 || req.StateRevision > 9007199254740991 || req.ContractVersion < 0 || req.ContractVersion > 1000000 || len(req.Milestones) > 16 || len(req.Decisions) > 16 || len(req.Invariants) > 16 || len(req.Effects) > 16 {
		return ErrInvalidArgument
	}
	if operator && (req.TenantID == "" || req.AppID != "") || !operator && (req.TenantID != "" || req.AppID == "") {
		return ErrInvalidArgument
	}
	seenEffects := map[string]bool{}
	for _, planned := range req.Effects {
		if api.ValidateOperationBusinessEffect(planned.Effect) != nil || !validName(planned.Milestone) || seenEffects[planned.Milestone] {
			return ErrInvalidArgument
		}
		seenEffects[planned.Milestone] = true
	}
	seenInvariants := map[string]bool{}
	for _, planned := range req.Invariants {
		if _, err := api.CanonicalOperationBusinessInvariant(planned.Invariant); err != nil || !validName(planned.Milestone) || seenInvariants[planned.Milestone] {
			return ErrInvalidArgument
		}
		seenInvariants[planned.Milestone] = true
	}
	seenDecisions := map[string]bool{}
	for _, planned := range req.Decisions {
		if !validName(planned.Milestone) || seenDecisions[planned.Milestone] || api.ValidateOperationBusinessDecision(planned.Decision) != nil || planned.Decision.Workflow != req.Workflow || planned.Decision.InstanceID != req.InstanceID {
			return ErrInvalidArgument
		}
		seenDecisions[planned.Milestone] = true
	}
	seen := map[string]bool{}
	for _, name := range req.Milestones {
		if !validName(name) || seen[name] {
			return ErrInvalidArgument
		}
		seen[name] = true
	}
	return nil
}

func evaluateOperationWorkflowReadiness(instance *api.OperationWorkflowInstanceSnapshot, req api.OperationWorkflowReadinessRequest) api.OperationWorkflowTransitionReadiness {
	out := api.OperationWorkflowTransitionReadiness{Transition: api.OperationWorkflowInstanceTransition{From: req.FromState, To: req.ToState, Operation: req.Operation}, Reasons: make([]string, 0), Advisories: make([]string, 0), Blockers: make([]api.OperationWorkflowBlocker, 0), UnmetDependencies: make([]api.OperationWorkflowRelatedInstance, 0), MissingMilestones: make([]string, 0)}
	reason := func(value string) { out.Reasons = append(out.Reasons, value) }
	if instance == nil {
		reason("state_unknown")
		reason("transition_undeclared")
		return out
	}
	out.ContractVersion = instance.ContractVersion
	for _, edge := range instance.AllowedTransitions {
		if edge.Operation == req.Operation && edge.From == req.FromState && edge.To == req.ToState {
			out.Declared = true
			out.Transition = edge
			out.Transition.RequiredMilestones = append([]string(nil), edge.RequiredMilestones...)
			break
		}
	}
	if !out.Declared {
		reason("transition_undeclared")
	}
	if req.ContractVersion != 0 && req.ContractVersion != instance.ContractVersion {
		reason("contract_version_mismatch")
	}
	state := instance.State
	if state == nil {
		reason("state_unknown")
	} else {
		out.StateRevision = state.Revision
		if state.Terminal {
			reason("terminal")
		}
		if state.State != req.FromState {
			reason("from_state_mismatch")
		}
		if req.StateRevision != 0 && req.StateRevision != state.Revision {
			reason("revision_mismatch")
		}
		for _, blocker := range state.Blockers {
			if blocker.Operation == req.Operation {
				out.Blockers = append(out.Blockers, blocker)
				if strings.HasPrefix(blocker.Code, api.OperationBusinessInvariantBlockerPrefix) {
					out.InvariantBlockers = append(out.InvariantBlockers, blocker)
				}
			}
		}
		if len(out.Blockers) > 0 {
			reason("application_blocked")
		}
		selected := map[string]bool{}
		found := map[string]bool{}
		if out.Transition.RequiredDependencyWorkflows != nil {
			for _, workflow := range *out.Transition.RequiredDependencyWorkflows {
				selected[workflow] = true
			}
		}
		for _, dependency := range state.DependsOn {
			if out.Transition.RequiredDependencyWorkflows != nil && !selected[dependency.Workflow] {
				continue
			}
			found[dependency.Workflow] = true
			related := api.OperationWorkflowRelatedInstance{Dependency: dependency, Status: "unknown"}
			for _, candidate := range instance.RelatedWorkflows {
				if candidate.Dependency == dependency {
					related = candidate
					break
				}
			}
			// Readiness carries the unmet reference/status; full states remain in detail.
			related.State = nil
			if related.Status != "terminal" && related.Status != "satisfied" {
				out.UnmetDependencies = append(out.UnmetDependencies, related)
			}
		}
		if out.Transition.RequiredDependencyWorkflows != nil {
			for _, workflow := range *out.Transition.RequiredDependencyWorkflows {
				if !found[workflow] {
					out.MissingDependencyWorkflows = append(out.MissingDependencyWorkflows, workflow)
				}
			}
		}
		if len(out.MissingDependencyWorkflows) > 0 {
			reason("dependency_required")
		}
		if len(out.UnmetDependencies) > 0 {
			reason("dependency_unmet")
		}
		if state.Stale {
			out.Advisories = append(out.Advisories, "state_stale")
		}
		if state.Overdue {
			out.Advisories = append(out.Advisories, "deadline_overdue")
		}
	}
	planned := map[string]bool{}
	for _, name := range req.Milestones {
		planned[name] = true
	}
	for _, name := range out.Transition.RequiredMilestones {
		if !planned[name] {
			out.MissingMilestones = append(out.MissingMilestones, name)
		}
	}
	sort.Strings(out.MissingMilestones)
	if len(out.MissingMilestones) > 0 {
		reason("milestone_required")
	}
	for _, policy := range out.Transition.RequiredPolicies {
		matched := false
		for _, planned := range req.Decisions {
			if api.OperationWorkflowPolicyMatches(policy, planned.Milestone, req.Workflow, req.InstanceID, planned.Decision) && plannedMilestonePresent(req.Milestones, planned.Milestone) {
				matched = true
			}
		}
		if !matched {
			out.MissingPolicies = append(out.MissingPolicies, policy)
		}
	}
	if len(out.MissingPolicies) > 0 {
		reason("policy_evidence_required")
	}
	for _, requirement := range out.Transition.RequiredInvariants {
		why := "missing"
		for _, planned := range req.Invariants {
			if planned.Milestone != requirement.Milestone {
				continue
			}
			why = api.OperationWorkflowInvariantEvidenceReason(requirement, planned.Milestone, req.Workflow, req.InstanceID, req.FromState, req.Operation, planned.Invariant)
			if !plannedMilestonePresent(req.Milestones, planned.Milestone) {
				why = "missing"
			}
			break
		}
		if why != "" {
			out.UnmetInvariants = append(out.UnmetInvariants, api.OperationWorkflowUnmetInvariant{Requirement: requirement, Reason: why})
		}
	}
	if len(out.UnmetInvariants) > 0 {
		reason("invariant_evidence_required")
	}
	for _, requirement := range out.Transition.RequiredEffects {
		why := "missing"
		for _, planned := range req.Effects {
			if planned.Milestone != requirement.Milestone {
				continue
			}
			why = api.OperationWorkflowEffectEvidenceReason(requirement, planned.Milestone, req.Workflow, req.InstanceID, req.ToState, req.Operation, planned.Effect)
			if !plannedMilestonePresent(req.Milestones, planned.Milestone) {
				why = "missing"
			}
			break
		}
		if why != "" {
			out.UnmetEffects = append(out.UnmetEffects, api.OperationWorkflowUnmetEffect{Requirement: requirement, Reason: why})
		}
	}
	if len(out.UnmetEffects) > 0 {
		reason("effect_evidence_required")
	}
	out.Ready = len(out.Reasons) == 0
	return out
}

func projectOperationWorkflowReadiness(page *api.OperationMilestonesResponse) {
	instance := page.WorkflowInstance
	if instance == nil {
		return
	}
	overview := &api.OperationWorkflowReadinessOverview{Items: make([]api.OperationWorkflowTransitionReadiness, 0)}
	if instance.State != nil && !instance.State.Terminal {
		for _, edge := range instance.AllowedTransitions {
			if edge.From != instance.State.State {
				continue
			}
			overview.TransitionCount++
			if len(overview.Items) < 100 {
				overview.Items = append(overview.Items, evaluateOperationWorkflowReadiness(instance, api.OperationWorkflowReadinessRequest{Operation: edge.Operation, FromState: edge.From, ToState: edge.To}))
			}
		}
	}
	overview.HasMore = overview.TransitionCount > len(overview.Items)
	instance.Readiness = overview
}

func checkOperationWorkflowReadiness(ctx context.Context, store OperationMilestoneStore, account, tenant, app string, req api.OperationWorkflowReadinessRequest, operator bool) (api.OperationWorkflowReadinessResponse, error) {
	if err := validateOperationWorkflowReadiness(req, operator); err != nil {
		return api.OperationWorkflowReadinessResponse{}, err
	}
	opts := api.OperationMilestoneListOptions{ReadinessOnly: true, AppID: app, Scope: req.Scope, TenantID: req.TenantID, SubjectType: req.Subject.Type, SubjectID: req.Subject.ID, Workflow: req.Workflow, WorkflowInstanceID: req.InstanceID, Limit: 1}
	var page api.OperationMilestonesResponse
	var err error
	if operator {
		page, err = store.ListAccountOperationMilestones(ctx, account, opts)
	} else {
		opts.AppID = req.AppID
		page, err = store.ListPlatformTenantOperationMilestones(ctx, account, tenant, opts)
	}
	if err != nil {
		return api.OperationWorkflowReadinessResponse{}, err
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	if page.WorkflowInstance != nil && page.WorkflowInstance.State != nil {
		state := page.WorkflowInstance.State
		state.Stale = operationWorkflowStateIsStale(at, *state)
		evaluateOperationWorkflowDeadline(at, state)
	}
	return api.OperationWorkflowReadinessResponse{Subject: req.Subject, Workflow: req.Workflow, InstanceID: req.InstanceID, EvaluatedAt: at, Readiness: evaluateOperationWorkflowReadiness(page.WorkflowInstance, req)}, nil
}

func (m *MemStore) CheckAccountWorkflowReadiness(ctx context.Context, account, app string, req api.OperationWorkflowReadinessRequest) (api.OperationWorkflowReadinessResponse, error) {
	return checkOperationWorkflowReadiness(ctx, m, account, req.TenantID, app, req, true)
}
func (m *MemStore) CheckPlatformTenantWorkflowReadiness(ctx context.Context, account, tenant string, req api.OperationWorkflowReadinessRequest) (api.OperationWorkflowReadinessResponse, error) {
	return checkOperationWorkflowReadiness(ctx, m, account, tenant, req.AppID, req, false)
}
func (s *PgStore) CheckAccountWorkflowReadiness(ctx context.Context, account, app string, req api.OperationWorkflowReadinessRequest) (api.OperationWorkflowReadinessResponse, error) {
	return checkOperationWorkflowReadiness(ctx, s, account, req.TenantID, app, req, true)
}
func (s *PgStore) CheckPlatformTenantWorkflowReadiness(ctx context.Context, account, tenant string, req api.OperationWorkflowReadinessRequest) (api.OperationWorkflowReadinessResponse, error) {
	return checkOperationWorkflowReadiness(ctx, s, account, tenant, req.AppID, req, false)
}

func plannedMilestonePresent(names []string, name string) bool {
	for _, v := range names {
		if v == name {
			return true
		}
	}
	return false
}
