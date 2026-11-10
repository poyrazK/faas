package alerts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/state"
)

type sentTo struct{ email string }

func (s *sentTo) Send(_ context.Context, t alertchannels.Target, _ alertchannels.Message) error {
	s.email = t.Email
	return nil
}

type accountsByID map[string]state.Account

func (a accountsByID) AccountByID(_ context.Context, id string) (state.Account, error) {
	if acct, ok := a[id]; ok {
		return acct, nil
	}
	return state.Account{}, state.ErrNotFound
}

// adr: 20261109153000 — an email channel sends to its account's current
// address, and only once that address is verified; other kinds pass through.
func TestEmailChannelsSendToTheCurrentVerifiedAddress(t *testing.T) {
	verified := time.Now()
	accounts := accountsByID{
		"moved":      {ID: "moved", Email: "new@example.com", EmailVerifiedAt: &verified},
		"unverified": {ID: "unverified", Email: "someone@example.com"},
	}
	inner := &sentTo{}
	sender := verifiedEmailSender(inner, accounts)
	ctx := context.Background()

	if err := sender.Send(ctx, alertchannels.Target{Kind: alertchannels.KindEmail, Email: "old@example.com", AccountID: "moved"}, alertchannels.Message{}); err != nil || inner.email != "new@example.com" {
		t.Fatalf("send = %v to %q; want the account's current address", err, inner.email)
	}
	inner.email = ""
	if err := sender.Send(ctx, alertchannels.Target{Kind: alertchannels.KindEmail, Email: "someone@example.com", AccountID: "unverified"}, alertchannels.Message{}); !errors.Is(err, errEmailUnverified) || inner.email != "" {
		t.Fatalf("send = %v to %q; want an unverified address refused", err, inner.email)
	}
	if err := sender.Send(ctx, alertchannels.Target{Kind: alertchannels.KindEmail, Email: "x@example.com", AccountID: "gone"}, alertchannels.Message{}); err == nil {
		t.Fatal("an unreadable account must not be mailed")
	}
	if err := sender.Send(ctx, alertchannels.Target{Kind: alertchannels.KindSlack, AccountID: "unverified"}, alertchannels.Message{}); err != nil {
		t.Fatalf("slack send = %v; other kinds pass through", err)
	}
	if again, ok := verifiedEmailSender(sender, accounts).(emailResolvingSender); !ok {
		t.Fatal("wrapping again must keep the wrapper")
	} else if _, nested := again.inner.(emailResolvingSender); nested {
		t.Fatal("wrapping twice must keep one wrapper")
	}
}
