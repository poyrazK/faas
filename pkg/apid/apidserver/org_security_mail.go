package apidserver

import (
	"context"
	"strings"
	"time"

	mailpkg "github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// mailOrgSecurityEvent mails the organization's owners and the members in
// also that change happened (ADR 20261111130000). Ownership transfers,
// removals, role changes and admin invitations were recorded only in the
// activity feed, so a compromised admin or owner session could reshape an
// organization unnoticed. kind names the event for the per-account mail
// throttle, so one event does not swallow another's notice. Sends run off
// the request path; failures log.
func (s *server) mailOrgSecurityEvent(ctx context.Context, kind, orgID, change string, also ...string) {
	ctx = state.WithoutOrgAccessFence(ctx)
	org, err := s.store.OrgByID(ctx, orgID)
	if err != nil {
		s.log.Warn("org_security_mail.org", "org", orgID, "err", err)
		return
	}
	recipients := map[string]bool{}
	for _, id := range also {
		if id != "" {
			recipients[id] = true
		}
	}
	members, err := s.store.ListOrgMembers(ctx, orgID)
	if err != nil {
		s.log.Warn("org_security_mail.members", "org", orgID, "err", err)
	}
	for _, m := range members {
		if m.Role == state.OrgRoleOwner && m.RemovedAt == nil {
			recipients[m.AccountID] = true
		}
	}
	now := time.Now()
	for id := range recipients {
		acct, err := s.store.AccountByID(ctx, id)
		if err != nil || acct.Email == "" {
			continue
		}
		subject, body := mailpkg.OrgSecurityEventBody(acct.Email, orgDisplayName(org), change, now)
		s.sendSecurityNotice(ctx, acct, kind, subject, body)
	}
}

// mailOrgBillingEmailChanged tells the previous billing address and the
// owners that invoices and billing sign-in links now go to newEmail: the
// change was audit-only, so a member who can change billing could redirect
// the portal sign-in silently (ADR 20261111130000).
func (s *server) mailOrgBillingEmailChanged(ctx context.Context, orgID, oldEmail, newEmail string) {
	ctx = state.WithoutOrgAccessFence(ctx)
	org, err := s.store.OrgByID(ctx, orgID)
	if err != nil {
		s.log.Warn("org_billing_mail.org", "org", orgID, "err", err)
		return
	}
	now := time.Now()
	sent := map[string]bool{strings.ToLower(newEmail): true}
	if oldEmail != "" && !sent[strings.ToLower(oldEmail)] {
		sent[strings.ToLower(oldEmail)] = true
		subject, body := mailpkg.OrgBillingEmailChangedBody(oldEmail, orgDisplayName(org), newEmail, now)
		s.sendNoticeInBackground(ctx, "org_billing_email_changed", orgID, Message{To: []string{oldEmail}, Subject: subject, TextBody: body})
	}
	members, err := s.store.ListOrgMembers(ctx, orgID)
	if err != nil {
		s.log.Warn("org_billing_mail.members", "org", orgID, "err", err)
		return
	}
	for _, m := range members {
		if m.Role != state.OrgRoleOwner || m.RemovedAt != nil {
			continue
		}
		owner, err := s.store.AccountByID(ctx, m.AccountID)
		if err != nil || owner.Email == "" || sent[strings.ToLower(owner.Email)] {
			continue
		}
		sent[strings.ToLower(owner.Email)] = true
		subject, body := mailpkg.OrgBillingEmailChangedBody(owner.Email, orgDisplayName(org), newEmail, now)
		s.sendSecurityNotice(ctx, owner, "org_billing_email_changed", subject, body)
	}
}

// orgMemberLabel is a member's masked email for a change description, or
// "a member" when the account cannot be read.
func (s *server) orgMemberLabel(ctx context.Context, accountID string) string {
	acct, err := s.store.AccountByID(state.WithoutOrgAccessFence(ctx), accountID)
	if err != nil || acct.Email == "" {
		return "a member"
	}
	return mailpkg.MaskEmail(acct.Email)
}

func orgDisplayName(org state.Org) string {
	if name := strings.TrimSpace(org.Name); name != "" {
		return name
	}
	return org.Slug
}
