package apidserver

import (
	"context"
	"time"

	mailpkg "github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// Security alert kinds for the per-account mail budget. Each has its own
// budget, so sign-in-failure or reset mail cannot use up the one an attacker
// would need to stay silent.
const (
	securityAlertPasswordChanged = "alert_password_changed"
	securityAlertMFAEnabled      = "alert_mfa_enabled"
	securityAlertMFADisabled     = "alert_mfa_disabled"
)

// sendInlineSecurityAlert mails the owner that a credential changed, on the
// paths that run while the durable alert outboxes are off (bug hunt H99).
// Those paths used to write only an audit row, so a takeover (new password,
// attacker's authenticator, MFA switched off) sent the owner nothing. The
// send is best effort and detached from the request, like the password
// reset mail: the change has committed, and a mail failure is logged, not
// returned. The body is the same frozen template the outbox sends.
func (s *server) sendInlineSecurityAlert(ctx context.Context, acct state.Account, kind string, render func(at time.Time) (subject, body string)) {
	if s.mailer == nil || acct.Email == "" {
		return
	}
	if !s.allowSecurityMail(ctx, acct, kind) {
		return
	}
	subject, body := render(time.Now().UTC())
	s.audit.Emit(ctx, "account.security_alert_sent", &acct.ID, map[string]any{"kind": kind, "path": "inline"})
	go func() {
		mailCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := s.mailer.Send(mailCtx, Message{To: []string{acct.Email}, Subject: subject, TextBody: body}); err != nil {
			s.log.Error("security_alert.inline_send_failed", "account", acct.ID, "kind", kind, "err", err)
		}
	}()
}

// alertPasswordChangedInline: method is "dashboard" or "password_reset", as
// the outbox records it.
func (s *server) alertPasswordChangedInline(ctx context.Context, acct state.Account, method string) {
	s.sendInlineSecurityAlert(ctx, acct, securityAlertPasswordChanged, func(at time.Time) (string, string) {
		return mailpkg.PasswordChangedBodyV1(acct.Email, method, at)
	})
}

func (s *server) alertMFAEnabledInline(ctx context.Context, acct state.Account) {
	s.sendInlineSecurityAlert(ctx, acct, securityAlertMFAEnabled, func(at time.Time) (string, string) {
		return mailpkg.MFAEnabledBodyV1(acct.Email, at)
	})
}

// alertMFADisabledInline: method is "password", "recovery_code" or "email".
func (s *server) alertMFADisabledInline(ctx context.Context, acct state.Account, method string) {
	s.sendInlineSecurityAlert(ctx, acct, securityAlertMFADisabled, func(at time.Time) (string, string) {
		return mailpkg.MFADisabledBodyV1(acct.Email, method, at)
	})
}
