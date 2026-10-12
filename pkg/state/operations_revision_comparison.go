package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func compareWorkflowRevisions(previous *api.OperationWorkflowStateHistoryEntry, current api.OperationWorkflowStateHistoryEntry) *api.OperationWorkflowRevisionComparison {
	out := &api.OperationWorkflowRevisionComparison{ChangeReason: api.CloneOperationWorkflowChangeReason(current.ChangeReason), Status: "baseline_unavailable", Summary: "The preceding revision is unavailable on this page; changes cannot be determined.", Changes: make([]api.OperationWorkflowRevisionChange, 0), Evidence: make([]api.OperationWorkflowRevisionEvidence, 0)}
	defer func() {
		if out.ConditionComparison == nil {
			out.ConditionComparison = &api.OperationWorkflowConditionComparison{Status: out.Status, Changes: []api.OperationWorkflowConditionChange{}}
		}
	}()
	if previous == nil || previous.Workflow != current.Workflow || previous.InstanceID != current.InstanceID || previous.PlatformTenantID != current.PlatformTenantID {
		return out
	}
	out.PreviousReportID, out.PreviousOperationID, out.PreviousRevision = previous.ID, previous.OperationID, previous.Revision
	if current.Revision <= previous.Revision {
		out.Status = "ambiguous_revision"
		out.Summary = "Reports do not provide a unique increasing revision baseline; changes cannot be determined."
		return out
	}
	if current.Revision-previous.Revision != 1 {
		out.Status = "revision_gap"
		out.Summary = "Intervening revisions are missing from retained history; changes cannot be determined."
		return out
	}
	add := func(kind, key, before, after, explanation string) {
		out.Changes = append(out.Changes, api.OperationWorkflowRevisionChange{Kind: kind, Key: key, Before: before, After: after, Explanation: explanation})
	}
	if effectiveWorkflowContractVersion(previous.ContractVersion) != effectiveWorkflowContractVersion(current.ContractVersion) {
		out.Status = "contract_changed"
		out.Summary = "The pinned contract version changed; business snapshots are not compared across contracts."
		add("contract_changed", "contract_version", strconv.Itoa(effectiveWorkflowContractVersion(previous.ContractVersion)), strconv.Itoa(effectiveWorkflowContractVersion(current.ContractVersion)), "Application reported a different workflow contract version.")
		return out
	}
	out.Status = "compared"
	out.ConditionComparison = compareWorkflowDecisionConditions(previous, current)
	if previous.State != current.State {
		add("state_changed", "state", previous.State, current.State, "Application changed the reported workflow state.")
	}
	beforeBlockers, afterBlockers := map[string]api.OperationWorkflowBlocker{}, map[string]api.OperationWorkflowBlocker{}
	for _, b := range previous.Blockers {
		beforeBlockers[b.Operation+"/"+b.Code] = b
	}
	for _, b := range current.Blockers {
		afterBlockers[b.Operation+"/"+b.Code] = b
	}
	keys := map[string]bool{}
	for k := range beforeBlockers {
		keys[k] = true
	}
	for k := range afterBlockers {
		keys[k] = true
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	for _, k := range ordered {
		before, b := beforeBlockers[k]
		after, a := afterBlockers[k]
		switch {
		case !b:
			add("blocker_added", k, "", revisionSnapshot(after), "Application added blocker "+k+".")
		case !a:
			add("blocker_removed", k, revisionSnapshot(before), "", "Application stopped reporting blocker "+k+"; this does not prove business resolution.")
		case before != after:
			add("blocker_updated", k, revisionSnapshot(before), revisionSnapshot(after), "Application updated blocker "+k+".")
		}
	}
	beforePaths := map[string]api.OperationWorkflowPathSelection{}
	for _, p := range previous.SelectedPaths {
		beforePaths[p.GroupID] = p
	}
	// Selections describe this business transition. Metadata updates do not clear a prior choice.
	for _, after := range current.SelectedPaths {
		before, exists := beforePaths[after.GroupID]
		if !exists {
			add("path_selected", after.GroupID, "", after.PathID, "Application reported path "+after.PathID+" for group "+after.GroupID+".")
		} else if before.PathID != after.PathID {
			add("path_changed", after.GroupID, before.PathID, after.PathID, "Application reported a different business path for group "+after.GroupID+".")
		}
		if (!exists && after.DecisionMilestoneID != "") || exists && (before.DecisionCode != after.DecisionCode || before.DecisionRuleID != after.DecisionRuleID || before.DecisionRuleVersion != after.DecisionRuleVersion || before.DecisionDescription != after.DecisionDescription) {
			beforeDecision := ""
			if exists && before.DecisionMilestoneID != "" {
				beforeDecision = revisionDecisionSnapshot(before)
			}
			afterDecision := ""
			if after.DecisionMilestoneID != "" {
				afterDecision = revisionDecisionSnapshot(after)
			}
			add("decision_changed", after.GroupID, beforeDecision, afterDecision, "Application reported changed decision reasoning for group "+after.GroupID+".")
		}
	}
	seen := map[string]int{}
	fact := func(kind, operation, id, name string) {
		if id == "" || operation == "" {
			return
		}
		key := operation + "/" + id
		if index, exists := seen[key]; exists {
			if out.Evidence[index].Milestone == "" {
				out.Evidence[index].Milestone = name
			}
			return
		}
		seen[key] = len(out.Evidence)
		out.Evidence = append(out.Evidence, api.OperationWorkflowRevisionEvidence{Kind: kind, OperationID: operation, MilestoneID: id, Milestone: name})
	}
	for _, row := range []api.OperationWorkflowStateHistoryEntry{*previous, current} {
		if row.ChangeReason != nil {
			fact("decision", row.OperationID, row.ChangeReason.MilestoneID, "")
		}
		for _, p := range row.SelectedPaths {
			fact("decision", row.OperationID, p.DecisionMilestoneID, "")
		}
		for _, e := range row.EvidenceMilestones {
			fact("milestone", row.OperationID, e.ID, e.Name)
		}
	}
	for _, r := range current.BlockerResolutions {
		operation := r.VerificationOperationID
		if operation == "" {
			operation = current.OperationID
		}
		fact("verification_expected", operation, r.VerificationMilestoneID, r.VerificationMilestoneName)
	}
	sort.Slice(out.Evidence, func(i, j int) bool {
		a, b := out.Evidence[i], out.Evidence[j]
		if a.OperationID != b.OperationID {
			return a.OperationID < b.OperationID
		}
		return a.MilestoneID < b.MilestoneID
	})
	out.Summary = fmt.Sprintf("Compared revisions %d and %d: %d reported state, blocker, path, or decision changes; %d reported condition changes.", previous.Revision, current.Revision, len(out.Changes), len(out.ConditionComparison.Changes))
	if out.ConditionComparison.Truncated {
		out.Summary += " Additional condition changes were omitted."
	}
	if len(out.Changes) == 0 && len(out.ConditionComparison.Changes) == 0 {
		out.Summary = "No reported state, blocker, path, decision, or condition changes between these revisions."
	}
	return out
}

func revisionSnapshot(value any) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	return strings.TrimSuffix(buffer.String(), "\n")
}
func revisionDecisionSnapshot(p api.OperationWorkflowPathSelection) string {
	return revisionSnapshot(struct {
		Code        string `json:"code"`
		Description string `json:"description"`
		RuleID      string `json:"rule_id"`
		RuleVersion string `json:"rule_version"`
	}{p.DecisionCode, p.DecisionDescription, p.DecisionRuleID, p.DecisionRuleVersion})
}
