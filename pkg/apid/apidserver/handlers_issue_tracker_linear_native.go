package apidserver

import (
	"context"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuetrackers"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"time"
)

func (s *server) nativeLinearStatusService(w http.ResponseWriter, r *http.Request, acct state.Account) (issuetrackers.LinearService, bool) {
	_, app, ok := s.issueTrackerLinksAccess(w, r, acct)
	if !ok {
		return issuetrackers.LinearService{}, false
	}
	if acct.ID != app.AccountID {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Owner required", "only the app owner may use the stored Linear credential"))
		return issuetrackers.LinearService{}, false
	}
	link, err := uuid.Parse(r.PathValue("link_id"))
	if err != nil || link == uuid.Nil {
		api.WriteProblem(w, api.ErrValidation("link_id must be a nonzero UUID"))
		return issuetrackers.LinearService{}, false
	}
	provider := s.nativeTrackerProvider
	if provider == nil {
		provider = issuetracker.NewClient(nil)
	}
	return issuetrackers.LinearService{Host: nativeLinearCreationHost{server: s, app: app, account: acct, issueID: r.PathValue("issue_id"), linkID: link.String()}, Provider: provider}, true
}
func (s *server) getNativeLinearIssueTrackerStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	service, ok := s.nativeLinearStatusService(w, r, acct)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.IssueTrackerRequestTimeoutSeconds)*time.Second)
	defer cancel()
	out, err := service.Read(ctx)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) updateNativeLinearIssueTrackerStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	service, ok := s.nativeLinearStatusService(w, r, acct)
	if !ok {
		return
	}
	var in api.UpdateNativeLinearIssueTrackerStatusRequest
	if !decodeIssueBody(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.IssueTrackerRequestTimeoutSeconds)*time.Second)
	defer cancel()
	out, err := service.Push(ctx, in)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
