package apidserver

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuetrackers"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/state"
)

type nativeIssueTrackerHost struct {
	server          *server
	app             state.App
	account         state.Account
	issueID, linkID string
}

func (h nativeIssueTrackerHost) Prepare(ctx context.Context) (issuetrackers.Prepared, error) {
	st, ok := h.server.store.(state.IssueStore)
	if !ok {
		return issuetrackers.Prepared{}, api.ErrCapacity("issues persistence is unavailable")
	}
	links, ok := h.server.store.(state.IssueTrackerLinkStore)
	if !ok {
		return issuetrackers.Prepared{}, api.ErrCapacity("tracker references are unavailable")
	}
	now := time.Now().UTC()
	detail, err := st.GetIssueDetail(ctx, h.app.ID, h.issueID, now.Add(-time.Hour), now, state.IssueDetailCursors{})
	if err != nil {
		return issuetrackers.Prepared{}, err
	}
	p := issuetrackers.Prepared{Issue: detail.Issue, GroupFingerprint: issuetrackers.GroupScopeFingerprint(detail)}
	cursor := state.IssueCursor{}
	for h.linkID != "" {
		page, e := links.ListIssueTrackerLinks(ctx, h.app.ID, h.app.AccountID, p.Issue.ID, cursor)
		if e != nil {
			return p, e
		}
		for _, item := range page.Items {
			if item.ID == h.linkID {
				p.Link = item
				break
			}
		}
		if p.Link.ID != "" {
			break
		}
		if page.NextCursor == "" {
			return p, state.ErrNotFound
		}
		cursor, e = state.DecodeIssueCursor(page.NextCursor)
		if e != nil {
			return p, e
		}
	}
	binding, e := h.server.store.GetGithubInstallBindingForApp(ctx, h.app.ID, h.account.ID)
	if e != nil {
		if errors.Is(e, state.ErrNotFound) {
			return p, issuetrackers.ConnectionRequired()
		}
		return p, api.ErrCapacity("could not read GitHub binding")
	}
	_, e = h.server.store.GitHubInstallForAccountInstallation(ctx, h.account.ID, binding.InstallID)
	if e != nil {
		return p, issuetrackers.ConnectionRequired()
	}
	n, e := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: p.Link.URL})
	repository := strings.ToLower(binding.RepoFullName)
	if h.linkID != "" && (e != nil || n.Provider != "github" || p.Link.Provider != "github" || strings.SplitN(n.ExternalKey, "#", 2)[0] != repository) || binding.RepoID <= 0 || binding.OwnerID <= 0 || binding.BindingID == "" {
		return p, issuetrackers.ConnectionRequired()
	}
	repos, e := h.server.githubd.ListInstallableRepos(ctx, h.account.ID, binding.InstallID)
	if e != nil {
		return p, api.ErrCapacity("could not verify repository visibility")
	}
	visible := false
	for _, repo := range repos {
		if repo.ID == binding.RepoID && strings.EqualFold(repo.FullName, binding.RepoFullName) {
			visible = true
			break
		}
	}
	if !visible {
		return p, issuetrackers.ConnectionRequired()
	}
	p.Connection = issuetrackers.Connection{AccountID: h.account.ID, BindingID: binding.BindingID, Repository: repository, InstallationID: binding.InstallID, RepositoryID: binding.RepoID, OwnerID: binding.OwnerID}
	return p, nil
}
func (h nativeIssueTrackerHost) Token(ctx context.Context, c issuetrackers.Connection) (string, error) {
	token, expiry, err := h.server.githubd.MintInstallationToken(ctx, c.AccountID, c.InstallationID)
	if err != nil || token == "" || !expiry.After(time.Now().UTC()) {
		return "", api.ErrCapacity("connected GitHub credentials are unavailable")
	}
	return token, nil
}
func (s *server) nativeIssueTrackerService(w http.ResponseWriter, r *http.Request, acct state.Account) (issuetrackers.Service, bool) {
	_, app, ok := s.issueTrackerLinksAccess(w, r, acct)
	if !ok {
		return issuetrackers.Service{}, false
	}
	id, err := uuid.Parse(r.PathValue("link_id"))
	if err != nil || id == uuid.Nil {
		api.WriteProblem(w, api.ErrValidation("link_id must be a nonzero UUID"))
		return issuetrackers.Service{}, false
	}
	return s.issueTrackerServiceFor(app, acct, r.PathValue("issue_id"), id.String()), true
}
func (s *server) issueTrackerServiceFor(app state.App, acct state.Account, issue, link string) issuetrackers.Service {
	provider := s.nativeTrackerProvider
	if provider == nil {
		provider = issuetracker.NewClient(nil)
	}
	return issuetrackers.Service{Host: nativeIssueTrackerHost{server: s, app: app, account: acct, issueID: issue, linkID: link}, Provider: provider}
}

func (s *server) getNativeIssueTrackerStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	service, ok := s.nativeIssueTrackerService(w, r, acct)
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
func (s *server) updateNativeIssueTrackerStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	service, ok := s.nativeIssueTrackerService(w, r, acct)
	if !ok {
		return
	}
	var in api.UpdateNativeIssueTrackerStatusRequest
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
func writeNativeIssueTrackerError(w http.ResponseWriter, err error) {
	var problem *api.Problem
	if errors.As(err, &problem) {
		api.WriteProblem(w, problem)
		return
	}
	writeIssueError(w, err)
}
