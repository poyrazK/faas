package faas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/poyrazK/faas/sdk/go/internal/api"
	"strings"
	"time"
)

// CustomerOperationReadinessError carries unmet requirements without committing business writes.
type CustomerOperationReadinessError struct {
	Response OperationWorkflowReadinessResponse
}

func (e *CustomerOperationReadinessError) Error() string {
	return "faas: workflow transition readiness requirements are unmet"
}

// CustomerOperationPlannedMilestone is actual evidence queued after the guard passes.
type CustomerOperationPlannedMilestone struct {
	Name    string
	Payload any
}

// GuardedWorkflowTransition checks retained reports, then queues the transition and
// actual milestone evidence. Call before writes, after locking and checking business
// rows. The checker must authenticate as this transaction's customer. Any error
// prevents commit even if the callback ignores it. This is not a remote row lock.
func (tx *CustomerOperationTransaction) GuardedWorkflowTransition(ctx context.Context, request OperationWorkflowReadinessRequest, milestones []CustomerOperationPlannedMilestone, check func(context.Context, OperationWorkflowReadinessRequest) (OperationWorkflowReadinessResponse, error)) (response OperationWorkflowReadinessResponse, err error) {
	if tx == nil || !tx.open {
		return response, errors.New("faas: readiness guards require an open transaction callback")
	}
	defer func() {
		if err != nil && tx.guardErr == nil {
			tx.guardErr = err
		}
	}()
	if tx.guardErr != nil {
		return response, tx.guardErr
	}
	if check == nil || ctx == nil || request.AppID != tx.input.appID || request.TenantID != "" || request.StateRevision <= 0 || request.ContractVersion <= 0 {
		return response, errors.New("faas: readiness guard requires a customer checker, matching app, and positive locked revision and contract version")
	}
	// Stage actual evidence through the existing validators without mutating the queue.
	staged := *tx
	staged.milestones = append([]OperationMilestoneRequest(nil), tx.milestones...)
	staged.workflowStates = append([]OperationWorkflowStateReport(nil), tx.workflowStates...)
	request.Milestones = nil
	request.Decisions = nil
	request.Invariants = nil
	request.Effects = nil
	seen := map[string]bool{}
	for _, fact := range milestones {
		raw, encodeErr := json.Marshal(fact.Payload)
		if encodeErr != nil {
			return response, encodeErr
		}
		effect, effectErr := api.ParseOperationBusinessEffect(raw)
		if effectErr != nil {
			return response, effectErr
		}
		if effect != nil {
			request.Effects = append(request.Effects, api.OperationWorkflowPlannedEffect{Milestone: fact.Name, Effect: *effect})
		}
		invariant, invariantErr := api.ParseOperationBusinessInvariant(raw)
		if invariantErr != nil {
			return response, invariantErr
		}
		if invariant != nil {
			request.Invariants = append(request.Invariants, api.OperationWorkflowPlannedInvariant{Milestone: fact.Name, Invariant: *invariant})
		}
		decision, parseErr := api.ParseOperationBusinessDecision(raw)
		if parseErr != nil {
			return response, parseErr
		}
		if decision != nil {
			request.Decisions = append(request.Decisions, api.OperationWorkflowPlannedDecision{Milestone: fact.Name, Decision: *decision})
		}
		if err = staged.Milestone(fact.Name, fact.Payload); err != nil {
			return response, err
		}
		if !seen[fact.Name] {
			request.Milestones = append(request.Milestones, fact.Name)
			seen[fact.Name] = true
		}
	}
	if err = staged.WorkflowTransition(request.Workflow, request.InstanceID, request.FromState, request.ToState); err != nil {
		return response, err
	}
	response, err = check(ctx, request)
	if err != nil {
		return response, err
	}
	r := response.Readiness
	for i := len(tx.workflowStates) - 1; i >= 0; i-- {
		pending := tx.workflowStates[i]
		if pending.Workflow != request.Workflow || pending.InstanceID != request.InstanceID || !pending.BlockersOnly {
			continue
		}
		for _, blocker := range pending.Blockers {
			if blocker.Operation != request.Operation || !strings.HasPrefix(blocker.Code, api.OperationBusinessInvariantBlockerPrefix) {
				continue
			}
			exists := false
			for _, prior := range r.Blockers {
				if prior.Code == blocker.Code && prior.Operation == blocker.Operation {
					exists = true
				}
			}
			if !exists {
				r.Blockers = append(r.Blockers, blocker)
			}
			exists = false
			for _, prior := range r.InvariantBlockers {
				if prior.Code == blocker.Code && prior.Operation == blocker.Operation {
					exists = true
				}
			}
			if !exists {
				r.InvariantBlockers = append(r.InvariantBlockers, blocker)
			}
			blocked := false
			for _, reason := range r.Reasons {
				if reason == "application_blocked" {
					blocked = true
				}
			}
			if !blocked {
				r.Reasons = append(r.Reasons, "application_blocked")
			}
			r.Ready = false
		}
		break
	}
	response.Readiness = r
	if response.Subject != request.Subject || response.Workflow != request.Workflow || response.InstanceID != request.InstanceID || r.Transition.Operation != request.Operation || r.Transition.From != request.FromState || r.Transition.To != request.ToState {
		return response, fmt.Errorf("faas: readiness response does not match proposed transition")
	}
	for _, requirement := range r.Transition.RequiredRetainedMilestones {
		verified := false
		for _, evidence := range r.RetainedMilestones {
			if evidence.Requirement == requirement && retainedEvidenceUsable(evidence, r.Transition, response.EvaluatedAt) {
				verified = true
				break
			}
		}
		if !verified {
			return response, &CustomerOperationReadinessError{Response: response}
		}
	}
	if !retainedGroupsUsable(r, response.EvaluatedAt) {
		return response, &CustomerOperationReadinessError{Response: response}
	}
	for _, evidence := range r.RetainedMilestones {
		if !retainedEvidenceUsable(evidence, r.Transition, response.EvaluatedAt) {
			return response, &CustomerOperationReadinessError{Response: response}
		}
	}
	if !r.Ready || !r.Declared || r.StateRevision != request.StateRevision || r.ContractVersion != request.ContractVersion || len(r.Reasons)+len(r.Blockers)+len(r.UnmetDependencies)+len(r.MissingMilestones)+len(r.MissingPolicies)+len(r.MissingDependencyWorkflows)+len(r.UnmetInvariants)+len(r.UnmetEffects) != 0 {
		return response, &CustomerOperationReadinessError{Response: response}
	}
	tx.milestones, tx.workflowStates = staged.milestones, staged.workflowStates
	return response, nil
}

