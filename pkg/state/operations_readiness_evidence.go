package state

import (
	"slices"
	"sort"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// The existing declaration preview cap also bounds saved evidence inventories.
// Full fingerprints remain independent of this preview.
func readinessEvidence(row api.OperationWorkflowReadinessQueueEntry, instance *api.OperationWorkflowInstanceSnapshot, r api.OperationWorkflowTransitionReadiness) ([]api.OperationWorkflowReadinessEvidence, bool) {
	facts := []api.OperationWorkflowReadinessEvidence{}
	add := func(category, key, name, status, owner, operation, invalidation string, value any) {
		facts = append(facts, api.OperationWorkflowReadinessEvidence{Category: category, Key: readinessDigest([]string{category, key}), Name: name, Status: status, Owner: owner, OperationID: operation, InvalidationOperationID: invalidation, Fingerprint: readinessDigest(value)})
	}
	for _, b := range row.Blockers {
		add("blockers", b.Operation+"/"+b.Code, b.Operation+"/"+b.Code, "active", b.Owner, row.OperationID, "", b)
	}
	retained := func(group, path string, fact api.OperationWorkflowRetainedMilestone) {
		name := fact.Requirement.Operation + "/" + fact.Requirement.Milestone
		operation, invalidation := "", ""
		if fact.Evidence != nil {
			operation = fact.Evidence.OperationID
		}
		if fact.Invalidation != nil {
			invalidation = fact.Invalidation.Evidence.OperationID
		}
		add("prerequisites", group+"/"+path+"/"+name, group+"/"+path+"/"+name, fact.Status, "", operation, invalidation, readinessRetainedEvidence(fact))
	}
	for _, fact := range r.RetainedMilestones {
		retained("required", "", fact)
	}
	for _, group := range r.RetainedMilestoneGroups {
		for _, fact := range group.Alternatives {
			retained(group.ID, "any-of", fact)
		}
		for _, path := range group.Paths {
			for _, fact := range path.Requirements {
				retained(group.ID, path.ID, fact)
			}
		}
	}

	// Apply the evaluator's declared dependency selection, retaining both met and unmet states.
	for _, dependency := range instance.State.DependsOn {
		if r.Transition.RequiredDependencyWorkflows != nil && !slices.Contains(*r.Transition.RequiredDependencyWorkflows, dependency.Workflow) {
			continue
		}
		status, operation := "unknown", ""
		var state any
		for _, related := range instance.RelatedWorkflows {
			if related.Dependency != dependency {
				continue
			}
			status = related.Status
			if related.State != nil {
				operation = related.State.OperationID
				state = []any{operation, related.State.ReportID, related.State.OutcomeCode, related.State.Terminal}
			}
			break
		}
		add("prerequisites", "dependency/"+readinessDigest(dependency), "dependency/"+dependency.Workflow, status, "", operation, "", []any{dependency, status, state})
	}
	for _, v := range instance.ReadinessVerifications {
		if v.Resolution.Operation != row.Transition.Operation {
			continue
		}
		operation := v.Resolution.VerificationOperationID
		if operation == "" {
			operation = v.ResolutionOperationID
		}
		add("verification", resolutionVerificationKey(v.Resolution, v.ResolutionOperationID), v.Resolution.Operation+"/"+v.Resolution.Code, v.Status, v.Resolution.VerificationOwner, operation, "", v)
	}
	sort.Slice(facts, func(i, j int) bool { return facts[i].Key < facts[j].Key })
	complete := len(facts) <= api.OperationMilestoneDeclarationsMax
	if !complete {
		facts = facts[:api.OperationMilestoneDeclarationsMax]
	}
	return facts, complete
}

func validReadinessEvidence(row api.OperationWorkflowReadinessSnapshotEntry) bool {
	if len(row.Evidence) > api.OperationMilestoneDeclarationsMax || row.EvidenceComplete && row.Evidence == nil {
		return false
	}
	seen := map[string]bool{}
	for _, f := range row.Evidence {
		if !slices.Contains([]string{"blockers", "prerequisites", "verification"}, f.Category) || !validReadinessDigest(f.Key) || !validReadinessDigest(f.Fingerprint) || seen[f.Key] || len(f.Name) == 0 || len(f.Name) > api.OperationWorkflowInstanceIDMaxBytes || len(f.Owner) > api.OperationWorkflowInstanceIDMaxBytes || !slices.Contains([]string{"active", "required", "verified", "invalidated", "expired", "awaiting_verification", "unknown", "waiting", "terminal", "satisfied", "outcome_mismatch"}, f.Status) {
			return false
		}
		for _, id := range []string{f.OperationID, f.InvalidationOperationID} {
			if id != "" {
				if _, err := uuid.Parse(id); err != nil {
					return false
				}
			}
		}
		seen[f.Key] = true
	}
	return true
}

func compareReadinessEvidence(before, after api.OperationWorkflowReadinessSnapshotEntry) ([]api.OperationWorkflowReadinessEvidenceChange, bool) {
	out := []api.OperationWorkflowReadinessEvidenceChange{}
	complete := before.EvidenceComplete && after.EvidenceComplete
	old := map[string]api.OperationWorkflowReadinessEvidence{}
	current := map[string]api.OperationWorkflowReadinessEvidence{}
	for _, f := range before.Evidence {
		old[f.Key] = f
	}
	for _, f := range after.Evidence {
		current[f.Key] = f
		prior, found := old[f.Key]
		if !found {
			kind := "observed"
			if complete {
				kind = "added"
			}
			out = append(out, api.OperationWorkflowReadinessEvidenceChange{Kind: kind, After: &f})
		} else if prior.Fingerprint != f.Fingerprint {
			kind := "changed"
			if prior.Owner != f.Owner {
				kind = "reassigned"
			}
			if f.Category == "prerequisites" && prior.Status != f.Status {
				kind = f.Status
			}
			if f.Category == "verification" && prior.Status == "awaiting_verification" && f.Status == "verified" {
				kind = "fulfilled"
			}
			out = append(out, api.OperationWorkflowReadinessEvidenceChange{Kind: kind, Before: &prior, After: &f})
		}
	}
	for _, f := range before.Evidence {
		if _, found := current[f.Key]; found {
			continue
		}
		kind := "unavailable"
		// A vanished proof is never claimed fulfilled; only a retained verified observation can establish that.
		if complete && f.Category == "blockers" {
			kind = "cleared"
		}
		out = append(out, api.OperationWorkflowReadinessEvidenceChange{Kind: kind, Before: &f})
	}
	sort.Slice(out, func(i, j int) bool {
		key := func(d api.OperationWorkflowReadinessEvidenceChange) string {
			if d.After != nil {
				return d.After.Key
			}
			return d.Before.Key
		}
		return key(out[i]) < key(out[j])
	})
	return out, complete
}
