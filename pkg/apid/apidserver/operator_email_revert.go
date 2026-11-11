package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	mailpkg "github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// Operator email revert (ADR 20261110100000). Someone who took over a
// session and confirmed an email change to an address they control owns
// the account's mail from then on: password reset and sign-in links go to
// them. After the identity checks in
// docs/runbooks/account-recovery-email-changed.md an operator moves the
// account back with `gregalectl accounts revert-email`. Everything the
// taker could still use ends with it: sessions, remembered browsers,
// interactive sign-in keys, pending grants and the links mailed to their
// address. The restored address is mailed, with the API keys that still
// work.

// operatorRevertEmail moves targetID back to the address its last confirmed
// change replaced. It returns the number of revoked sessions, or a problem
// for a refusal.
func (s *server) operatorRevertEmail(r *http.Request, targetID string) (int, *api.Problem, error) {
	ctx := r.Context()
	target, err := s.store.AccountByID(ctx, targetID)
	if err != nil {
		return 0, nil, fmt.Errorf("load account: %w", err)
	}
	if s.isOperator(target) {
		return 0, api.NewProblem(http.StatusConflict, api.CodeConflict, "operator account",
			"operator accounts recover through the operator credential compromise runbook, not revert-email"), nil
	}
	st, ok := s.store.(state.AccountEmailRevertStore)
	if !ok {
		return 0, nil, errors.New("store cannot revert email changes")
	}
	change, err := st.RevertLastEmailChange(ctx, targetID, api.EmailChangeRevertWindow)
	switch {
	case errors.Is(err, state.ErrNotFound):
		return 0, api.NewProblem(http.StatusConflict, api.CodeConflict, "no email change to revert",
			fmt.Sprintf("the account has no confirmed email change in the last %d days, or has changed its email again since",
				int(api.EmailChangeRevertWindow.Hours()/24))), nil
	case errors.Is(err, state.ErrConflict):
		return 0, api.NewProblem(http.StatusConflict, api.CodeConflict, "previous address in use",
			"another Gregale account now uses the previous address"), nil
	case err != nil:
		return 0, nil, fmt.Errorf("revert email change: %w", err)
	}
	return s.finishOperatorEmailRevert(r, target, change), nil, nil
}

// finishOperatorEmailRevert ends what the taker could still use and mails
// the restored address.
func (s *server) finishOperatorEmailRevert(r *http.Request, target state.Account, change state.EmailChange) int {
	ctx := r.Context()
	s.forgetTrustedBrowsers(ctx, target.ID, "operator_email_revert")
	s.endPendingGrants(ctx, target.ID, "operator_email_revert")
	s.removeForeignProviderLinks(ctx, target.ID, "operator_email_revert")
	revoked, err := s.store.RevokeAllSessions(ctx, target.ID, uuid.Nil.String())
	if err != nil {
		s.log.Warn("email revert: revoke sessions failed", "account", target.ID, "err", err)
	}
	s.revokeInteractiveSignInKeys(r, target, "operator_email_revert")
	s.audit.Emit(ctx, "account.email_reverted", &target.ID, map[string]any{"method": "operator"})
	// Billing emails follow the address back, as they followed it away.
	s.moveBillingEmailsInBackground(ctx, state.EmailChange{AccountID: target.ID, OldEmail: change.NewEmail, NewEmail: change.OldEmail})
	restored := target
	restored.Email = change.OldEmail
	s.mailEmailReverted(ctx, restored, change.NewEmail)
	return revoked
}

func (s *server) mailEmailReverted(ctx context.Context, acct state.Account, removed string) {
	subject, body := mailpkg.OperatorEmailRevertedBody(acct.Email, removed, time.Now())
	s.sendNoticeInBackground(ctx, "operator_email_revert", acct.ID, Message{To: []string{acct.Email}, Subject: subject, TextBody: body})
	s.mailKeysLeftAfterReset(ctx, acct, s.keysLeftAfterReset(ctx, acct))
}
