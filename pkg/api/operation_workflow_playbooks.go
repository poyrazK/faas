package api

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

var workflowPlaybookMatchCode = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func CanonicalOperationWorkflowPlaybooks(input []OperationWorkflowPlaybook) ([]OperationWorkflowPlaybook, error) {
	if len(input) > OperationWorkflowPlaybooksMax {
		return nil, fmt.Errorf("workflow playbooks exceed their limit")
	}
	if len(input) == 0 {
		return nil, nil
	}
	out := append([]OperationWorkflowPlaybook(nil), input...)
	text := func(v string, max int, required bool) bool {
		return len(v) <= max && utf8.ValidString(v) && strings.TrimSpace(v) == v && (!required || v != "") && strings.IndexFunc(v, unicode.IsControl) < 0
	}
	link := func(v string) bool {
		if v == "" {
			return true
		}
		if !text(v, OperationWorkflowPlaybookURLMaxBytes, true) {
			return false
		}
		u, err := url.Parse(v)
		return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Opaque == ""
	}
	seen := map[string]bool{}
	for i := range out {
		r := &out[i]
		if ValidateOperationWorkflowName(r.ID) != nil || seen[r.ID] || r.Version < 1 || r.Version > OperationWorkflowContractVersionMax || !slices.Contains([]string{"blocker", "missing_milestone", "missing_dependency", "pending_verification", "requirement"}, r.MatchKind) || !workflowPlaybookMatchCode.MatchString(r.MatchCode) || !text(r.Summary, OperationWorkflowPlaybookSummaryMaxBytes, true) || !text(r.Owner, OperationWorkflowReadinessFollowUpOwnerMaxBytes, false) || len(r.RequiredEvidence) > OperationWorkflowPlaybookEvidenceMax || !link(r.RunbookURL) || !link(r.ActionURL) {
			return nil, fmt.Errorf("workflow playbook is invalid or duplicated")
		}
		seen[r.ID] = true
		r.RequiredEvidence = append([]string(nil), r.RequiredEvidence...)
		slices.Sort(r.RequiredEvidence)
		for j, e := range r.RequiredEvidence {
			if !text(e, OperationWorkflowPlaybookEvidenceMaxBytes, true) || j > 0 && r.RequiredEvidence[j-1] == e {
				return nil, fmt.Errorf("workflow playbook evidence is invalid or duplicated")
			}
		}
	}
	slices.SortFunc(out, func(a, b OperationWorkflowPlaybook) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}
