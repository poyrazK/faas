package state

import (
	"github.com/onebox-faas/faas/pkg/api"
	"sort"
)

type impactSummaryKey struct{ Workflow, Kind, Code string }
type impactReviewKey struct {
	ProgressID, OutcomeCode string
	ItemIndex               int
	Revision                int64
}
type impactCaseKey struct{ Tenant, SubjectType, SubjectID, Instance string }
type impactReviewCount struct {
	Priority int
	Overdue  bool
}
type impactSummaryAccumulator struct {
	summary api.OperationWorkflowReadinessImpactSummary
	reviews map[impactReviewKey]impactReviewCount
	cases   map[impactCaseKey]bool
	owners  map[string]api.OperationWorkflowReadinessImpactOwner
}

// Summarize the same authorized, filtered retained candidate window as the queue.
// Delivery counts remain distinct from source obligations shared across subscriptions.
func readinessBusinessImpactSummary(rows []api.OperationWorkflowReadinessFollowUp) []api.OperationWorkflowReadinessImpactSummary {
	groups := map[impactSummaryKey]*impactSummaryAccumulator{}
	for _, row := range rows {
		impact := readinessFollowUpImpact(row)
		if impact == nil {
			continue
		}
		key := impactSummaryKey{row.Workflow, impact.Kind, impact.Code}
		group := groups[key]
		if group == nil {
			group = &impactSummaryAccumulator{summary: api.OperationWorkflowReadinessImpactSummary{Workflow: key.Workflow, Kind: key.Kind, Code: key.Code, Owners: []api.OperationWorkflowReadinessImpactOwner{}}, reviews: map[impactReviewKey]impactReviewCount{}, cases: map[impactCaseKey]bool{}, owners: map[string]api.OperationWorkflowReadinessImpactOwner{}}
			groups[key] = group
		}
		step := row.PlaybookStep
		reviewKey := impactReviewKey{step.ProgressID, step.OutcomeCode, step.ItemIndex, step.DependencyReview.DependencyReviewRevision}
		counted := group.reviews[reviewKey]
		counted.Priority = max(counted.Priority, readinessFollowUpImpactPriority(row))
		counted.Overdue = counted.Overdue || row.Overdue
		group.reviews[reviewKey] = counted
		caseKey := impactCaseKey{Tenant: row.PlatformTenantID, Instance: row.InstanceID}
		if row.Subject != nil {
			caseKey.SubjectType = row.Subject.Type
			caseKey.SubjectID = row.Subject.ID
		}
		group.cases[caseKey] = true
		group.summary.TaskCount++
		owner := group.owners[row.Coordination.Owner]
		owner.Owner = row.Coordination.Owner
		owner.TaskCount++
		if row.Overdue {
			owner.OverdueCount++
		}
		group.owners[owner.Owner] = owner
	}
	result := []api.OperationWorkflowReadinessImpactSummary{}
	for _, group := range groups {
		summary := group.summary
		summary.ReviewCount = len(group.reviews)
		summary.CaseCount = len(group.cases)
		for _, review := range group.reviews {
			switch review.Priority {
			case 3:
				summary.CriticalCount++
			case 2:
				summary.HighCount++
			}
			if review.Overdue {
				summary.OverdueCount++
				if review.Priority == 3 {
					summary.CriticalOverdueCount++
				}
			}
		}
		for _, owner := range group.owners {
			summary.Owners = append(summary.Owners, owner)
		}
		sort.Slice(summary.Owners, func(i, j int) bool {
			a, b := summary.Owners[i], summary.Owners[j]
			if a.OverdueCount != b.OverdueCount {
				return a.OverdueCount > b.OverdueCount
			}
			if a.TaskCount != b.TaskCount {
				return a.TaskCount > b.TaskCount
			}
			return a.Owner < b.Owner
		})
		result = append(result, summary)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.CriticalOverdueCount != b.CriticalOverdueCount {
			return a.CriticalOverdueCount > b.CriticalOverdueCount
		}
		if a.CriticalCount != b.CriticalCount {
			return a.CriticalCount > b.CriticalCount
		}
		if a.ReviewCount != b.ReviewCount {
			return a.ReviewCount > b.ReviewCount
		}
		if a.Workflow != b.Workflow {
			return a.Workflow < b.Workflow
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Code < b.Code
	})
	return result
}