func usableRetainedMilestone(e OperationWorkflowRetainedMilestone) bool {
	if e.Status != "verified" || e.Evidence == nil || e.Invalidation != nil {
		return false
	}
	f := e.Freshness
	return f == nil || f.MaxAgeSeconds > 0 && !f.EvaluatedAt.IsZero() && f.ExpiresAt != nil && f.ExpiresAt.After(f.EvaluatedAt) && f.AgeSeconds != nil && *f.AgeSeconds >= 0 && f.RemainingSeconds != nil && *f.RemainingSeconds > 0 && *f.RemainingSeconds <= f.MaxAgeSeconds
}

func retainedEvidenceUsable(e OperationWorkflowRetainedMilestone, edge OperationWorkflowInstanceTransition, at time.Time) bool {
	if !usableRetainedMilestone(e) {
		return false
	}
	for _, rule := range edge.RetainedMilestoneFreshness {
		if rule.Prerequisite == e.Requirement && (e.Freshness == nil || e.Freshness.MaxAgeSeconds != rule.MaxAgeSeconds || !e.Freshness.EvaluatedAt.Equal(at)) {
			return false
		}
	}
	return true
}

func retainedGroupsUsable(r OperationWorkflowTransitionReadiness, at time.Time) bool {
	if len(r.RetainedMilestoneGroups) != len(r.Transition.RetainedMilestoneGroups) {
		return false
	}
	for _, expected := range r.Transition.RetainedMilestoneGroups {
		matches := 0
		for _, group := range r.RetainedMilestoneGroups {
			if group.ID != expected.ID {
				continue
			}
			matches++
			if !group.Satisfied || !retainedGroupUsable(expected, group, r.Transition, at) {
				return false
			}
		}
		if matches != 1 {
			return false
		}
	}
	return true
}

func retainedFactsUsable(expected []OperationWorkflowRetainedMilestoneRequirement, facts []OperationWorkflowRetainedMilestone, edge OperationWorkflowInstanceTransition, at time.Time, all bool) (complete, usable bool) {
	if len(expected) == 0 || len(facts) != len(expected) {
		return false, false
	}
	usable = all
	for _, selector := range expected {
		matches := 0
		for _, fact := range facts {
			if fact.Requirement != selector {
				continue
			}
			matches++
			valid := retainedEvidenceUsable(fact, edge, at)
			if all {
				usable = usable && valid
			} else {
				usable = usable || valid
			}
		}
		if matches != 1 {
			return false, false
		}
	}
	return true, usable
}

func retainedGroupUsable(expected OperationWorkflowRetainedMilestoneGroup, group OperationWorkflowRetainedMilestoneGroupResult, edge OperationWorkflowInstanceTransition, at time.Time) bool {
	if len(expected.Paths) == 0 {
		if len(expected.AnyOf) < 2 || len(group.Paths) != 0 {
			return false
		}
		complete, usable := retainedFactsUsable(expected.AnyOf, group.Alternatives, edge, at, false)
		return complete && usable
	}
	if len(expected.AnyOf) != 0 || len(expected.Paths) < 2 || len(group.Alternatives) != 0 || len(group.Paths) != len(expected.Paths) {
		return false
	}
	satisfied := false
	for _, expectedPath := range expected.Paths {
		matches := 0
		for _, path := range group.Paths {
			if path.ID != expectedPath.ID {
				continue
			}
			matches++
			complete, usable := retainedFactsUsable(expectedPath.AllOf, path.Requirements, edge, at, true)
			if !complete || path.Satisfied != usable {
				return false
			}
			satisfied = satisfied || usable
		}
		if matches != 1 {
			return false
		}
	}
	return satisfied
}
