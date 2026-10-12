package state

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func playbookStepReadiness(p api.OperationWorkflowPlaybookProgress, code string, items []api.OperationWorkflowPlaybookProgressItem, index int) api.OperationWorkflowPlaybookStepReadiness {
	result := api.OperationWorkflowPlaybookStepReadiness{Status: "ready", WaitingFor: []string{}}
	if index < 0 || index >= len(items) {
		result.Status = "waiting"
		return result
	}
	edges := map[string][]string{}
	for _, g := range p.Playbook.OutcomeGuidance {
		if g.OutcomeCode == code {
			for _, d := range g.FollowUpDependencies {
				edges[d.Label] = d.Prerequisites
			}
			break
		}
	}
	completed := map[string]bool{}
	for _, item := range items {
		completed[item.Label] = item.Status == "completed" && item.DependencyReviewRevision == 0
	}
	visiting := map[string]bool{}
	memo := map[string]bool{}
	var satisfied func(string) bool
	satisfied = func(label string) bool {
		if ready, exists := memo[label]; exists {
			return ready
		}
		if visiting[label] || !completed[label] {
			return false
		}
		visiting[label] = true
		ready := true
		for _, before := range edges[label] {
			if !satisfied(before) {
				ready = false
				break
			}
		}
		visiting[label] = false
		memo[label] = ready
		return ready
	}
	for _, label := range edges[items[index].Label] {
		if !satisfied(label) {
			result.WaitingFor = append(result.WaitingFor, label)
		}
	}
	if len(result.WaitingFor) > 0 {
		result.Status = "waiting"
		result.ReviewSuggested = items[index].Status == "completed"
	}
	result.ReviewSuggested = result.ReviewSuggested || items[index].DependencyReviewRevision > 0
	return result
}
func projectPlaybookStepReadiness(p *api.OperationWorkflowPlaybookProgress) {
	for f := range p.OutcomeFollowUps {
		follow := &p.OutcomeFollowUps[f]
		for i := range follow.Items {
			ready := playbookStepReadiness(*p, follow.OutcomeCode, follow.Items, i)
			follow.Items[i].Readiness = &ready
		}
	}
}
func activateReadyPlaybookStepDeadlines(p *api.OperationWorkflowPlaybookProgress, followIndex int, at time.Time) {
	follow := &p.OutcomeFollowUps[followIndex]
	for _, guidance := range p.Playbook.OutcomeGuidance {
		if guidance.OutcomeCode != follow.OutcomeCode {
			continue
		}
		for _, policy := range guidance.FollowUpAssignments {
			for i := range follow.Items {
				item := &follow.Items[i]
				if item.Label == policy.Label && item.DueAt == nil && playbookStepReadiness(*p, follow.OutcomeCode, follow.Items, i).Status == "ready" {
					due := at.Add(time.Duration(policy.CompleteAfterSeconds) * time.Second)
					item.DueAt = &due
				}
			}
		}
	}
}
