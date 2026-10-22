package state

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type reviewResponseBlockageUpdate struct {
	Health                *api.OperationWorkflowReviewResponseHealth
	Failed                bool
	ResponseUpdatedAt     time.Time
	Blockers              []string
	VerifiedClearPrefixes []string
	CheckedAt             time.Time
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
		clearPrefixes := []string{}
		failed := false
		health := &api.OperationWorkflowReviewResponseHealth{Sources: []api.OperationWorkflowReviewResponseSourceHealth{}, RemainingBlockers: []string{}, RecoveredBlockers: []string{}}
		cache := map[string]api.OperationWorkflowCase{}
		for i, link := range n.ReviewResponse.Reviews {
			source := api.OperationWorkflowReviewResponseSourceHealth{ReviewIndex: i, Status: "observed", Blockers: []string{}}
			start := len(blockers)
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
					source.Status = "unavailable"
					source.Explanation = "Source could not be read; previous observations are retained."
					source.CheckedAt = time.Now().UTC().Truncate(time.Microsecond)
					health.Sources = append(health.Sources, source)
					continue
				}
				cache[key] = overview
			}
			if overview.Instance != nil && overview.Instance.State != nil {
				source.StateRevision = overview.Instance.State.Revision
			}
			prefix := fmt.Sprintf("Linked review %d: ", i+1)
			found := false
			for _, p := range overview.PlaybookProgress {
				if p.ID != link.ProgressID {
					continue
				}
				found = true
				source.ProgressRevision = p.Revision
				if !workflowPlaybookProgressActive(p) {
					source.Status = "unavailable"
					source.Explanation = "Source progress retired or replaced."
					blockers = append(blockers, prefix+"source progress retired or replaced")
					continue
				}
				if p.Revision < link.DependencyReviewRevision {
					source.Status = "unavailable"
					source.Explanation = "Retained source revision precedes the linked review."
					blockers = append(blockers, prefix+"source revision unavailable")
					continue
				}
				stepFound := false
				for _, follow := range p.OutcomeFollowUps {
					if follow.OutcomeCode != link.OutcomeCode || link.ItemIndex < 0 || link.ItemIndex >= len(follow.Items) {
						continue
					}
					stepFound = true
					source.ReviewRequired = follow.Items[link.ItemIndex].DependencyReviewRevision > 0
					clearPrefixes = append(clearPrefixes, prefix+"waiting for prerequisite ", prefix+"source progress unavailable", prefix+"source revision unavailable", prefix+"linked review step unavailable")
					ready := playbookStepReadiness(p, follow.OutcomeCode, follow.Items, link.ItemIndex)
					for _, label := range reviewResponsePrerequisiteBlockers(p, follow.OutcomeCode, follow.Items, link.ItemIndex, ready.WaitingFor) {
						blockers = append(blockers, prefix+"waiting for prerequisite "+label)
					}
				}
				if !stepFound {
					source.Status = "unavailable"
					source.Explanation = "Linked review step unavailable."
					blockers = append(blockers, prefix+"linked review step unavailable")
				}
				// Only declared live requirements are observed; human attachments are not revalidated.
				for _, item := range p.Items {
					if item.EvidenceCheck != nil && item.EvidenceCheck.Status == "satisfied" {
						clearPrefixes = append(clearPrefixes, prefix+"checklist evidence "+item.Label+": ")
					}
					if item.EvidenceCheck != nil && item.EvidenceCheck.Status != "satisfied" {
						if item.EvidenceCheck.Status == "unavailable" || item.EvidenceCheck.Status == "stale" {
							source.Status = "unavailable"
							source.Explanation = "Declared checklist evidence is unavailable or stale."
						}
						blockers = append(blockers, prefix+"checklist evidence "+item.Label+": "+item.EvidenceCheck.Status+" ("+item.EvidenceCheck.Reason+")")
					}
				}
			}
			if !found {
				source.Status = "unavailable"
				source.Explanation = "Source progress unavailable."
				blockers = append(blockers, prefix+"source progress unavailable")
			}
			source.CheckedAt = time.Now().UTC().Truncate(time.Microsecond)
			source.Blockers = slices.Clone(blockers[start:])
			if source.Blockers == nil {
				source.Blockers = []string{}
			}
			if source.Status == "observed" {
				checked := source.CheckedAt
				source.LastSuccessfulCheckAt = &checked
			}
			health.Sources = append(health.Sources, source)
		}
		slices.Sort(blockers)
		blockers = slices.Compact(blockers)
		updates = append(updates, readinessWatchUpdate{SourceID: n.ID, Sequence: n.Sequence, ReviewBlockage: &reviewResponseBlockageUpdate{Health: health, Failed: failed, ResponseUpdatedAt: n.ReviewResponse.UpdatedAt, Blockers: blockers, VerifiedClearPrefixes: clearPrefixes, CheckedAt: time.Now().UTC().Truncate(time.Microsecond)}})
	}
	return updates, firstErr
}

