package state

import (
	"slices"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

func observePlaybookDependencyStates(p api.OperationWorkflowPlaybookProgress) []api.OperationWorkflowPlaybookDependencyState {
	out := []api.OperationWorkflowPlaybookDependencyState{}
	for _, follow := range p.OutcomeFollowUps {
		for i, item := range follow.Items {
			out = append(out, api.OperationWorkflowPlaybookDependencyState{OutcomeCode: follow.OutcomeCode, ItemIndex: i, Label: item.Label, Status: item.Status, Owner: item.Owner, Readiness: playbookStepReadiness(p, follow.OutcomeCode, follow.Items, i)})
		}
	}
	return out
}

// Compare only successful retained observations. Unknown/new lists silently acquire a baseline.
func publishPlaybookDependencyRegressions(r *readinessSubscriptionRecord, before api.OperationWorkflowPlaybookEvidenceWatch, current *api.OperationWorkflowPlaybookEvidenceWatch, p api.OperationWorkflowPlaybookProgress) []api.OperationWorkflowReadinessNotification {
	prior := func(step api.OperationWorkflowPlaybookDependencyState) *api.OperationWorkflowPlaybookDependencyState {
		for _, old := range before.DependencyStates {
			if old.OutcomeCode == step.OutcomeCode && old.ItemIndex == step.ItemIndex && old.Label == step.Label {
				copy := old
				return &copy
			}
		}
		return nil
	}
	for i, step := range current.DependencyStates {
		if old := prior(step); old != nil {
			current.DependencyStates[i].Regression = old.Regression
		}
	}
	reopened := []api.OperationWorkflowPlaybookDependencyState{}
	for _, step := range current.DependencyStates {
		old := prior(step)
		if old != nil && old.Status == "completed" && step.Status != "completed" {
			reopened = append(reopened, step)
		}
	}
	groups := map[string][]api.OperationWorkflowPlaybookDependencyState{}
	causes := map[string][]api.OperationWorkflowPlaybookDependencyState{}
	for _, step := range current.DependencyStates {
		old := prior(step)
		if old == nil || step.Readiness.Status != "waiting" {
			continue
		}
		regression := old.Readiness.Status == "ready"
		for _, label := range step.Readiness.WaitingFor {
			if !slices.Contains(old.Readiness.WaitingFor, label) {
				regression = true
			}
		}
		roots := []api.OperationWorkflowPlaybookDependencyState{}
		for _, root := range reopened {
			if root.OutcomeCode == step.OutcomeCode && playbookStepDependsOn(p, step.OutcomeCode, step.Label, root.Label) {
				roots = append(roots, root)
				regression = true
			}
		}
		if !regression {
			continue
		}
		owner := step.Owner
		if owner == "" {
			owner = current.Owner
		}
		groups[owner] = append(groups[owner], step)
		for _, root := range roots {
			if !slices.ContainsFunc(causes[owner], func(old api.OperationWorkflowPlaybookDependencyState) bool {
				return old.OutcomeCode == root.OutcomeCode && old.ItemIndex == root.ItemIndex
			}) {
				causes[owner] = append(causes[owner], root)
			}
		}
	}
	owners := []string{}
	for owner := range groups {
		owners = append(owners, owner)
	}
	sort.Strings(owners)
	out := []api.OperationWorkflowReadinessNotification{}
	for _, owner := range owners {
		n := readinessSubscriptionNotify(r, "playbook_dependency_regressed", "Follow-up prerequisites regressed. Review affected downstream work; original reviews, assignments, completion and deadlines remain unchanged.", nil)
		n.PlaybookEvidence = &api.OperationWorkflowPlaybookEvidenceEvent{WatchID: current.ID, ProgressID: current.ProgressID, PlatformTenantID: current.PlatformTenantID, Subject: current.Subject, Workflow: current.Workflow, InstanceID: current.InstanceID, ContractVersion: current.ContractVersion, PlaybookID: current.PlaybookID, PlaybookVersion: current.PlaybookVersion, CheckedAt: current.EvaluatedAt, Changes: []api.OperationWorkflowPlaybookEvidenceChange{}}
		roots := causes[owner]
		if roots == nil {
			roots = []api.OperationWorkflowPlaybookDependencyState{}
		}
		n.DependencyRegression = &api.OperationWorkflowPlaybookDependencyRegression{CheckedAt: current.EvaluatedAt, ReopenedPrerequisites: roots, AffectedSteps: groups[owner]}
		n.Coordination = &api.OperationWorkflowReadinessCoordination{Revision: 1, Status: "unread", Owner: owner, UpdatedAt: n.CreatedAt, UpdatedBy: "system:playbook-dependency-watch"}
		ref := &api.OperationWorkflowReadinessOutcomeEscalationReference{NotificationID: n.ID, Sequence: n.Sequence, CreatedAt: n.CreatedAt}
		for i, step := range current.DependencyStates {
			if slices.ContainsFunc(groups[owner], func(affected api.OperationWorkflowPlaybookDependencyState) bool {
				return step.OutcomeCode == affected.OutcomeCode && step.ItemIndex == affected.ItemIndex
			}) {
				current.DependencyStates[i].Regression = ref
			}
		}
		out = append(out, n)
	}
	return out
}

func playbookStepDependsOn(p api.OperationWorkflowPlaybookProgress, code, label, prerequisite string) bool {
	edges := map[string][]string{}
	for _, g := range p.Playbook.OutcomeGuidance {
		if g.OutcomeCode == code {
			for _, d := range g.FollowUpDependencies {
				edges[d.Label] = d.Prerequisites
			}
			break
		}
	}
	seen := map[string]bool{}
	var visit func(string) bool
	visit = func(step string) bool {
		if seen[step] {
			return false
		}
		seen[step] = true
		for _, before := range edges[step] {
			if before == prerequisite || visit(before) {
				return true
			}
		}
		return false
	}
	return visit(label)
}

// A recovery belongs to a previously delivered regression, not initial prerequisite completion.
func publishPlaybookDependencyRecoveries(r *readinessSubscriptionRecord, before api.OperationWorkflowPlaybookEvidenceWatch, current *api.OperationWorkflowPlaybookEvidenceWatch) []api.OperationWorkflowReadinessNotification {
	groups := map[string][]api.OperationWorkflowPlaybookDependencyState{}
	owners := map[string]string{}
	refs := map[string]api.OperationWorkflowReadinessOutcomeEscalationReference{}
	for _, step := range current.DependencyStates {
		if step.Readiness.Status != "ready" || step.Regression == nil {
			continue
		}
		wasWaiting := slices.ContainsFunc(before.DependencyStates, func(old api.OperationWorkflowPlaybookDependencyState) bool {
			return old.OutcomeCode == step.OutcomeCode && old.ItemIndex == step.ItemIndex && old.Label == step.Label && old.Readiness.Status == "waiting"
		})
		if !wasWaiting {
			continue
		}
		owner := step.Owner
		if owner == "" {
			owner = current.Owner
		}
		key := owner + "\x1f" + step.Regression.NotificationID
		groups[key] = append(groups[key], step)
		owners[key] = owner
		refs[key] = *step.Regression
	}
	keys := []string{}
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := []api.OperationWorkflowReadinessNotification{}
	for _, key := range keys {
		n := readinessSubscriptionNotify(r, "playbook_dependency_recovered", "Previously regressed follow-up prerequisites are ready again. Review the linked regression and resume eligible work; existing reviews, assignments and deadlines are preserved.", nil)
		n.PlaybookEvidence = &api.OperationWorkflowPlaybookEvidenceEvent{WatchID: current.ID, ProgressID: current.ProgressID, PlatformTenantID: current.PlatformTenantID, Subject: current.Subject, Workflow: current.Workflow, InstanceID: current.InstanceID, ContractVersion: current.ContractVersion, PlaybookID: current.PlaybookID, PlaybookVersion: current.PlaybookVersion, CheckedAt: current.EvaluatedAt, Changes: []api.OperationWorkflowPlaybookEvidenceChange{}}
		n.DependencyRecovery = &api.OperationWorkflowPlaybookDependencyRecovery{CheckedAt: current.EvaluatedAt, Regression: refs[key], ReadySteps: groups[key]}
		n.Coordination = &api.OperationWorkflowReadinessCoordination{Revision: 1, Status: "unread", Owner: owners[key], UpdatedAt: n.CreatedAt, UpdatedBy: "system:playbook-dependency-watch"}
		out = append(out, n)
	}
	for i, step := range current.DependencyStates {
		if step.Readiness.Status == "ready" {
			current.DependencyStates[i].Regression = nil
		}
	}
	return out
}

func carryPlaybookDependencyRegressionLinks(current, old []api.OperationWorkflowPlaybookDependencyState) {
	for i, step := range current {
		if step.Readiness.Status != "waiting" {
			continue
		}
		for _, prior := range old {
			if prior.OutcomeCode == step.OutcomeCode && prior.ItemIndex == step.ItemIndex && prior.Label == step.Label && prior.Readiness.Status == "waiting" {
				current[i].Regression = prior.Regression
				break
			}
		}
	}
}
