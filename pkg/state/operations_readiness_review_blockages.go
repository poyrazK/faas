package state

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type reviewResponseBlockageUpdate struct {
	ResponseUpdatedAt time.Time
	Blockers          []string
	CheckedAt         time.Time
}

// Separate timeout and cursor keep case observation from starving other watch types.
func evaluateReadinessReviewBlockages(ctx context.Context, store readinessSubscriptionBackend, r *readinessSubscriptionRecord) ([]readinessWatchUpdate, error) {
	page, err := store.readReadinessInbox(ctx, r.Account, r.Tenant, r.Subscription.AppID, r.Subscription.Scope, r.Subscription.ID, 0, api.OperationWorkflowReadinessInboxMax, r.Operator)
	if err != nil {
		return nil, err
	}
	return evaluateReviewResponseBlockages(ctx, store, r, page.Items)
}

// Read at most five retained active plans per pass, independently of check-in deadlines.
func evaluateReviewResponseBlockages(ctx context.Context, store readinessSubscriptionBackend, r *readinessSubscriptionRecord, items []api.OperationWorkflowReadinessNotification) ([]readinessWatchUpdate, error) {
	backend, ok := store.(workflowCaseBackend)
	if !ok {
		return nil, fmt.Errorf("playbook case backend unavailable")
	}
	candidates := []api.OperationWorkflowReadinessNotification{}
	retained := map[string]bool{}
	for _, n := range items {
		if n.Kind == "business_impact_changed" && n.ReviewResponse != nil && n.ReviewResponse.Status == "active" {
			candidates = append(candidates, n)
			retained[n.ID] = true
		}
	}
	for id := range r.ReviewBlockages {
		if !retained[id] {
			delete(r.ReviewBlockages, id)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].Sequence > r.ReviewBlockageCursor, candidates[j].Sequence > r.ReviewBlockageCursor
		if a != b {
			return a
		}
		return candidates[i].Sequence < candidates[j].Sequence
	})
	if len(candidates) > api.OperationWorkflowReadinessEscalationResponsesPerPassMax {
		candidates = candidates[:api.OperationWorkflowReadinessEscalationResponsesPerPassMax]
	}
	updates := []readinessWatchUpdate{}
	var firstErr error
	for _, n := range candidates {
		r.ReviewBlockageCursor = n.Sequence
		blockers := []string{}
		failed := false
		cache := map[string]api.OperationWorkflowCase{}
		for i, link := range n.ReviewResponse.Reviews {
			tenant := r.Tenant
			if r.Operator {
				tenant = link.PlatformTenantID
			}
			opts := api.OperationWorkflowCoordinationTimelineOptions{AppID: r.Subscription.AppID, Scope: r.Subscription.Scope, SubjectType: link.Subject.Type, SubjectID: link.Subject.ID, Workflow: link.Workflow, InstanceID: link.InstanceID}
			if r.Operator {
				opts.TenantID = tenant
			}
			identity := playbookEvidenceWatchIdentity(*r, api.OperationWorkflowPlaybookEvidenceWatch{PlatformTenantID: link.PlatformTenantID, Subject: link.Subject, Workflow: link.Workflow, InstanceID: link.InstanceID})
			key := identity.key() + ":" + link.ProgressID
			overview, exists := cache[key]
			if !exists {
				var err error
				overview, err = readPlaybookEvidenceWatchCase(ctx, backend, r.Account, tenant, opts, link.ProgressID, r.Operator)
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
					failed = true
					break
				}
				cache[key] = overview
			}
			prefix := fmt.Sprintf("Linked review %d: ", i+1)
			found := false
			for _, p := range overview.PlaybookProgress {
				if p.ID != link.ProgressID {
					continue
				}
				found = true
				if !workflowPlaybookProgressActive(p) {
					blockers = append(blockers, prefix+"source progress retired or replaced")
					continue
				}
				if p.Revision < link.DependencyReviewRevision {
					blockers = append(blockers, prefix+"source revision unavailable")
					continue
				}
				stepFound := false
				for _, follow := range p.OutcomeFollowUps {
					if follow.OutcomeCode != link.OutcomeCode || link.ItemIndex < 0 || link.ItemIndex >= len(follow.Items) {
						continue
					}
					stepFound = true
					ready := playbookStepReadiness(p, follow.OutcomeCode, follow.Items, link.ItemIndex)
					for _, label := range reviewResponsePrerequisiteBlockers(p, follow.OutcomeCode, follow.Items, link.ItemIndex, ready.WaitingFor) {
						blockers = append(blockers, prefix+"waiting for prerequisite "+label)
					}
				}
				if !stepFound {
					blockers = append(blockers, prefix+"linked review step unavailable")
				}
				// Only declared live requirements are observed; human attachments are not revalidated.
				for _, item := range p.Items {
					if item.EvidenceCheck != nil && item.EvidenceCheck.Status != "satisfied" {
						blockers = append(blockers, prefix+"checklist evidence "+item.Label+": "+item.EvidenceCheck.Status+" ("+item.EvidenceCheck.Reason+")")
					}
				}
			}
			if !found {
				blockers = append(blockers, prefix+"source progress unavailable")
			}
		}
		if failed {
			continue
		}
		slices.Sort(blockers)
		blockers = slices.Compact(blockers)
		updates = append(updates, readinessWatchUpdate{SourceID: n.ID, Sequence: n.Sequence, ReviewBlockage: &reviewResponseBlockageUpdate{ResponseUpdatedAt: n.ReviewResponse.UpdatedAt, Blockers: blockers, CheckedAt: time.Now().UTC().Truncate(time.Microsecond)}})
	}
	return updates, firstErr
}

