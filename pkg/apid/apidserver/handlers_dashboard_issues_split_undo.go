package apidserver

import (
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type dashboardIssueSplitUndo struct {
	Rule    api.IssueSplitRule
	Source  api.Issue
	Branch  api.Issue
	CanUndo bool
	Reason  string
}

func populateIssueDashboardSplitUndo(w http.ResponseWriter, r *http.Request, app state.App, st state.IssueStore, data *dashboardIssuesData) bool {
	id := r.URL.Query().Get("undo_split_rule")
	if id == "" || data.Detail == nil {
		return true
	}
	var rule *api.IssueSplitRule
	for i := range data.Detail.SplitRules {
		if data.Detail.SplitRules[i].ID == id {
			rule = &data.Detail.SplitRules[i]
			break
		}
	}
	if rule == nil {
		writeIssueError(w, state.ErrNotFound)
		return false
	}
	now := time.Now().UTC()
	source, err := st.GetIssueDetail(r.Context(), app.ID, rule.SourceIssueID, now.Add(-api.IssueImpactSummaryWindow), now, state.IssueDetailCursors{})
	if err != nil {
		writeIssueError(w, err)
		return false
	}
	branch, err := st.GetIssueDetail(r.Context(), app.ID, rule.TargetIssueID, now.Add(-api.IssueImpactSummaryWindow), now, state.IssueDetailCursors{})
	if err != nil {
		writeIssueError(w, err)
		return false
	}
	out := &dashboardIssueSplitUndo{Rule: *rule, Source: source.Issue, Branch: branch.Issue, CanUndo: true}
	if source.Issue.ID != branch.Issue.ID && (branch.Issue.ID != rule.TargetIssueID || len(branch.GroupMembers) > 1) {
		out.CanUndo = false
		out.Reason = "The branch has other group links. Review those links before undoing this rule."
	}
	data.SplitUndo = out
	return true
}

// Named authenticated CSRF, MFA and write scope are verified by the shared wrapper.
func (s *server) dashboardIssueSplitUndoForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	st, ok := s.issueStore(w, acct)
	if !ok {
		return
	}
	if !validIssueSplitRulePath(r) || r.ParseForm() != nil {
		api.WriteProblem(w, api.ErrValidation("invalid split undo form"))
		return
	}
	in := api.IssueSplitRuleUndoRequest{CanonicalSourceIssueID: r.FormValue("canonical_source_issue_id")}
	if _, err := st.UndoIssueSplitRule(r.Context(), app.ID, r.PathValue("issue_id"), r.PathValue("rule_id"), acct.ID, in, time.Now().UTC()); err != nil {
		writeIssueError(w, err)
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/issues?issue="+url.QueryEscape(in.CanonicalSourceIssueID)+"&split_rule=updated", http.StatusSeeOther)
}
