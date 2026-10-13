package apidserver

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) createIssueSplitRule(w http.ResponseWriter, r *http.Request, acct state.Account) {
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
	rule, err := st.CreateIssueSplitRule(r.Context(), app.ID, r.PathValue("issue_id"), acct.ID, matcher, acct.Plan.IssueLimits(), time.Now().UTC())
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (s *server) updateIssueSplitRule(w http.ResponseWriter, r *http.Request, acct state.Account) {
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
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeIssueBody(w, r, &in) {
		return
	}
	if in.Enabled == nil {
		api.WriteProblem(w, api.ErrValidation("enabled is required and must be a boolean"))
		return
	}
	rule, err := st.SetIssueSplitRuleEnabled(r.Context(), app.ID, r.PathValue("issue_id"), r.PathValue("rule_id"), acct.ID, *in.Enabled, time.Now().UTC())
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func validIssueSplitRulePath(r *http.Request) bool {
	_, sourceErr := uuid.Parse(r.PathValue("issue_id"))
	_, ruleErr := uuid.Parse(r.PathValue("rule_id"))
	return sourceErr == nil && ruleErr == nil
}
