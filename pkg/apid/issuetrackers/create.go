package issuetrackers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	return api.NewProblem(409, "issue_tracker_creation_unconfirmed", "Ticket outcome unconfirmed", "reuse the same request ID and inputs; after the dispatch window use recovery_url to verify the existing ticket's operation marker")
}
func creationResult(r state.NativeIssueTicketReceipt) api.NativeIssueTicketResult {
	return api.NativeIssueTicketResult{RequestID: r.RequestID, State: r.State, TicketURL: r.TicketURL, LinkID: r.LinkID}
}
func creationTicketValid(r issuetracker.Request, t issuetracker.Ticket) bool {
	n, e := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: t.URL})
	number, e2 := strconv.ParseInt(t.ProviderID, 10, 64)
	return e == nil && e2 == nil && number > 0 && n.Provider == "github" && n.URL == t.URL && n.ExternalKey == r.Target+"#"+t.ProviderID
}
func creationValid(p Prepared) bool {
	c := p.Connection
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
	if s.Host == nil || s.Store == nil || s.Provider == nil {
		return api.NativeIssueTicketResult{}, unavailable()
	}
	p, e := s.Host.Prepare(ctx)
	if e != nil {
		return api.NativeIssueTicketResult{}, e
	}
	if !creationValid(p) {
		return api.NativeIssueTicketResult{}, ConnectionRequired()
	}
	request, e := issuetracker.ValidateRequest(issuetracker.Request{RequestID: in.RequestID, APIBase: "https://gregale.dev", AppID: p.Issue.AppID, AppSlug: s.AppSlug, IssueID: s.IssueID, Provider: "github", Target: in.Repository, Title: in.Title, Description: in.Description})
	if e != nil {
		return api.NativeIssueTicketResult{}, api.ErrValidation(e.Error())
	}
	if request.Target != p.Connection.Repository {
		return api.NativeIssueTicketResult{}, ConnectionRequired()
	}
	if in.RecoveryURL != "" {
		n, err := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: in.RecoveryURL})
		if err != nil || n.Provider != "github" {
			return api.NativeIssueTicketResult{}, api.ErrValidation("recovery_url must be a GitHub issue URL")
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
		receipt, e = s.dispatch(ctx, p, request, receipt, in.RecoveryURL)
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
	if e != nil {
		return creationResult(receipt), nil
	}
	if link.ID == "" || link.URL != receipt.TicketURL {
		return creationResult(receipt), unavailable()
	}
	receipt.LinkID = link.ID
	receipt, e = s.Store.LinkNativeIssueTicket(ctx, receipt)
	if e != nil {
		return creationResult(receipt), api.ErrCapacity("ticket exists; retry the same request to confirm attachment")
	}
	return creationResult(receipt), nil
}
func (s CreationService) dispatch(ctx context.Context, p Prepared, r issuetracker.Request, receipt state.NativeIssueTicketReceipt, recoveryURL string) (state.NativeIssueTicketReceipt, error) {
	recovery := receipt.State == "dispatching"
	if recovery && recoveryURL == "" {
		return receipt, unknownCreation()
	}
	if !recovery && recoveryURL != "" {
		return receipt, api.ErrValidation("recovery_url applies only to an unconfirmed dispatch")
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
			return receipt, api.NewProblem(502, "issue_tracker_creation_rejected", "Ticket creation rejected", "GitHub rejected creation; check installation Issues write permission and retry the same inputs")
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