// Publish and advance the baseline together under the subscription lease and source lock.
func applyReviewResponseBlockage(r *readinessSubscriptionRecord, n api.OperationWorkflowReadinessNotification, u readinessWatchUpdate) (api.OperationWorkflowReadinessNotification, []api.OperationWorkflowReadinessNotification, bool) {
	observation := u.ReviewBlockage
	if n.ID != u.SourceID || n.Kind != "business_impact_changed" || n.BusinessImpact == nil || n.ReviewResponse == nil || n.ReviewResponse.Status != "active" || !n.ReviewResponse.UpdatedAt.Equal(observation.ResponseUpdatedAt) {
		return n, nil, false
	}
	if r.ReviewBlockages == nil {
		r.ReviewBlockages = map[string][]string{}
	}
	previous, initialized := r.ReviewBlockages[n.ID]
	r.ReviewBlockages[n.ID] = slices.Clone(observation.Blockers)
	added := []string{}
	if initialized {
		for _, blocker := range observation.Blockers {
			if !slices.Contains(previous, blocker) {
				added = append(added, blocker)
			}
		}
	}
	if len(added) == 0 {
		return n, nil, true
	}
	notice := readinessSubscriptionNotify(r, "review_response_blocked", "New blockers were observed for the active response plan. Review the linked sources and revise the plan before its check-in. These observations do not determine application business success.", nil)
	impact := *n.BusinessImpact
	impact.Response = nil
	notice.BusinessImpact = &impact
	notice.ReviewBlockage = &api.OperationWorkflowReviewResponseBlockageEvent{SourceNotificationID: n.ID, SourceSequence: n.Sequence, CheckedAt: observation.CheckedAt, AddedBlockers: added}
	notice.Coordination = &api.OperationWorkflowReadinessCoordination{Revision: 1, Status: "unread", Owner: n.ReviewResponse.Owner, UpdatedAt: observation.CheckedAt, UpdatedBy: "system:review-response-blockage"}
	return n, []api.OperationWorkflowReadinessNotification{notice}, true
}

// Include reopened transitive ancestors even when the immediate prerequisite was already blocked.
func reviewResponsePrerequisiteBlockers(p api.OperationWorkflowPlaybookProgress, code string, items []api.OperationWorkflowPlaybookProgressItem, index int, direct []string) []string {
	blockers := slices.Clone(direct)
	edges := map[string][]string{}
	for _, guidance := range p.Playbook.OutcomeGuidance {
		if guidance.OutcomeCode == code {
			for _, dependency := range guidance.FollowUpDependencies {
				edges[dependency.Label] = dependency.Prerequisites
			}
			break
		}
	}
	completed := map[string]bool{}
	for _, item := range items {
		completed[item.Label] = item.Status == "completed" && item.DependencyReviewRevision == 0
	}
	visited := map[string]bool{}
	var visit func(string)
	visit = func(label string) {
		if visited[label] {
			return
		}
		visited[label] = true
		if !completed[label] {
			blockers = append(blockers, label)
		}
		for _, prerequisite := range edges[label] {
			visit(prerequisite)
		}
	}
	for _, prerequisite := range edges[items[index].Label] {
		visit(prerequisite)
	}
	slices.Sort(blockers)
	return slices.Compact(blockers)
}
