package state

import (
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Use the already scoped case projection: no broad evidence queries or per-item database reads.
func projectWorkflowPlaybookEvidence(out *api.OperationWorkflowCase, source workflowCasePlaybookContext) {
	at := out.EvidenceCheckedAt
	for i := range out.PlaybookProgress {
		p := &out.PlaybookProgress[i]
		for j := range p.Items {
			item := &p.Items[j]
			item.EvidenceCheck = nil
			for _, requirement := range p.Playbook.EvidenceRequirements {
				if requirement.Label != item.Label {
					continue
				}
				check := checkWorkflowPlaybookEvidence(out, source, *p, requirement, at)
				check.ReviewSuggested = workflowPlaybookProgressActive(*p) && item.Status == "completed" && check.Status != "satisfied"
				item.EvidenceCheck = &check
				break
			}
		}
	}
	out.Limitations = append(out.Limitations, "Checklist evidence checks observe application-declared requirements from the pinned rule. They do not revalidate human attachments, prove external correctness, change reviews, or establish readiness or business success. Refresh the case for current observations.")
}

func checkWorkflowPlaybookEvidence(out *api.OperationWorkflowCase, source workflowCasePlaybookContext, p api.OperationWorkflowPlaybookProgress, r api.OperationWorkflowPlaybookEvidenceRequirement, at time.Time) api.OperationWorkflowPlaybookEvidenceCheck {
	c := api.OperationWorkflowPlaybookEvidenceCheck{Requirement: r, Status: "unavailable", Reason: "evidence_unavailable", CheckedAt: at}
	instance := out.Instance
	if instance == nil || instance.State == nil {
		return c
	}
	current := instance.State
	c.StateRevision, c.ReportingOperationID = current.Revision, current.OperationID
	if current.Stale || operationWorkflowStateIsStale(at, *current) {
		c.Status, c.Reason = "stale", "state_report_stale"
		return c
	}
	if instance.ContractVersion != p.ContractVersion || instance.ContractVersion != effectiveWorkflowContractVersion(current.ContractVersion) {
		c.Reason = "contract_changed"
		return c
	}
	if source.DefinitionID == "" {
		c.Reason = "definition_unavailable"
		return c
	}
	canonical, err := api.CanonicalOperationWorkflowPlaybooks([]api.OperationWorkflowPlaybook{p.Playbook})
	if err != nil {
		c.Reason = "pinned_guidance_invalid"
		return c
	}
	same := false
	for _, rule := range source.Rules {
		if rule.ID == p.Playbook.ID && readinessDigest(rule) == readinessDigest(canonical[0]) {
			same = true
			break
		}
	}
	if !same {
		c.Reason = "guidance_changed"
		return c
	}
	switch r.Kind {
	case "milestone":
		declared := false
		for _, step := range instance.Steps {
			if step.Operation != r.Operation || step.Milestone != r.Milestone {
				continue
			}
			declared = true
			if step.ObservedInRetention && step.LatestRetainedMilestone != nil {
				c.Status, c.Reason, c.Evidence = "satisfied", "retained_milestone", step.LatestRetainedMilestone
				return c
			}
		}
		if declared {
			c.Status, c.Reason = "missing", "milestone_not_retained"
		} else {
			c.Reason = "milestone_not_declared"
		}
	case "prerequisite":
		if instance.Readiness == nil {
			c.Reason = "transition_unavailable"
			return c
		}
		for _, edge := range instance.Readiness.Items {
			if edge.Transition.Operation != r.Operation || edge.Transition.From != r.FromState || edge.Transition.To != r.ToState {
				continue
			}
			if !edge.Declared || edge.ContractVersion != p.ContractVersion || edge.StateRevision != current.Revision {
				c.Reason = "transition_unavailable"
				return c
			}
			facts := edge.RetainedMilestones
			if r.GroupID != "" {
				facts = nil
				for _, group := range edge.RetainedMilestoneGroups {
					if group.ID != r.GroupID {
						continue
					}
					if r.PathID == "" {
						facts = group.Alternatives
					} else {
						for _, path := range group.Paths {
							if path.ID == r.PathID {
								facts = path.Requirements
								break
							}
						}
					}
					break
				}
			}
			for _, fact := range facts {
				if fact.Requirement.Operation != r.PrerequisiteOperation || fact.Requirement.Milestone != r.Milestone {
					continue
				}
				f := fact
				c.Prerequisite = &f
				c.Evidence = f.Evidence
			}
			if c.Prerequisite == nil {
				c.Reason = "prerequisite_not_declared"
				return c
			}
			switch c.Prerequisite.Status {
			case "verified":
				if c.Evidence == nil {
					c.Reason = "prerequisite_evidence_unavailable"
					return c
				}
				c.Status, c.Reason = "satisfied", "retained_prerequisite"
			case "expired":
				c.Status, c.Reason = "stale", "prerequisite_expired"
			case "invalidated":
				c.Status, c.Reason = "invalidated", "prerequisite_invalidated"
			case "awaiting_verification":
				c.Status, c.Reason = "missing", "prerequisite_not_retained"
			default:
				c.Reason = "prerequisite_unavailable"
			}
			return c
		}
		c.Reason = "transition_not_evaluated"
	case "verification":
		// Bind to obligations in this exact current contract report, never an older-version resolution.
		if int64(len(instance.ReadinessVerifications)) != instance.ResolutionVerificationCount {
			c.Reason = "verification_coverage_incomplete"
			return c
		}
		found := false
		for _, resolution := range current.BlockerResolutions {
			if resolution.Code != r.BlockerCode || resolution.Operation != r.Operation || resolution.VerificationMilestoneID == "" {
				continue
			}
			found = true
			key := resolutionVerificationKey(resolution, current.OperationID)
			index := slices.IndexFunc(instance.ReadinessVerifications, func(v api.OperationWorkflowResolutionVerification) bool {
				return resolutionVerificationKey(v.Resolution, v.ResolutionOperationID) == key
			})
			if index < 0 {
				c.Reason = "verification_unavailable"
				return c
			}
			v := instance.ReadinessVerifications[index]
			if v.ResolutionRevision != current.Revision || !sameOperationHistoryIdentity(v.ResolutionOperationID, current.OperationID) || v.ResolutionReportID != current.ReportID {
				c.Reason = "verification_report_mismatch"
				return c
			}
			if v.Status != "verified" {
				c.Status, c.Reason = "missing", "awaiting_verification"
				return c
			}
		}
		if found {
			c.Status, c.Reason = "satisfied", "current_resolutions_verified"
		} else {
			c.Reason = "current_resolution_not_reported"
		}
	}
	return c
}
