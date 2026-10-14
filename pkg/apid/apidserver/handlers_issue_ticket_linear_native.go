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

type nativeLinearCreationHost struct {
	server  *server
	app     state.App
	account state.Account
	issueID string
}

func (h nativeLinearCreationHost) Prepare(ctx context.Context) (issuetrackers.Prepared, error) {
	app, err := h.server.store.AppByID(ctx, h.app.ID)
	if err != nil {
		return issuetrackers.Prepared{}, err
	}
	account, err := h.server.store.AccountByID(ctx, h.account.ID)
	if err != nil {
		return issuetrackers.Prepared{}, err
	}
	if app.AccountID != account.ID || app.DeletedAt != nil || app.Status == state.AppDeleted || !account.MayDeploy() || !account.Plan.IssueLimits().Enabled {
		return issuetrackers.Prepared{}, issuetrackers.LinearConnectionRequired()
	}
	st, ok := h.server.store.(state.IssueStore)
	if !ok {
		return issuetrackers.Prepared{}, api.ErrCapacity("issues unavailable")
	}
	now := time.Now().UTC()
	detail, err := st.GetIssueDetail(ctx, app.ID, h.issueID, now.Add(-time.Hour), now, state.IssueDetailCursors{})
	if err != nil {
		return issuetrackers.Prepared{}, err
	}
	store, ok := h.server.store.(state.IssueTrackerLinearStore)
	if !ok {
		return issuetrackers.Prepared{}, api.ErrCapacity("Linear connections unavailable")
	}
	record, err := store.GetIssueTrackerLinearConnection(ctx, app.ID, account.ID)
	if err != nil {
		return issuetrackers.Prepared{}, err
	}
	if !record.Connection.Connected || record.Connection.Revision <= 0 {
		return issuetrackers.Prepared{}, issuetrackers.LinearConnectionRequired()
	}
	return issuetrackers.Prepared{Issue: detail.Issue, GroupFingerprint: issuetrackers.GroupScopeFingerprint(detail), Connection: issuetrackers.Connection{AccountID: account.ID, Linear: linearBoundConnection(record)}}, nil
}
func linearBoundConnection(r state.IssueTrackerLinearRecord) *issuetrackers.LinearBoundConnection {
	return &issuetrackers.LinearBoundConnection{Revision: r.Connection.Revision, Scope: issuetrackers.LinearCredentialScope{AccountID: r.AccountID, AppID: r.AppID, Identity: issuetracker.LinearIdentity{WorkspaceID: r.Connection.WorkspaceID, TeamID: r.Connection.TeamID, ViewerID: r.Connection.ViewerID}}}
}
func (h nativeLinearCreationHost) Token(ctx context.Context, c issuetrackers.Connection) (string, error) {
	store, ok := h.server.store.(state.IssueTrackerLinearStore)
	if !ok || c.Linear == nil || mfaIdentities == nil {
		return "", api.ErrCapacity("Linear credentials unavailable")
	}
	r, err := store.GetIssueTrackerLinearConnection(ctx, h.app.ID, h.account.ID)
	if err != nil || !r.Connection.Connected || *linearBoundConnection(r) != *c.Linear {
		return "", issuetrackers.Changed()
	}
	token, err := issuetrackers.OpenLinearCredential(mfaIdentities(), c.Linear.Scope, r.SealedCredential)
	if err != nil {
		return "", api.ErrCapacity("Linear credentials unavailable")
	}
	verifier := h.server.nativeLinearVerifier
	if verifier == nil {
		verifier = issuetracker.NewClient(nil)
	}
	identity, err := verifier.VerifyLinearConnection(ctx, token, c.Linear.Scope.Identity.TeamID)
	if err != nil || identity != c.Linear.Scope.Identity {
		return "", issuetrackers.LinearConnectionRequired()
	}
	return token, nil
}
func (h nativeLinearCreationHost) Attach(ctx context.Context, r issuetracker.Request, t issuetracker.Ticket) (api.IssueTrackerLink, error) {
	st, ok := h.server.store.(state.IssueTrackerLinkStore)
	if !ok {
		return api.IssueTrackerLink{}, api.ErrCapacity("tracker references unavailable")
	}
	return st.PutIssueTrackerLink(ctx, h.app.ID, h.app.AccountID, r.IssueID, h.account.ID, api.PutIssueTrackerLinkRequest{URL: t.URL, Title: r.Title}, time.Now().UTC())
}
func (s *server) createNativeLinearIssueTicket(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, app, ok := s.issueTrackerLinksAccess(w, r, acct)
	if !ok {
		return
	}
	if app.AccountID != acct.ID {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Owner required", "only the app owner may use the stored Linear credential"))
		return
	}
	store, ok := s.store.(state.NativeIssueTicketStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("native ticket receipts unavailable"))
		return
	}
	var in api.CreateNativeLinearIssueTicketRequest
	if !decodeIssueBody(w, r, &in) {
		return
	}
	provider := s.nativeTicketProvider
	if provider == nil {
		provider = issuetracker.NewClient(nil)
	}
	service := issuetrackers.CreationService{Host: nativeLinearCreationHost{server: s, app: app, account: acct, issueID: r.PathValue("issue_id")}, Store: store, Provider: provider, AppSlug: app.Slug, IssueID: r.PathValue("issue_id")}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.IssueTrackerRequestTimeoutSeconds)*time.Second)
	defer cancel()
	out, err := service.RunLinear(ctx, in)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
