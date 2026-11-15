package apidserver

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	mailpkg "github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// A mailbox alone used to be enough to turn off two-factor: reset the
// password, ask for the email-assisted disable, wait out the 24 hours. The
// owner could not stop it. Now proving the second factor cancels any pending
// disable, and a session that holds the factor can cancel it from the account
// page (F14-1, ADR-20261115100000).

// cancelPendingMFADisable ends every pending email-assisted disable after acct
// proved the second factor or pressed Cancel, and tells the owner; via is
// "authenticator", "recovery_code" or "account_page". Best effort: a failure is logged and the sign-in proceeds.
func (s *server) cancelPendingMFADisable(ctx context.Context, acct state.Account, via string) int {
	st, ok := s.store.(state.MFADisablePendingStore)
	if !ok {
		return 0
	}
	n, err := st.CancelPendingMFADisable(ctx, acct.ID)
	if err != nil {
		s.log.Warn("mfa.disable_email.cancel", "account_id", acct.ID, "err", err)
		return 0
	}
	if n == 0 {
		return 0
	}
	s.audit.Emit(ctx, "account.mfa_disable_email_cancelled", &acct.ID, map[string]any{"via": via, "cancelled": n})
	subject, body := mailpkg.MFADisableEmailCancelledBody(acct.Email, mfaDisableCancelHow[via], time.Now())
	s.sendSecurityNotice(ctx, acct, "mfa_disable_cancelled", subject, body)
	return n
}

// mfaDisableCancelHow phrases a cancellation's cause for the owner's mail.
var mfaDisableCancelHow = map[string]string{
	"authenticator": "after a sign-in with your authenticator app",
	"recovery_code": "after a sign-in with one of your recovery codes",
	"account_page":  "from the two-factor page of a signed-in session",
}

// pendingMFADisableAt is when the earliest pending email-assisted disable on
// acct was requested, or nil.
func (s *server) pendingMFADisableAt(ctx context.Context, acct state.Account) *time.Time {
	st, ok := s.store.(state.MFADisablePendingStore)
	if !ok {
		return nil
	}
	at, err := st.PendingMFADisableRequestedAt(ctx, acct.ID)
	if err != nil {
		s.log.Warn("mfa.disable_email.pending", "account_id", acct.ID, "err", err)
		return nil
	}
	return at
}

// mfaDisableEmailCancel is POST /v1/account/mfa/disable-email-cancel.
func (s *server) mfaDisableEmailCancel(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if err := s.verifySessionCSRF(r, "mfa_disable_email_cancel", acct.ID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	n := s.cancelPendingMFADisable(r.Context(), acct, "account_page")
	writeJSON(w, http.StatusOK, api.MFADisableEmailCancelResponse{Cancelled: n})
}
