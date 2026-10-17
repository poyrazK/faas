package apidserver

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuetrackers"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) issueTrackerAutoSyncAccess(w http.ResponseWriter, r *http.Request, acct state.Account) (state.IssueTrackerAutoSyncStore, state.App, bool) {
	_, app, ok := s.issueTrackerLinksAccess(w, r, acct)
	if !ok {
		return nil, app, false
	}
	id, err := uuid.Parse(r.PathValue("link_id"))
	if err != nil || id == uuid.Nil {
		api.WriteProblem(w, api.ErrValidation("link_id must be a nonzero UUID"))
		return nil, app, false
	}
	r.SetPathValue("link_id", id.String())
	store, ok := s.store.(state.IssueTrackerAutoSyncStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("automatic tracker sync is unavailable"))
	}
	return store, app, ok
}
func (s *server) getIssueTrackerAutoSync(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.issueTrackerAutoSyncAccess(w, r, acct)
	if !ok {
		return
	}
	out, err := store.GetIssueTrackerAutoSync(r.Context(), app.ID, app.AccountID, r.PathValue("issue_id"), r.PathValue("link_id"))
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out.Policy)
}
func (s *server) updateIssueTrackerAutoSync(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.issueTrackerAutoSyncAccess(w, r, acct)
	if !ok {
		return
	}
	if acct.ID != app.AccountID {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Owner required", "only the app owner may configure automatic tracker sync"))
		return
	}
	var in api.UpdateIssueTrackerAutoSyncRequest
	if !decodeIssueBody(w, r, &in) {
		return
	}
	if _, err := api.NormalizeIssueTrackerSyncDirection(in.Direction); err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	if in.ExpectedRevision < 0 {
		api.WriteProblem(w, api.ErrValidation("expected_revision must be nonnegative"))
		return
	}
	out, err := s.configureIssueTrackerAutoSync(r, acct, app, store, in)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	if s.audit != nil {
		s.audit.Emit(r.Context(), "issue.tracker_auto_sync_configured", &acct.ID, map[string]any{"app_id": app.ID, "link_id": out.LinkID, "enabled": out.Enabled, "revision": out.Revision})
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) configureIssueTrackerAutoSync(r *http.Request, acct state.Account, app state.App, store state.IssueTrackerAutoSyncStore, in api.UpdateIssueTrackerAutoSyncRequest) (api.IssueTrackerAutoSync, error) {
	if !in.Enabled {
		out, err := store.ConfigureIssueTrackerAutoSync(r.Context(), app.ID, app.AccountID, r.PathValue("issue_id"), state.IssueTrackerAutoSyncRecord{Policy: api.IssueTrackerAutoSync{LinkID: r.PathValue("link_id")}}, in.ExpectedRevision)
		return out.Policy, err
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.IssueTrackerRequestTimeoutSeconds)*time.Second)
	defer cancel()
	if in.ExpectedLinearStatus != nil {
		provider := s.nativeTrackerProvider
		if provider == nil {
			provider = issuetracker.NewClient(nil)
		}
		service := issuetrackers.LinearService{Host: nativeLinearCreationHost{server: s, app: app, account: acct, issueID: r.PathValue("issue_id"), linkID: r.PathValue("link_id")}, Provider: provider}
		return service.EnableAutoSync(ctx, store, r.PathValue("issue_id"), in)
	}
	service := s.issueTrackerServiceFor(app, acct, r.PathValue("issue_id"), r.PathValue("link_id"))
	return service.EnableAutoSync(ctx, store, r.PathValue("issue_id"), in)
}
