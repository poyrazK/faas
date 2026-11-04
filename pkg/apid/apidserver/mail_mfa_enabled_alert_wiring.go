package apidserver

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mailoutbox"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) mfaEnabledAlertRequested() bool {
	return s.mfaEnabledAlertOutboxRequested != nil && s.mfaEnabledAlertOutboxRequested()
}

func (s *server) saveMFAEnrollment(w http.ResponseWriter, r *http.Request, acct state.Account, sealed []byte, hashes [][]byte) bool {
	if !s.mfaEnabledAlertRequested() {
		if err := s.store.SetMFASecret(r.Context(), acct.ID, sealed, hashes); err != nil {
			s.log.Error("mfa.enroll.persist", "err", err.Error())
			api.WriteProblem(w, api.ErrCapacity("could not persist TOTP enrollment"))
			return false
		}
		return true
	}
	err := internalmail.ErrDisabled
	if s.deletionMailOutbox != nil {
		err = s.deletionMailOutbox.SavePendingMFAEnrollment(r.Context(), acct.ID, acct.Email, sealed, hashes)
	}
	if err != nil {
		s.writeMFAEnabledAlertError(w, r, acct, err)
		return false
	}
	return true
}

// Return true once selected, including failures. A late withdrawal or missing
// configuration must not fall back to enrollment without its security alert.
func (s *server) tryMFAEnabledAlert(w http.ResponseWriter, r *http.Request, acct state.Account, code string) bool {
	if !s.mfaEnabledAlertRequested() {
		return false
	}
	err := internalmail.ErrDisabled
	if s.deletionMailOutbox != nil {
		err = s.deletionMailOutbox.ConfirmMFAEnrollmentWithAlert(r.Context(), acct.ID, acct.Email, code, accountIdentities())
	}
	if err != nil {
		s.writeMFAEnabledAlertError(w, r, acct, err)
		return true
	}
	s.finishMFAConfirmation(w, r, acct)
	return true
}

func (s *server) writeMFAEnabledAlertError(w http.ResponseWriter, r *http.Request, acct state.Account, err error) {
	switch {
	case errors.Is(err, mailoutbox.ErrMFAEnrollmentInvalidCode):
		s.totp.fail(acct.ID, time.Now())
		s.audit.Emit(r.Context(), "account.mfa_confirm_failed", &acct.ID, map[string]any{"reason": "code_mismatch"})
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeMFAInvalidCode, "Invalid code", "the TOTP code did not match"))
	case errors.Is(err, mailoutbox.ErrMFAEnrollmentAlreadyEnabled):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Already enrolled", "call /v1/account/mfa/disable before re-enrolling"))
	case errors.Is(err, mailoutbox.ErrMFAEnrollmentUnavailable):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not enrolled", "complete /v1/account/mfa/enroll first"))
	default:
		s.log.Error("mfa_enabled_alert.transaction_failed", "account_id", acct.ID)
		api.WriteProblem(w, api.ErrCapacity("could not finalize enrollment"))
	}
}
