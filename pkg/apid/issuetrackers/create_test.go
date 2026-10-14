package issuetrackers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/state"
)

type creationHostFake struct {
	p           Prepared
	tokenHook   func()
	attachErr   error
	attachments int
}

func (h *creationHostFake) Prepare(context.Context) (Prepared, error) { return h.p, nil }
func (h *creationHostFake) Token(context.Context, Connection) (string, error) {
	if h.tokenHook != nil {
		h.tokenHook()
	}
	return "secret", nil
}
func (h *creationHostFake) Attach(_ context.Context, _ issuetracker.Request, t issuetracker.Ticket) (api.IssueTrackerLink, error) {
	h.attachments++
	return api.IssueTrackerLink{ID: uuid.NewString(), URL: t.URL}, h.attachErr
}

type creationStoreFake struct {
	r       state.NativeIssueTicketReceipt
	expired bool
}

func (s *creationStoreFake) ReserveNativeIssueTicket(_ context.Context, r state.NativeIssueTicketReceipt) (state.NativeIssueTicketReceipt, error) {
	if s.r.RequestID == "" {
		s.r = r
		s.r.State = "ready"
	}
	return s.r, nil
}
func (s *creationStoreFake) ClaimNativeIssueTicket(_ context.Context, r state.NativeIssueTicketReceipt, recovery bool) (state.NativeIssueTicketReceipt, error) {
	if (recovery && !s.expired) || (!recovery && s.r.State != "ready") {
		return r, state.ErrNativeIssueTicketBusy
	}
	s.r.State = "dispatching"
	s.r.ClaimID = uuid.NewString()
	s.r.RecoverAfter = time.Now().Add(time.Hour)
	s.expired = false
	return s.r, nil
}
func (s *creationStoreFake) FinishNativeIssueTicket(_ context.Context, r state.NativeIssueTicketReceipt) (state.NativeIssueTicketReceipt, error) {
	if s.r.ClaimID != r.ClaimID {
		return r, state.ErrNativeIssueTicketBusy
	}
	s.r = r
	return r, nil
}
func (s *creationStoreFake) LinkNativeIssueTicket(_ context.Context, r state.NativeIssueTicketReceipt) (state.NativeIssueTicketReceipt, error) {
	s.r = r
	s.r.State = "linked"
	return s.r, nil
}

type creationProviderFake struct {
	creates, recovers int
	err               error
	ticket            issuetracker.Ticket
}

func (p *creationProviderFake) Create(context.Context, issuetracker.Request, string) (issuetracker.Ticket, error) {
	p.creates++
	return p.ticket, p.err
}
func (p *creationProviderFake) Recover(context.Context, issuetracker.Request, string, string) (issuetracker.Ticket, error) {
	p.recovers++
	return p.ticket, p.err
}
func creationFixture() (CreationService, *creationHostFake, *creationStoreFake, *creationProviderFake, api.CreateNativeIssueTicketRequest) {
	host := &creationHostFake{p: Prepared{Issue: api.Issue{ID: uuid.NewString(), AppID: uuid.NewString(), State: "open"}, Connection: Connection{AccountID: uuid.NewString(), BindingID: uuid.NewString(), Repository: "owner/repo", InstallationID: 1, RepositoryID: 2, OwnerID: 3}}}
	store := &creationStoreFake{}
	provider := &creationProviderFake{ticket: issuetracker.Ticket{URL: "https://github.com/owner/repo/issues/42", ProviderID: "42"}}
	return CreationService{Host: host, Store: store, Provider: provider, AppSlug: "example", IssueID: host.p.Issue.ID}, host, store, provider, api.CreateNativeIssueTicketRequest{RequestID: uuid.NewString(), Repository: "owner/repo", Title: "Payment error"}
}
func TestNativeCreationReceiptRetriesAttachmentAndPinsInputs(t *testing.T) {
	service, host, store, provider, in := creationFixture()
	host.attachErr = errors.New("unavailable")
	out, err := service.Run(t.Context(), in)
	if err != nil || out.State != "created" || provider.creates != 1 {
		t.Fatal(out, err)
	}
	host.attachErr = nil
	out, err = service.Run(t.Context(), in)
	if err != nil || out.State != "linked" || provider.creates != 1 {
		t.Fatal(out, err)
	}
	out, err = service.Run(t.Context(), in)
	if err != nil || out.LinkID != store.r.LinkID || host.attachments != 2 {
		t.Fatal(out, err)
	}
	in.Title = "different"
	if _, err = service.Run(t.Context(), in); err == nil || provider.creates != 1 {
		t.Fatal("changed inputs authorized new create")
	}
}
func TestNativeCreationUnknownCannotCreateAgainAndRecoveryIsFenced(t *testing.T) {
	service, _, store, provider, in := creationFixture()
	provider.err = errors.New("secret transport details")
	if _, err := service.Run(t.Context(), in); err == nil || store.r.State != "dispatching" {
		t.Fatal(err)
	}
	if _, err := service.Run(t.Context(), in); err == nil || provider.creates != 1 {
		t.Fatal("uncertain create repeated")
	}
	in.RecoveryURL = provider.ticket.URL
	provider.err = nil
	if _, err := service.Run(t.Context(), in); err == nil || provider.recovers != 0 {
		t.Fatal("active dispatch was not fenced")
	}
	store.expired = true
	out, err := service.Run(t.Context(), in)
	if err != nil || out.State != "linked" || provider.creates != 1 || provider.recovers != 1 {
		t.Fatal(out, err)
	}
}
func TestNativeCreationChangedConnectionNeverDispatches(t *testing.T) {
	service, host, _, provider, in := creationFixture()
	host.tokenHook = func() { host.p.Connection.RepositoryID++ }
	if _, err := service.Run(t.Context(), in); err == nil || provider.creates != 0 {
		t.Fatal("changed connection dispatched")
	}
}
func TestNativeCreationRejectedCanRetryButInvalidEvidenceCannot(t *testing.T) {
	service, _, store, provider, in := creationFixture()
	provider.err = &issuetracker.RejectedError{Status: 403}
	if _, err := service.Run(t.Context(), in); err == nil || store.r.State != "ready" {
		t.Fatal(err)
	}
	provider.err = nil
	provider.ticket.URL = "https://github.com/another/repo/issues/42"
	if _, err := service.Run(t.Context(), in); err == nil || store.r.State != "dispatching" {
		t.Fatal("invalid identity accepted")
	}
	if _, err := service.Run(t.Context(), in); err == nil || provider.creates != 2 {
		t.Fatal("invalid evidence repeated creation")
	}
}
