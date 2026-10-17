package apidserver

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuetrackers"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/state"
)

type nativeIssueCreationHost struct {
	nativeIssueTrackerHost
	links state.IssueTrackerLinkStore
}

func (h nativeIssueCreationHost) Attach(ctx context.Context, r issuetracker.Request, t issuetracker.Ticket) (api.IssueTrackerLink, error) {
	return h.links.PutIssueTrackerLink(ctx, h.app.ID, h.app.AccountID, r.IssueID, h.account.ID, api.PutIssueTrackerLinkRequest{URL: t.URL, Title: r.Title}, time.Now().UTC())
}
func (s *server) createNativeIssueTicket(w http.ResponseWriter, r *http.Request, acct state.Account) {
	links, app, ok := s.issueTrackerLinksAccess(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.NativeIssueTicketStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("native ticket receipts are unavailable"))
		return
	}
	var in api.CreateNativeIssueTicketRequest
	if !decodeIssueBody(w, r, &in) {
		return
	}
	provider := s.nativeTicketProvider
	if provider == nil {
		provider = issuetracker.NewClient(nil)
	}
	service := issuetrackers.CreationService{Host: nativeIssueCreationHost{nativeIssueTrackerHost: nativeIssueTrackerHost{server: s, app: app, account: acct, issueID: r.PathValue("issue_id")}, links: links}, Store: store, Provider: provider, AppSlug: app.Slug, IssueID: r.PathValue("issue_id")}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.IssueTrackerRequestTimeoutSeconds)*time.Second)
	defer cancel()
	out, err := service.Run(ctx, in)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
