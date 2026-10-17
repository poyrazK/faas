// Package issuetrackers owns native provider status operations after API authorization.
package issuetrackers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
)

type Connection struct {
	AccountID, BindingID, Repository      string
	InstallationID, RepositoryID, OwnerID int64
	Linear                                *LinearBoundConnection `json:"Linear,omitempty"`
}
type Prepared struct {
	GroupFingerprint string
	Issue            api.Issue
	Link             api.IssueTrackerLink
	Connection       Connection
}
type Host interface {
	Prepare(context.Context) (Prepared, error)
	Token(context.Context, Connection) (string, error)
}
type Service struct {
	Host     Host
	Provider issuetracker.StatusProvider
}

func ConnectionRequired() error {
	return api.NewProblem(409, "issue_tracker_connection_required", "Connected GitHub repository required", "connect and bind the app owner's GitHub installation with verified repository identity")
}
func Changed() error {
	return api.NewProblem(409, "issue_tracker_connection_changed", "Tracker connection changed", "the issue group, reference or provider connection changed; preview again")
}
func unavailable() error {
	return api.NewProblem(http.StatusBadGateway, "issue_tracker_provider_unavailable", "Tracker unavailable", "connected provider status could not be confirmed; preview again before retrying")
}
func valid(p Prepared) error {
	n, err := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: p.Link.URL})
	if err != nil || n.Provider != "github" || p.Link.Provider != "github" || n.URL != p.Link.URL {
		return ConnectionRequired()
	}
	repository := strings.SplitN(n.ExternalKey, "#", 2)[0]
	c := p.Connection
	if c.AccountID == "" || c.BindingID == "" || c.InstallationID <= 0 || c.RepositoryID <= 0 || c.OwnerID <= 0 || c.Repository != repository {
		return ConnectionRequired()
	}
	return nil
}
func same(a, b Prepared) bool {
	return a.Issue.ID == b.Issue.ID && a.Issue.AppID == b.Issue.AppID && a.Issue.State == b.Issue.State && a.Link.ID == b.Link.ID && a.Link.URL == b.Link.URL && a.Link.Provider == b.Link.Provider && sameConnection(a.Connection, b.Connection) && a.GroupFingerprint == b.GroupFingerprint
}
func fromStatus(s issuetracker.Status) api.IssueTrackerProviderStatus {
	return api.IssueTrackerProviderStatus{Provider: s.Provider, URL: s.URL, ID: s.ID, State: s.State, TeamID: s.TeamID, StateID: s.StateID}
}
func toStatus(s api.IssueTrackerProviderStatus) issuetracker.Status {
	return issuetracker.Status{Provider: s.Provider, URL: s.URL, ID: s.ID, State: s.State, TeamID: s.TeamID, StateID: s.StateID}
}
func result(p Prepared, s issuetracker.Status) api.NativeIssueTrackerStatus {
	return api.NativeIssueTrackerStatus{IssueID: p.Issue.ID, LinkID: p.Link.ID, InstallationID: p.Connection.InstallationID, Repository: p.Connection.Repository, RepositoryID: p.Connection.RepositoryID, Status: fromStatus(s)}
}
func validStatus(p Prepared, s issuetracker.Status) bool {
	n, e := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: p.Link.URL})
	return e == nil && s.Provider == "github" && s.URL == p.Link.URL && n.ExternalKey == p.Connection.Repository+"#"+s.ID && (s.State == "open" || s.State == "resolved") && s.TeamID == "" && s.StateID == ""
}
func mappedError(err error) error {
	if errors.Is(err, issuetracker.ErrStatusChanged) {
		return api.NewProblem(409, "issue_tracker_status_changed", "Tracker status changed", "the provider lifecycle changed; preview again")
	}
	if errors.Is(err, issuetracker.ErrUnsupportedStatus) {
		return api.NewProblem(409, "issue_tracker_status_unsupported", "Manual triage required", "this provider status cannot be mapped to open or resolved")
	}
	return unavailable()
}
func (s Service) prepare(ctx context.Context) (Prepared, error) {
	if s.Host == nil || s.Provider == nil {
		return Prepared{}, unavailable()
	}
	p, err := s.Host.Prepare(ctx)
	if err != nil {
		return p, err
	}
	return p, valid(p)
}
func (s Service) credentials(ctx context.Context, p Prepared) (string, error) {
	token, err := s.Host.Token(ctx, p.Connection)
	if err != nil || token == "" {
		return "", unavailable()
	}
	fresh, err := s.prepare(ctx)
	if err != nil {
		return "", err
	}
	if !same(p, fresh) {
		return "", Changed()
	}
	return token, nil
}
func (s Service) Read(ctx context.Context) (api.NativeIssueTrackerStatus, error) {
	p, err := s.prepare(ctx)
	if err != nil {
		return api.NativeIssueTrackerStatus{}, err
	}
	token, err := s.credentials(ctx, p)
	if err != nil {
		return api.NativeIssueTrackerStatus{}, err
	}
	remote, err := s.Provider.ReadStatus(ctx, p.Link.URL, token)
	if err != nil {
		return api.NativeIssueTrackerStatus{}, mappedError(err)
	}
	if !validStatus(p, remote) {
		return api.NativeIssueTrackerStatus{}, unavailable()
	}
	latest, err := s.prepare(ctx)
	if err != nil {
		return api.NativeIssueTrackerStatus{}, err
	}
	if !same(p, latest) {
		return api.NativeIssueTrackerStatus{}, Changed()
	}
	return result(p, remote), nil
}
func (s Service) Push(ctx context.Context, in api.UpdateNativeIssueTrackerStatusRequest) (api.NativeIssueTrackerStatus, error) {
	if in.DesiredState != "open" && in.DesiredState != "resolved" {
		return api.NativeIssueTrackerStatus{}, api.ErrValidation("desired_state must be open or resolved")
	}
	p, err := s.prepare(ctx)
	if err != nil {
		return api.NativeIssueTrackerStatus{}, err
	}
	if p.Issue.State != in.DesiredState {
		return api.NativeIssueTrackerStatus{}, Changed()
	}
	if in.Expected.Provider != "github" || in.Expected.URL != p.Link.URL || in.Expected.ID == "" || in.Expected.TeamID != "" || in.Expected.StateID != "" || in.Expected.State != "open" && in.Expected.State != "resolved" {
		return api.NativeIssueTrackerStatus{}, api.ErrValidation("expected must be the observed status of this linked GitHub issue")
	}
	token, err := s.credentials(ctx, p)
	if err != nil {
		return api.NativeIssueTrackerStatus{}, err
	}
	remote, err := s.Provider.SetStatus(ctx, toStatus(in.Expected), token, in.DesiredState, "")
	if err != nil {
		return api.NativeIssueTrackerStatus{}, mappedError(err)
	}
	if !validStatus(p, remote) || remote.ID != in.Expected.ID || remote.State != in.DesiredState {
		return api.NativeIssueTrackerStatus{}, unavailable()
	}
	return result(p, remote), nil
}

// Revision fences in-flight operations; durable receipts bind identity across key rotation.
type LinearBoundConnection struct {
	Scope    LinearCredentialScope
	Revision int64 `json:"-"`
}

func sameConnection(a, b Connection) bool {
	if (a.Linear == nil) != (b.Linear == nil) {
		return false
	}
	if a.Linear != nil && *a.Linear != *b.Linear {
		return false
	}
	a.Linear = nil
	b.Linear = nil
	return a == b
}

func LinearConnectionRequired() error {
	return api.NewProblem(409, "issue_tracker_connection_required", "Connected Linear team required", "connect the app owner's Linear personal API key to the selected active team")
}
