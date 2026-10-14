package apidserver

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) undoIssueSplitRule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	st, ok := s.issueStore(w, acct)
	if !ok {
		return
	}
	if !validIssueSplitRulePath(r) {
		writeIssueError(w, state.ErrNotFound)
		return
	}
	var in api.IssueSplitRuleUndoRequest
	if !decodeIssueBody(w, r, &in) {
		return
	}
	if _, err := uuid.Parse(in.CanonicalSourceIssueID); err != nil {
		api.WriteProblem(w, api.ErrValidation("canonical_source_issue_id must identify the reviewed destination"))
		return
	}
	rule, err := st.UndoIssueSplitRule(r.Context(), app.ID, r.PathValue("issue_id"), r.PathValue("rule_id"), acct.ID, in, time.Now().UTC())
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}
