package apidserver

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuetrackers"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"time"
)

func (s *server) linearConnectionAccess(w http.ResponseWriter, r *http.Request, acct state.Account) (state.IssueTrackerLinearStore, state.App, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return nil, app, false
	}
	if app.AccountID != acct.ID {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Owner required", "only the app owner may manage the Linear connection"))
		return nil, app, false
	}
	store, ok := s.store.(state.IssueTrackerLinearStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Linear connections are unavailable"))
	}
	return store, app, ok
}
func (s *server) getIssueTrackerLinearConnection(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.linearConnectionAccess(w, r, acct)
	if !ok {
		return
	}
	out, err := store.GetIssueTrackerLinearConnection(r.Context(), app.ID, acct.ID)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out.Connection)
}
func (s *server) updateIssueTrackerLinearConnection(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.linearConnectionAccess(w, r, acct)
	if !ok {
		return
	}
	var in api.UpdateIssueTrackerLinearConnectionRequest
	if !decodeIssueBody(w, r, &in) {
		return
	}
	if in.Connected {
		if _, ok = s.issueStore(w, acct); !ok {
			return
		}
		if !acct.MayDeploy() {
			api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Account unavailable", "account cannot configure provider access"))
			return
		}
	}
	// The Linear credential is an account value (ADR 20261022111812); no
	// recipient leaves the connection unsealable, which configuration reports.
	recipients, _ := accountRecipients(hostSecretRecipient())
	verifier := s.nativeLinearVerifier
	if verifier == nil {
		verifier = issuetracker.NewClient(nil)
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.IssueTrackerRequestTimeoutSeconds)*time.Second)
	defer cancel()
	out, err := issuetrackers.ConfigureLinearConnection(ctx, store, verifier, recipients, acct.ID, app.ID, in)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	if s.audit != nil {
		s.audit.Emit(r.Context(), "issue.tracker_linear_connection_configured", &acct.ID, map[string]any{"app_id": app.ID, "connected": out.Connected, "revision": out.Revision})
	}
	writeJSON(w, http.StatusOK, out)
}
