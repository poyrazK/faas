package alerts

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/state"
)

// An email channel may only address its account's own email
// (ADR-749). It stored that address when it was created, so it kept sending
// to an address the account had since changed, and it could be created
// before the account had verified its address, which made it a way to mail
// someone else's inbox (ADR-20261109120000). The channel now sends to the
// account's current address, and only once that address is verified.

// errEmailUnverified is an email channel's delivery error while its
// account's address is unverified.
var errEmailUnverified = errors.New("the account's email address is not verified; verify it to receive email alerts")

type accountReader interface {
	AccountByID(ctx context.Context, id string) (state.Account, error)
}

// verifiedEmailSender wraps sender so an email target is sent to its
// account's current verified address. A store that cannot read accounts, or
// a target without an account, keeps the stored address.
func verifiedEmailSender(sender ChannelSender, store any) ChannelSender {
	accounts, ok := store.(accountReader)
	if sender == nil || !ok {
		return sender
	}
	if _, wrapped := sender.(emailResolvingSender); wrapped {
		return sender
	}
	return emailResolvingSender{inner: sender, accounts: accounts}
}

func (e *Evaluator) verifiedEmail(sender ChannelSender) ChannelSender {
	return verifiedEmailSender(sender, e.store)
}

type emailResolvingSender struct {
	inner    ChannelSender
	accounts accountReader
}

func (s emailResolvingSender) Send(ctx context.Context, t alertchannels.Target, msg alertchannels.Message) error {
	if t.Kind != alertchannels.KindEmail || t.AccountID == "" {
		return s.inner.Send(ctx, t, msg)
	}
	acct, err := s.accounts.AccountByID(ctx, t.AccountID)
	if err != nil {
		return fmt.Errorf("read the channel's account: %w", err)
	}
	if !acct.EmailVerified() || acct.Email == "" {
		return errEmailUnverified
	}
	t.Email = acct.Email
	return s.inner.Send(ctx, t, msg)
}
