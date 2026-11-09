package apidserver

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	mailpkg "github.com/onebox-faas/faas/pkg/mail"
)

// Operator MFA reset (ADR 20261108160000). A customer who lost both the
// authenticator (and its recovery codes) and the mailbox has no self-serve
// way back in: the email disable path mails the lost mailbox. After the
// identity checks in docs/runbooks/account-recovery-lost-factors.md an
// operator clears the factor with `gregalectl accounts reset-mfa`. The
// reset also signs out every browser and forgets remembered browsers, and
// mails the account's address so a takeover attempt is noticed. Operator
// accounts are refused: they recover through
// docs/runbooks/operator-credential-compromise.md.

// operatorMFAResetRefusal answers why the target cannot be reset, or nil.
func (s *server) operatorMFAResetRefusal(ctx context.Context, targetID string) *api.Problem {
	target, err := s.store.AccountByID(ctx, targetID)
	switch {
	case err != nil:
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Account not found", "no account with that id")
	case s.isOperator(target):
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "operator account",
			"operator accounts recover through the operator credential compromise runbook, not reset-mfa")
	case !target.MFAEnrolled():
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "no two-factor authentication",
			"the account has no enrolled authenticator to reset")
	}
	return nil
}

// operatorResetMFA clears the factor, signs out every browser, forgets
// remembered browsers and mails the account. It returns the number of
// revoked sessions.
func (s *server) operatorResetMFA(ctx context.Context, targetID string) (int, error) {
	if err := s.store.ClearMFA(ctx, targetID); err != nil {
		return 0, fmt.Errorf("clear mfa: %w", err)
	}
	revoked, err := s.store.RevokeAllSessions(ctx, targetID, uuid.Nil.String())
	if err != nil {
		return 0, fmt.Errorf("revoke sessions after mfa reset: %w", err)
	}
	s.forgetTrustedBrowsers(ctx, targetID, "operator_mfa_reset")
	s.audit.Emit(ctx, "account.mfa_disabled", &targetID, map[string]any{"method": "operator"})
	if target, err := s.store.AccountByID(ctx, targetID); err == nil {
		subject, body := mailpkg.OperatorMFAResetBody(target.Email, time.Now())
		s.sendNoticeInBackground(ctx, "operator_mfa_reset", targetID, Message{To: []string{target.Email}, Subject: subject, TextBody: body})
	}
	return revoked, nil
}
