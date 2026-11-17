package apidserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/orgbilling"
	"github.com/onebox-faas/faas/pkg/billing"
	mailpkg "github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// A confirmed email change moves the billing email with it (ADR
// 20261108160000). Receipts, dunning mail and the billing portal's sign-in
// otherwise stay with the address the customer just left, which may be the
// mailbox they lost. Moved:
//   - the account's own billing customer (personal billing);
//   - every organization the account belongs to whose billing email is the
//     old address: the organization chose this member as its contact.
//
// The provider customer moves first, as on PUT /v1/orgs/{slug}/billing/email.
// A refusal (another customer holds the address, provider down) leaves that
// billing email as it was and mails the new address how to set it.

const billingEmailSyncTimeout = 30 * time.Second

// moveBillingEmailsInBackground runs moveBillingEmails off the request path.
func (s *server) moveBillingEmailsInBackground(ctx context.Context, change state.EmailChange) {
	//nolint:contextcheck // The work outlives the request; it keeps the request values and has its own timeout.
	ctx = context.WithoutCancel(ctx)
	if !s.resetMail.try(func() {
		ctx, cancel := context.WithTimeout(ctx, billingEmailSyncTimeout)
		defer cancel()
		s.moveBillingEmails(ctx, change)
	}) {
		s.log.Warn("email_change.billing_sync_busy", "account_id", change.AccountID)
		s.audit.Emit(ctx, "account.billing_email_sync_failed", &change.AccountID, map[string]any{"reason": "busy"})
	}
}

// moveBillingEmails moves each billing email that was the old address and
// returns the names of the subjects it could not move.
func (s *server) moveBillingEmails(ctx context.Context, change state.EmailChange) []string {
	acct, err := s.store.AccountByID(ctx, change.AccountID)
	if err != nil {
		s.log.Error("email_change.billing_sync_account", "account_id", change.AccountID, "err", err)
		return nil
	}
	syncer, _ := s.billingProvider.(billing.CustomerEmailUpdater)
	var failed []string
	if err := s.movePersonalBillingEmail(ctx, acct, syncer); err != nil {
		s.log.Error("email_change.billing_sync_personal", "account_id", acct.ID, "err", err)
		failed = append(failed, "your personal account")
	}
	orgs, err := s.store.ListOrgsForAccount(ctx, acct.ID)
	if err != nil {
		s.log.Error("email_change.billing_sync_orgs", "account_id", acct.ID, "err", err)
		return failed
	}
	provider, _ := providerIdentityName(s.billingProvider)
	for _, org := range orgs {
		if org.Personal {
			continue
		}
		subject, err := state.BillingResourceAccount(ctx, s.store, org.ID)
		if err != nil || !orgbilling.IsBillingContact(subject.Email, change.OldEmail) {
			continue
		}
		if err := orgbilling.ChangeEmail(ctx, s.store, org.ID, provider, change.NewEmail, syncer); err != nil {
			s.log.Error("email_change.billing_sync_org", "account_id", acct.ID, "org_id", org.ID, "err", err)
			s.audit.Emit(ctx, "account.billing_email_sync_failed", &acct.ID, map[string]any{"org_id": org.ID})
			failed = append(failed, fmt.Sprintf("organization %q (%s)", org.Name, org.Slug))
			continue
		}
		s.audit.Emit(ctx, "org.billing_email_changed", &acct.ID, map[string]any{"org_id": org.ID, "reason": "member_email_change"})
	}
	if len(failed) > 0 {
		s.mailBillingEmailSyncFailed(ctx, acct, failed)
	}
	return failed
}

// movePersonalBillingEmail moves the account's own billing customer, if it
// has one, to the account's (new) email.
func (s *server) movePersonalBillingEmail(ctx context.Context, acct state.Account, syncer billing.CustomerEmailUpdater) error {
	if syncer == nil {
		return nil
	}
	billingAcct, err := s.accountForActiveBillingProvider(ctx, acct)
	if err != nil {
		return err
	}
	if billingAcct.ProviderCustomerID == "" {
		return nil
	}
	billingAcct.Email = acct.Email
	if err := syncer.UpdateCustomerEmail(ctx, billingAcct); err != nil {
		s.audit.Emit(ctx, "account.billing_email_sync_failed", &acct.ID, map[string]any{"in_use": errors.Is(err, billing.ErrCustomerEmailInUse)})
		return err
	}
	s.audit.Emit(ctx, "account.billing_email_changed", &acct.ID, map[string]any{"reason": "email_change"})
	return nil
}

// mailBillingEmailSyncFailed tells the new address which billing emails
// still point at the old one.
func (s *server) mailBillingEmailSyncFailed(ctx context.Context, acct state.Account, failed []string) {
	subject, body := mailpkg.BillingEmailSyncFailedBody(acct.Email, failed, wire.DocsBaseURL+"/billing")
	s.sendSecurityNotice(ctx, acct, "billing_email_sync", subject, body)
}
