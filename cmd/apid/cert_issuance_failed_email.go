package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// sendCertIssuanceFailedEmail sends the F2 notice after the durable store
// gate confirms the domain has been failed for at least 15 minutes. The
// helper is called from the existing domain-doctor pass, so no second poller
// or LISTEN lifecycle is needed.
func (s *server) sendCertIssuanceFailedEmail(parent context.Context, log *slog.Logger, domain string) error {
	if s == nil || s.store == nil || s.mailer == nil {
		return nil
	}
	cooldown, ok := s.store.(state.CustomDomainCertFailureEmailCooldownStore)
	if !ok {
		return fmt.Errorf("store does not implement custom-domain cert-failure email cooldown")
	}
	d, err := s.store.DomainByName(parent, domain)
	if err != nil {
		return err
	}
	if d.CertStatus != state.CustomDomainCertFailed {
		return nil
	}
	app, err := s.store.AppByID(parent, d.AppID)
	if err != nil {
		return err
	}
	acct, err := s.store.AccountByID(parent, app.AccountID)
	if err != nil {
		return err
	}
	recipients := certFailureRecipients(parent, s.store, acct, log)
	if len(recipients) == 0 {
		return nil
	}
	now := time.Now().UTC()
	claimed, err := cooldown.ClaimCustomDomainCertFailureEmail(parent, d.Domain, now)
	if err != nil || !claimed {
		return err
	}
	subject, body := mail.CertificateIssuanceFailedBody(mail.CertificateIssuanceFailure{
		Domain:       d.Domain,
		AppSlug:      app.Slug,
		LastError:    d.CertLastError,
		DashboardURL: certFailureDashboardURL(s.cliAuthURLBase, app.Slug, d.Domain),
		FailedAt:     d.CertFailedAt,
	})
	mailCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	return s.mailer.Send(mailCtx, Message{
		To:       recipients,
		Subject:  subject,
		TextBody: body,
		// Provider idempotency is useful across a retry, while the durable
		// claim remains the cross-replica duplicate guard.
		MessageID: "cert_issuance_failed:" + d.Domain + ":" + now.Format("20060102"),
	})
}

// certFailureRecipients includes the app account owner and active owner/admin
// members of organizations that account belongs to. The account owner is
// always included even when an older domain row has no org_id association.
func certFailureRecipients(ctx context.Context, store state.Store, owner state.Account, log *slog.Logger) []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(email string) {
		email = strings.TrimSpace(email)
		if email == "" {
			return
		}
		key := strings.ToLower(email)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, email)
	}
	add(owner.Email)
	orgs, err := store.ListOrgsForAccount(ctx, owner.ID)
	if err != nil {
		if log != nil {
			log.Warn("cert_issuance_failed: list owner orgs", "account_id", owner.ID, "err", err)
		}
	} else {
		for _, org := range orgs {
			members, memberErr := store.ListOrgMembers(ctx, org.ID)
			if memberErr != nil {
				if log != nil {
					log.Warn("cert_issuance_failed: list org members", "org_id", org.ID, "err", memberErr)
				}
				continue
			}
			for _, member := range members {
				if member.RemovedAt != nil || (member.Role != state.OrgRoleOwner && member.Role != state.OrgRoleAdmin) {
					continue
				}
				memberAcct, accountErr := store.AccountByID(ctx, member.AccountID)
				if accountErr != nil && !errors.Is(accountErr, state.ErrNotFound) {
					if log != nil {
						log.Warn("cert_issuance_failed: load org recipient", "account_id", member.AccountID, "err", accountErr)
					}
					continue
				}
				add(memberAcct.Email)
			}
		}
	}
	sort.Strings(out)
	return out
}

func certFailureDashboardURL(base, slug, domain string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = "https://gregale.dev"
	}
	return base + "/dashboard/apps/" + url.PathEscape(slug) + "/domains/" + url.PathEscape(domain)
}
