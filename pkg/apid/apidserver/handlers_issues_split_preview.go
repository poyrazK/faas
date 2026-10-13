package apidserver

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) previewIssueSplit(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	st, ok := s.issueStore(w, acct)
	if !ok {
		return
	}
	if _, err := uuid.Parse(r.PathValue("issue_id")); err != nil {
		writeIssueError(w, state.ErrNotFound)
		return
	}
	var matcher api.IssueSplitMatcher
	if !decodeIssueBody(w, r, &matcher) {
		return
	}
	if err := matcher.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	out, err := st.PreviewIssueSplit(r.Context(), app.ID, r.PathValue("issue_id"), matcher)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func populateIssueDashboardSplitPreview(w http.ResponseWriter, r *http.Request, app state.App, st state.IssueStore, data *dashboardIssuesData) bool {
	if r.URL.Query().Get("split_preview") != "1" || data.Detail == nil {
		return true
	}
	matcher := api.IssueSplitMatcher{ExceptionType: r.URL.Query().Get("split_exception_type"), ErrorCode: r.URL.Query().Get("split_error_code"), Route: r.URL.Query().Get("split_route"), SourceKind: r.URL.Query().Get("split_source_kind")}
	data.SplitMatcher = matcher
	if err := matcher.Validate(); err != nil {
		data.SplitPreviewError = err.Error()
		return true
	}
	id := r.URL.Query().Get("split_original_issue_id")
	if id == "" {
		id = data.Detail.Issue.ID
	}
	// Only preview the displayed identity or one of its current original members.
	allowed := id == data.Detail.Issue.ID
	for _, member := range data.Detail.GroupMembers {
		allowed = allowed || id == member.ID
	}
	if !allowed {
		data.SplitPreviewError = "Choose an original identity from this issue group."
		return true
	}
	out, err := st.PreviewIssueSplit(r.Context(), app.ID, id, matcher)
	if err != nil {
		writeIssueError(w, err)
		return false
	}
	data.SplitPreviewCanApply = out.ApplicationStatus == "new-rule"
	data.SplitPreview = &out
	return true
}
