package issuetrackers

import (
	"context"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
)

type LinearService struct {
	Host     Host
	Provider issuetracker.StatusProvider
}

func (s LinearService) prepare(ctx context.Context) (Prepared, error) {
	if s.Host == nil || s.Provider == nil {
		return Prepared{}, unavailable()
	}
	p, err := s.Host.Prepare(ctx)
	if err != nil {
		return p, err
	}
	n, e := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: p.Link.URL})
	if !creationValid(p) || p.Connection.Linear == nil || p.Link.ID == "" || p.GroupFingerprint == "" || e != nil || p.Link.Provider != "linear" || n.Provider != "linear" || n.URL != p.Link.URL {
		return p, LinearConnectionRequired()
	}
	frozen := *p.Connection.Linear
	p.Connection.Linear = &frozen
	return p, nil
}
func (s LinearService) fresh(ctx context.Context, p Prepared) error {
	latest, err := s.prepare(ctx)
	if err != nil {
		return err
	}
	if !same(p, latest) {
		return Changed()
	}
	return nil
}
func (s LinearService) credentials(ctx context.Context, p Prepared) (string, error) {
	token, err := s.Host.Token(ctx, p.Connection)
	if err != nil || token == "" {
		return "", unavailable()
	}
	if err := s.fresh(ctx, p); err != nil {
		return "", err
	}
	return token, nil
}
func validLinearStatus(p Prepared, status issuetracker.Status) bool {
	for _, value := range []string{status.ID, status.TeamID, status.StateID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return false
		}
	}
	return status.Provider == "linear" && status.URL == p.Link.URL && status.TeamID == p.Connection.Linear.Scope.Identity.TeamID && (status.State == "open" || status.State == "resolved")
}
func linearStatusResult(p Prepared, status issuetracker.Status) api.NativeLinearIssueTrackerStatus {
	c := p.Connection.Linear
	return api.NativeLinearIssueTrackerStatus{IssueID: p.Issue.ID, LinkID: p.Link.ID, ConnectionRevision: c.Revision, GroupFingerprint: p.GroupFingerprint, WorkspaceID: c.Scope.Identity.WorkspaceID, TeamID: c.Scope.Identity.TeamID, ViewerID: c.Scope.Identity.ViewerID, Status: fromStatus(status)}
}
func (s LinearService) Read(ctx context.Context) (api.NativeLinearIssueTrackerStatus, error) {
	p, err := s.prepare(ctx)
	if err != nil {
		return api.NativeLinearIssueTrackerStatus{}, err
	}
	token, err := s.credentials(ctx, p)
	if err != nil {
		return api.NativeLinearIssueTrackerStatus{}, err
	}
	remote, err := s.Provider.ReadStatus(ctx, p.Link.URL, token)
	if err != nil {
		return api.NativeLinearIssueTrackerStatus{}, mappedError(err)
	}
	if !validLinearStatus(p, remote) {
		return api.NativeLinearIssueTrackerStatus{}, unavailable()
	}
	if err := s.fresh(ctx, p); err != nil {
		return api.NativeLinearIssueTrackerStatus{}, err
	}
	return linearStatusResult(p, remote), nil
}
func (s LinearService) Push(ctx context.Context, in api.UpdateNativeLinearIssueTrackerStatusRequest) (api.NativeLinearIssueTrackerStatus, error) {
	if in.DesiredState != "open" && in.DesiredState != "resolved" {
		return api.NativeLinearIssueTrackerStatus{}, api.ErrValidation("desired_state must be open or resolved")
	}
	p, err := s.prepare(ctx)
	if err != nil {
		return api.NativeLinearIssueTrackerStatus{}, err
	}
	if p.Issue.State != in.DesiredState || in.ExpectedConnectionRevision != p.Connection.Linear.Revision || in.ExpectedGroupFingerprint != p.GroupFingerprint {
		return api.NativeLinearIssueTrackerStatus{}, Changed()
	}
	expected := toStatus(in.Expected)
	if !validLinearStatus(p, expected) {
		return api.NativeLinearIssueTrackerStatus{}, api.ErrValidation("expected must be the observed status of this linked Linear issue and connected team")
	}
	selected, parseErr := uuid.Parse(in.LinearStateID)
	if in.LinearStateID != "" || expected.State != in.DesiredState {
		if parseErr != nil || selected == uuid.Nil {
			return api.NativeLinearIssueTrackerStatus{}, api.ErrValidation("Linear push requires a nonzero workflow state UUID")
		}
		in.LinearStateID = selected.String()
	}
	token, err := s.credentials(ctx, p)
	if err != nil {
		return api.NativeLinearIssueTrackerStatus{}, err
	}
	remote, err := s.Provider.SetStatus(ctx, expected, token, in.DesiredState, in.LinearStateID)
	if err != nil {
		return api.NativeLinearIssueTrackerStatus{}, mappedError(err)
	}
	if !validLinearStatus(p, remote) || remote.ID != expected.ID || remote.State != in.DesiredState || (expected.State != in.DesiredState && remote.StateID != in.LinearStateID) || (expected.State == in.DesiredState && remote != expected) {
		return api.NativeLinearIssueTrackerStatus{}, unavailable()
	}
	if err := s.fresh(ctx, p); err != nil {
		return api.NativeLinearIssueTrackerStatus{}, err
	}
	return linearStatusResult(p, remote), nil
}
