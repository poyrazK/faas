package apidserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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
	cooldown, ok := s.store.(state.CustomDomainCertFailureEmailAttemptStore)
	if !ok {
		return fmt.Errorf("store does not implement custom-domain cert-failure email cooldown")
	}
	d, err := s.store.DomainByName(parent, domain)
	if err != nil {
		return err
	}
	if expected, bound := state.CustomDomainProbe(parent); bound && !state.SameCustomDomainClaim(expected, d) {
		return state.ErrStaleCustomDomainProbe
	}
	if d.CertStatus != state.CustomDomainCertFailed {
		return nil
	}
	parent = state.WithCustomDomainProbe(parent, d)
	app, err := s.store.AppByID(parent, d.AppID)
	if err != nil {
		return err
	}
	recipients := certFailureRecipients(parent, s.store, app, log)
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
	mailTimeout := time.Duration(api.MailRetryMaxWallClockMS) * time.Millisecond
	mailCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), mailTimeout)
	defer cancel()
	err = s.mailer.Send(mailCtx, Message{
		To:       recipients,
		Subject:  subject,
		TextBody: body,
		// Provider idempotency is useful across a retry, while the durable
		// claim remains the cross-replica duplicate guard.
		MessageID: mail.CertificateIssuanceFailedKey(d.AppID, d.ChallengeToken, recipients, subject, body),
	})
	if err != nil {
		receiptCtx, stop := context.WithTimeout(context.WithoutCancel(parent), mailTimeout)
		defer stop()
		return errors.Join(err, cooldown.ReleaseCustomDomainCertFailureEmail(receiptCtx, d, now))
	}
	return nil
}

// certFailureRecipients follows the app's tenant, independently of its creator
// and billing namespace. A company notice reaches only its current owners and
// admins; a personal notice reaches only its owner.
func certFailureRecipients(ctx context.Context, store state.Store, app state.App, log *slog.Logger) []string {
	accounts := []string{app.AccountID}
	if app.OrgID != "" {
		org, err := store.OrgByID(ctx, app.OrgID)
		if err != nil || org.DeletedPending || org.Status == state.OrgStatusDeletedPending {
			return nil
		}
		if org.Personal {
			if org.PersonalOwnerAccountID == nil || *org.PersonalOwnerAccountID != app.AccountID {
				return nil
			}
		} else {
			members, err := store.ListOrgMembers(ctx, org.ID)
			if err != nil {
				log.Warn("cert_issuance_failed: list app organization members", "org_id", org.ID, "err", err)
				return nil
			}
			accounts = nil
			for _, member := range members {
				if member.RemovedAt == nil && (member.Role == state.OrgRoleOwner || member.Role == state.OrgRoleAdmin) {
					accounts = append(accounts, member.AccountID)
				}
			}
		}
	}
	seen := make(map[string]bool)
	var recipients []string
	for _, id := range accounts {
		account, err := store.AccountByID(ctx, id)
		if err != nil {
			if !errors.Is(err, state.ErrNotFound) {
				log.Warn("cert_issuance_failed: load recipient", "account_id", id, "err", err)
			}
			continue
		}
		email := strings.TrimSpace(account.Email)
		if email != "" && !seen[strings.ToLower(email)] {
			seen[strings.ToLower(email)] = true
			recipients = append(recipients, email)
		}
	}
	sort.Strings(recipients)
	return recipients
}

func certFailureDashboardURL(base, slug, domain string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = "https://gregale.dev"
	}
	return base + "/dashboard/apps/" + url.PathEscape(slug) + "/domains/" + url.PathEscape(domain)
}