// Publish and advance the baseline together under the subscription lease and source lock.
func applyReviewResponseBlockage(r *readinessSubscriptionRecord, n api.OperationWorkflowReadinessNotification, u readinessWatchUpdate) (api.OperationWorkflowReadinessNotification, []api.OperationWorkflowReadinessNotification, bool) {
	observation := u.ReviewBlockage
	if n.ID != u.SourceID || n.Kind != "business_impact_changed" || n.BusinessImpact == nil || n.ReviewResponse == nil || n.ReviewResponse.Status != "active" || !n.ReviewResponse.UpdatedAt.Equal(observation.ResponseUpdatedAt) {
		return n, nil, false
	}
	if observation.Failed {
		n = withReviewResponseHealth(n, observation, r.ReviewBlockages[n.ID], nil)
		return applyReviewResponseDecisionReview(r, n)
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

	cleared := []string{}
	for _, blocker := range previous {
		if slices.Contains(observation.Blockers, blocker) {
			continue
		}
		if slices.ContainsFunc(observation.VerifiedClearPrefixes, func(prefix string) bool {
			if !strings.HasPrefix(blocker, prefix) {
				return false
			}
			// Match the complete checklist label, including labels containing ": ".
			return !strings.HasSuffix(prefix, ": ") || strings.LastIndex(blocker, ": ") == len(prefix)-2
		}) {
			cleared = append(cleared, blocker)
		}
	}
	// Preserve unverified disappearances so a later positive observation can report recovery.
	for _, blocker := range previous {
		if !slices.Contains(observation.Blockers, blocker) && !slices.Contains(cleared, blocker) {
			r.ReviewBlockages[n.ID] = append(r.ReviewBlockages[n.ID], blocker)
		}
	}
	slices.Sort(r.ReviewBlockages[n.ID])
	slices.Sort(cleared)
	n = withReviewResponseHealth(n, observation, r.ReviewBlockages[n.ID], cleared)
	notices := []api.OperationWorkflowReadinessNotification{}
	publish := func(kind, explanation string, added, cleared []string) {
		notice := readinessSubscriptionNotify(r, kind, explanation, nil)
		impact := *n.BusinessImpact
		impact.Response = nil
		notice.BusinessImpact = &impact
		notice.ReviewBlockage = &api.OperationWorkflowReviewResponseBlockageEvent{SourceNotificationID: n.ID, SourceSequence: n.Sequence, CheckedAt: observation.CheckedAt, AddedBlockers: added, ClearedBlockers: cleared}
		notice.Coordination = &api.OperationWorkflowReadinessCoordination{Revision: 1, Status: "unread", Owner: n.ReviewResponse.Owner, UpdatedAt: observation.CheckedAt, UpdatedBy: "system:review-response-blockage"}
		notices = append(notices, notice)
	}
	if len(added) > 0 {
		publish("review_response_blocked", "New blockers were observed for the active response plan. Review the linked sources and revise the plan before its check-in. These observations do not determine application business success.", added, nil)
	}
	if len(cleared) > 0 {
		publish("review_response_recovered", "The listed response-plan blockers have cleared in a fresh source observation; other blockers may remain. Inspect the linked reviews before resuming work; the response still requires an explicit decision to resolve.", []string{}, cleared)
	}
	updated, decisionNotices, _ := applyReviewResponseDecisionReview(r, n)
	return updated, append(notices, decisionNotices...), true
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

// Health updates preserve the human response revision and never satisfy a review obligation.
func withReviewResponseHealth(n api.OperationWorkflowReadinessNotification, observation *reviewResponseBlockageUpdate, remaining, recovered []string) api.OperationWorkflowReadinessNotification {
	health := *observation.Health
	health.CheckedAt = observation.CheckedAt
	health.RemainingBlockers = append([]string{}, remaining...)
	if observation.Failed {
		health.RemainingBlockers = append(health.RemainingBlockers, observation.Blockers...)
		slices.Sort(health.RemainingBlockers)
		health.RemainingBlockers = slices.Compact(health.RemainingBlockers)
	} else {
		checked := observation.CheckedAt
		health.LastCompleteCheckAt = &checked
	}
	health.RecoveredBlockers = []string{}
	if old := n.ReviewResponse.Health; old != nil {
		if observation.Failed {
			health.LastCompleteCheckAt = old.LastCompleteCheckAt
			health.RemainingBlockers = append(health.RemainingBlockers, old.RemainingBlockers...)
			slices.Sort(health.RemainingBlockers)
			health.RemainingBlockers = slices.Compact(health.RemainingBlockers)
		}
		health.RecoveredAt = old.RecoveredAt
		for _, blocker := range old.RecoveredBlockers {
			if !slices.ContainsFunc(health.RemainingBlockers, func(current string) bool {
				return reviewResponseBlockerIdentity(current) == reviewResponseBlockerIdentity(blocker)
			}) {
				health.RecoveredBlockers = append(health.RecoveredBlockers, blocker)
			}
		}
		for i := range health.Sources {
			if health.Sources[i].LastSuccessfulCheckAt != nil {
				continue
			}
			for _, previous := range old.Sources {
				if previous.ReviewIndex == health.Sources[i].ReviewIndex {
					health.Sources[i].LastSuccessfulCheckAt = previous.LastSuccessfulCheckAt
					break
				}
			}
		}
	}
	if len(recovered) > 0 {
		checked := observation.CheckedAt
		health.RecoveredAt = &checked
		health.RecoveredBlockers = slices.Clone(recovered)
	}
	health.Status = "clear"
	if len(health.RemainingBlockers) > 0 {
		health.Status = "blocked"
	}
	for _, source := range health.Sources {
		if source.ReviewRequired && health.Status == "clear" {
			health.Status = "review_required"
		}
		if source.Status == "unavailable" {
			health.Status = "unavailable"
		}
	}
	health.Fingerprint = reviewResponseHealthFingerprint(health)
	response := *n.ReviewResponse
	response.Health = &health
	n.ReviewResponse = &response
	return n
}

// Evidence remains the same blocker when its failure status or reason changes.
func reviewResponseBlockerIdentity(blocker string) string {
	prefixEnd := strings.Index(blocker, ": ")
	if prefixEnd >= 0 && strings.HasPrefix(blocker[prefixEnd+2:], "checklist evidence ") {
		return blocker[:strings.LastIndex(blocker, ": ")]
	}
	return blocker
}

// Exclude polling timestamps and historical recovery display from the decision fence.
func reviewResponseHealthFingerprint(health api.OperationWorkflowReviewResponseHealth) string {
	sources := slices.Clone(health.Sources)
	for i := range sources {
		sources[i].CheckedAt = time.Time{}
		sources[i].LastSuccessfulCheckAt = nil
	}
	return readinessDigest(struct {
		Status   string
		Blockers []string
		Sources  []api.OperationWorkflowReviewResponseSourceHealth
	}{health.Status, health.RemainingBlockers, sources})
}
