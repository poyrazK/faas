package issuetrackers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/state"
)

// CreationHost supplies an owned binding and attaches confirmed tickets only.
type CreationHost interface {
	Host
	Attach(context.Context, issuetracker.Request, issuetracker.Ticket) (api.IssueTrackerLink, error)
}
type CreationService struct {
	Host             CreationHost
	Store            state.NativeIssueTicketStore
	Provider         issuetracker.Provider
	AppSlug, IssueID string
}

func unknownCreation() error {
	return api.NewProblem(409, "issue_tracker_creation_unconfirmed", "Ticket outcome unconfirmed", "reuse the same request ID and inputs; after the dispatch window use recovery_url for GitHub or recover=true for Linear to verify the existing ticket")
}
func creationResult(r state.NativeIssueTicketReceipt) api.NativeIssueTicketResult {
	return api.NativeIssueTicketResult{RequestID: r.RequestID, State: r.State, TicketURL: r.TicketURL, LinkID: r.LinkID}
}
func creationTicketValid(r issuetracker.Request, t issuetracker.Ticket) bool {
	n, e := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: t.URL})
	if r.Provider == "linear" {
		id, err := uuid.Parse(t.ProviderID)
		return e == nil && err == nil && n.Provider == "linear" && n.URL == t.URL && id.String() == r.RequestID
	}
	number, e2 := strconv.ParseInt(t.ProviderID, 10, 64)
	return e == nil && e2 == nil && number > 0 && n.Provider == "github" && n.URL == t.URL && n.ExternalKey == r.Target+"#"+t.ProviderID
}
func creationValid(p Prepared) bool {
	c := p.Connection
	if c.Linear != nil {
		return p.Issue.ID != "" && p.Issue.AppID == c.Linear.Scope.AppID && c.AccountID == c.Linear.Scope.AccountID && validLinearScope(c.Linear.Scope) && c.Linear.Revision > 0
	}
	return p.Issue.ID != "" && p.Issue.AppID != "" && c.AccountID != "" && c.BindingID != "" && c.InstallationID > 0 && c.RepositoryID > 0 && c.OwnerID > 0 && c.Repository != ""
}
func (s CreationService) fresh(ctx context.Context, p Prepared) error {
	latest, e := s.Host.Prepare(ctx)
	if e != nil {
		return e
	}
	if !creationValid(latest) || !same(p, latest) {
		return Changed()
	}
	return nil
}
func (s CreationService) Run(ctx context.Context, in api.CreateNativeIssueTicketRequest) (api.NativeIssueTicketResult, error) {
	return s.run(ctx, in, "github", in.RecoveryURL != "")
}
func (s CreationService) RunLinear(ctx context.Context, in api.CreateNativeLinearIssueTicketRequest) (api.NativeIssueTicketResult, error) {
	return s.run(ctx, api.CreateNativeIssueTicketRequest{RequestID: in.RequestID, Repository: in.TeamID, Title: in.Title, Description: in.Description, RecoveryURL: in.RecoveryURL}, "linear", in.Recover || in.RecoveryURL != "")
}
func (s CreationService) run(ctx context.Context, in api.CreateNativeIssueTicketRequest, provider string, recoverRequested bool) (api.NativeIssueTicketResult, error) {
	if s.Host == nil || s.Store == nil || s.Provider == nil {
		return api.NativeIssueTicketResult{}, unavailable()
	}
	p, e := s.Host.Prepare(ctx)
	if e != nil {
		return api.NativeIssueTicketResult{}, e
	}
	if !creationValid(p) || (provider == "linear") != (p.Connection.Linear != nil) {
		if provider == "linear" {
			return api.NativeIssueTicketResult{}, LinearConnectionRequired()
		}
		return api.NativeIssueTicketResult{}, ConnectionRequired()
	}
	if p.Connection.Linear != nil {
		copy := *p.Connection.Linear
		p.Connection.Linear = &copy
	}
	request, e := issuetracker.ValidateRequest(issuetracker.Request{RequestID: in.RequestID, APIBase: "https://gregale.dev", AppID: p.Issue.AppID, AppSlug: s.AppSlug, IssueID: s.IssueID, Provider: provider, Target: in.Repository, Title: in.Title, Description: in.Description})
	if e != nil {
		return api.NativeIssueTicketResult{}, api.ErrValidation(e.Error())
	}
	target := p.Connection.Repository
	if p.Connection.Linear != nil {
		target = p.Connection.Linear.Scope.Identity.TeamID
	}
	if request.Target != target {
		if provider == "linear" {
			return api.NativeIssueTicketResult{}, LinearConnectionRequired()
		}
		return api.NativeIssueTicketResult{}, ConnectionRequired()
	}
	if in.RecoveryURL != "" {
		n, err := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: in.RecoveryURL})
		if err != nil || n.Provider != provider {
			return api.NativeIssueTicketResult{}, api.ErrValidation("recovery_url must match the selected ticket provider")
		}
		in.RecoveryURL = n.URL
	}
	encoded, _ := json.Marshal(struct {
		Request    issuetracker.Request
		Connection Connection
	}{request, p.Connection})
	sum := sha256.Sum256(encoded)
	receipt, e := s.Store.ReserveNativeIssueTicket(ctx, state.NativeIssueTicketReceipt{AccountID: p.Connection.AccountID, AppID: p.Issue.AppID, IssueID: request.IssueID, RequestID: request.RequestID, Hash: hex.EncodeToString(sum[:])})
	if e != nil {
		return api.NativeIssueTicketResult{}, e
	}
	if receipt.Hash != hex.EncodeToString(sum[:]) {
		return api.NativeIssueTicketResult{}, api.NewProblem(409, "issue_tracker_request_conflict", "Ticket request conflict", "request ID belongs to different inputs or a different connection; preserve the original request for recovery")
	}
	if in.RecoveryURL != "" && receipt.TicketURL != "" && receipt.TicketURL != in.RecoveryURL {
		return creationResult(receipt), api.ErrValidation("recovery_url conflicts with the recorded ticket")
	}
	if receipt.State == "linked" {
		return creationResult(receipt), nil
	}
	if receipt.State == "ready" || receipt.State == "dispatching" {
		receipt, e = s.dispatch(ctx, p, request, receipt, in.RecoveryURL, recoverRequested)
		if e != nil {
			return creationResult(receipt), e
		}
	}
	if receipt.State != "created" || !creationTicketValid(request, issuetracker.Ticket{URL: receipt.TicketURL, ProviderID: receipt.ProviderID}) {
		return creationResult(receipt), unknownCreation()
	}
	// Provider evidence is already durable; never create again if attachment fails.
	if e = s.fresh(ctx, p); e != nil {
		return creationResult(receipt), e
	}
	link, e := s.Host.Attach(ctx, request, issuetracker.Ticket{URL: receipt.TicketURL, ProviderID: receipt.ProviderID})
	if e == nil {
		if link.ID == "" || link.URL != receipt.TicketURL {
			return creationResult(receipt), unavailable()
		}
		receipt.LinkID = link.ID
		receipt, e = s.Store.LinkNativeIssueTicket(ctx, receipt)
		if e != nil {
			return creationResult(receipt), api.ErrCapacity("ticket exists; retry the same request to confirm attachment")
		}
	}
	// A failed attachment still returns the durable "created" receipt. Retrying
	// it resumes linking without issuing a second provider creation.
	return creationResult(receipt), nil
}
func (s CreationService) dispatch(ctx context.Context, p Prepared, r issuetracker.Request, receipt state.NativeIssueTicketReceipt, recoveryURL string, recoverRequested bool) (state.NativeIssueTicketReceipt, error) {
	recovery := receipt.State == "dispatching"
	if recovery && !recoverRequested {
		return receipt, unknownCreation()
	}
	if !recovery && recoverRequested {
		return receipt, api.ErrValidation("recovery applies only to an unconfirmed dispatch")
	}
	token, e := s.Host.Token(ctx, p.Connection)
	if e != nil || token == "" {
		return receipt, unavailable()
	}
	if e = s.fresh(ctx, p); e != nil {
		return receipt, e
	}
	if e = ctx.Err(); e != nil {
		return receipt, e
	}
	receipt, e = s.Store.ClaimNativeIssueTicket(ctx, receipt, recovery)
	if e != nil {
		return receipt, unknownCreation()
	}
	var ticket issuetracker.Ticket
	if recovery {
		ticket, e = s.Provider.Recover(ctx, r, token, recoveryURL)
	} else {
		ticket, e = s.Provider.Create(ctx, r, token)
	}
	if e != nil {
		var rejected *issuetracker.RejectedError
		if !recovery && errors.As(e, &rejected) {
			receipt.State = "ready"
			if _, err := s.Store.FinishNativeIssueTicket(ctx, receipt); err != nil {
				return receipt, unknownCreation()
			}
			return receipt, api.NewProblem(502, "issue_tracker_creation_rejected", "Ticket creation rejected", "provider rejected creation; check ticket write permission and retry the same inputs")
		}
		return receipt, unknownCreation()
	}
	if !creationTicketValid(r, ticket) {
		return receipt, unknownCreation()
	}
	receipt.State = "created"
	receipt.TicketURL = ticket.URL
	receipt.ProviderID = ticket.ProviderID
	saved, e := s.Store.FinishNativeIssueTicket(ctx, receipt)
	if e != nil {
		return receipt, unknownCreation()
	}
	return saved, nil
}
