package issuetracker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakeProvider struct {
	create  func() (Ticket, error)
	recover func(string) (Ticket, error)
}

func (p fakeProvider) Create(context.Context, Request, string) (Ticket, error) { return p.create() }
func (p fakeProvider) Recover(_ context.Context, _ Request, _ string, url string) (Ticket, error) {
	return p.recover(url)
}

// t.TempDir inherits the runner umask on some Go/OS combinations. The journal
// contract requires a directory created explicitly with owner-only permissions.
func privateJournalDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "journal")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
func ticketFixture() Ticket {
	return Ticket{URL: "https://github.com/owner/repo/issues/42", ProviderID: "42"}
}
func linkedTicket(_ context.Context, ticket Ticket) (api.IssueTrackerLink, error) {
	return api.IssueTrackerLink{ID: "55555555-5555-4555-8555-555555555555", URL: ticket.URL}, nil
}
func TestJournalRetriesAttachmentWithoutCreatingAgain(t *testing.T) {
	dir := privateJournalDir(t)
	r := requestFixture()
	creates, links := 0, 0
	provider := fakeProvider{create: func() (Ticket, error) { creates++; return ticketFixture(), nil }}
	linker := func(ctx context.Context, ticket Ticket) (api.IssueTrackerLink, error) {
		links++
		if links == 1 {
			return api.IssueTrackerLink{}, errors.New("temporary Gregale failure")
		}
		return linkedTicket(ctx, ticket)
	}
	first, err := Run(context.Background(), dir, r, "secret-credential", "", provider, linker)
	if err == nil || first.State != "created" || first.Ticket != ticketFixture() {
		t.Fatalf("ticket was not retained: %+v %v", first, err)
	}
	// A new invocation can resume linking without a provider credential.
	final, err := Run(context.Background(), dir, r, "", "", provider, linker)
	if err != nil || final.State != "linked" || creates != 1 || links != 2 {
		t.Fatalf("retry repeated creation: %+v %v (%d %d)", final, err, creates, links)
	}
	_, err = Run(context.Background(), dir, r, "", "", provider, linker)
	if err != nil || creates != 1 || links != 2 {
		t.Fatal("completed retry repeated an effect")
	}
	data, err := os.ReadFile(filepath.Join(dir, r.RequestID+".json"))
	if err != nil || strings.Contains(string(data), "secret-credential") {
		t.Fatal("credential persisted", err)
	}
	info, err := os.Stat(filepath.Join(dir, r.RequestID+".json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("receipt is not private")
	}
	changed := r
	changed.Title = "Different operation"
	if _, err = Run(context.Background(), dir, changed, "", "", provider, linker); !errors.Is(err, ErrConflict) {
		t.Fatal("request ID reused for different inputs", err)
	}
}
func TestJournalUncertainDispatchRequiresVerifiedRecovery(t *testing.T) {
	dir := privateJournalDir(t)
	r := requestFixture()
	creates, recovers := 0, 0
	provider := fakeProvider{create: func() (Ticket, error) { creates++; return Ticket{}, errors.New("response lost") }, recover: func(url string) (Ticket, error) {
		recovers++
		if url != ticketFixture().URL {
			return Ticket{}, errors.New("wrong ticket")
		}
		return ticketFixture(), nil
	}}
	for i := 0; i < 2; i++ {
		out, err := Run(context.Background(), dir, r, "secret", "", provider, linkedTicket)
		if !errors.Is(err, ErrOutcomeUnknown) || out.State != "dispatching" {
			t.Fatalf("uncertain write was retried: %+v %v", out, err)
		}
	}
	if creates != 1 {
		t.Fatal("duplicate dispatch")
	}
	out, err := Run(context.Background(), dir, r, "secret", ticketFixture().URL, provider, linkedTicket)
	if err != nil || out.State != "linked" || creates != 1 || recovers != 1 {
		t.Fatal("recovery created another ticket", out, err)
	}
}
func TestJournalConcurrentRequestsAreFencedBeforeDispatch(t *testing.T) {
	dir := privateJournalDir(t)
	r := requestFixture()
	started, finish := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	provider := fakeProvider{create: func() (Ticket, error) { close(started); <-finish; return ticketFixture(), nil }}
	go func() { _, err := Run(context.Background(), dir, r, "secret", "", provider, linkedTicket); done <- err }()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("first request returned before provider dispatch: %v", err)
	}
	_, err := Run(context.Background(), dir, r, "secret", "", provider, linkedTicket)
	close(finish)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent request was not fenced: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestJournalExplicitRejectionCanRetryButCorruptEvidenceCannot(t *testing.T) {
	dir := privateJournalDir(t)
	r := requestFixture()
	calls := 0
	provider := fakeProvider{create: func() (Ticket, error) {
		calls++
		if calls == 1 {
			return Ticket{}, &RejectedError{Status: 401}
		}
		return ticketFixture(), nil
	}}
	first, err := Run(context.Background(), dir, r, "expired", "", provider, linkedTicket)
	if err == nil || first.State != "ready" {
		t.Fatal("confirmed rejection did not retain safe retry", err)
	}
	if _, err = Run(context.Background(), dir, r, "fresh", "", provider, linkedTicket); err != nil || calls != 2 {
		t.Fatal("credential rotation cannot retry", err)
	}
	path := filepath.Join(dir, r.RequestID+".json")
	data, _ := os.ReadFile(path)
	var entry receipt
	_ = json.Unmarshal(data, &entry)
	entry.Outcome.State = "unknown-version"
	data, _ = json.Marshal(entry)
	_ = os.WriteFile(path, data, 0600)
	if _, err = Run(context.Background(), dir, r, "fresh", "", provider, linkedTicket); err == nil || calls != 2 {
		t.Fatal("corrupt evidence allowed creation")
	}
}
func TestJournalRejectsUntrustedPermissionsSymlinksAndCanceledDispatch(t *testing.T) {
	r := requestFixture()
	provider := fakeProvider{create: func() (Ticket, error) { t.Fatal("unexpected dispatch"); return Ticket{}, nil }}
	public := t.TempDir()
	if err := os.Chmod(public, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), public, r, "secret", "", provider, linkedTicket); err == nil {
		t.Fatal("public journal accepted")
	}
	dir := privateJournalDir(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), alias, r, "secret", "", provider, linkedTicket); err == nil {
		t.Fatal("symlink journal accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, dir, r, "secret", "", provider, linkedTicket); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled request dispatched", err)
	}
	if _, err := os.Stat(filepath.Join(dir, r.RequestID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled request left uncertain evidence")
	}
}

func TestJournalEscapedDescriptionRetainsRecoverableTicket(t *testing.T) {
	dir := privateJournalDir(t)
	r := requestFixture()
	r.Description = strings.Repeat("<", api.IssueTrackerTicketBodyMaxBytes)
	creates, links := 0, 0
	provider := fakeProvider{create: func() (Ticket, error) { creates++; return ticketFixture(), nil }}
	linker := func(ctx context.Context, ticket Ticket) (api.IssueTrackerLink, error) {
		links++
		if links == 1 {
			return api.IssueTrackerLink{}, errors.New("offline")
		}
		return linkedTicket(ctx, ticket)
	}
	if _, err := Run(context.Background(), dir, r, "secret", "", provider, linker); err == nil {
		t.Fatal("failed attachment reported success")
	}
	if _, err := Run(context.Background(), dir, r, "", "", provider, linker); err != nil || creates != 1 {
		t.Fatal("JSON escaping broke receipt recovery", err)
	}
}
