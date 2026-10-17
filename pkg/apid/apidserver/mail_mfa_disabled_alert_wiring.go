package apidserver

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mailoutbox"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authcode"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) mfaDisabledAlertRequested() bool {
	return s.mfaDisabledAlertOutboxRequested != nil && s.mfaDisabledAlertOutboxRequested()
}

func (s *server) tryMFADisabledAlert(w http.ResponseWriter, r *http.Request, acct state.Account, req api.MFADisableRequest) bool {
	if !s.mfaDisabledAlertRequested() {
		return false
	}
	if req.Password != "" {
		if pending, _ := authmw.MFAPendingFrom(r); pending {
			s.audit.Emit(r.Context(), "account.mfa_disable_failed", &acct.ID, map[string]any{"reason": "pending_session_password"})
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeMFARequired,
				"MFA required", "verify a TOTP code before disabling MFA with your password, or disable it with a recovery code"))
			return true
		}
	}
	err := internalmail.ErrDisabled
	method := "password"
	if req.Password == "" {
		method = "recovery_code"
	}
	if s.deletionMailOutbox != nil {
		if req.Password != "" {
			err = s.deletionMailOutbox.DisableMFAPasswordWithAlert(r.Context(), acct.ID, acct.Email, req.Password)
		} else {
			var hash []byte
			hash, err = authcode.HashRecoveryCode(req.RecoveryCode)
			defer clear(hash)
			if err == nil {
				err = s.deletionMailOutbox.DisableMFARecoveryWithAlert(r.Context(), acct.ID, acct.Email, hash)
			}
		}
	}
	if err != nil {
		s.writeMFADisabledAlertError(w, r, acct, time.Time{}, err, false)
		return true
	}
	s.audit.Emit(r.Context(), "account.mfa_disabled", &acct.ID, map[string]any{"method": method})
	writeJSON(w, http.StatusOK, api.MFADisableResponse{})
	return true
}

func (s *server) consumeMFADisabledAlertCredential(w http.ResponseWriter, r *http.Request, acct state.Account, raw []byte) bool {
	var ready time.Time
	err := internalmail.ErrDisabled
	if s.deletionMailOutbox != nil {
		ready, err = s.deletionMailOutbox.ConfirmMFADisableWithAlert(r.Context(), acct.ID, api.HashToken(raw))
	}
	if errors.Is(err, mailoutbox.ErrMFADisableUnknown) {
		if len(raw) != api.MFADisableTokenBytes {
			return false
		}
		err = mailoutbox.ErrMFADisableInvalid
	}
	s.completeMFADisabledEmailAlert(w, r, acct, ready, err)
	return true
}

func (s *server) confirmLegacyMFADisabledAlert(w http.ResponseWriter, r *http.Request, acct state.Account, sealed []byte) {
	var ready time.Time
	err := internalmail.ErrDisabled
	if s.deletionMailOutbox != nil {
		ready, err = s.deletionMailOutbox.ConfirmLegacyMFADisableWithAlert(r.Context(), acct.ID, acct.Email, api.HashToken(sealed))
	}
	s.completeMFADisabledEmailAlert(w, r, acct, ready, err)
}

func (s *server) completeMFADisabledEmailAlert(w http.ResponseWriter, r *http.Request, acct state.Account, ready time.Time, err error) {
	if err != nil {
		s.writeMFADisabledAlertError(w, r, acct, ready, err, true)
		return
	}
	s.audit.Emit(r.Context(), "account.mfa_disabled", &acct.ID, map[string]any{"method": "email"})
	writeJSON(w, http.StatusOK, api.MFADisableEmailConfirmResponse{})
}

func (s *server) writeMFADisabledAlertError(w http.ResponseWriter, r *http.Request, acct state.Account, ready time.Time, err error, email bool) {
	switch {
	case errors.Is(err, mailoutbox.ErrMFADisableCooldown):
		s.writeMFADisableCooldown(w, ready)
	case errors.Is(err, mailoutbox.ErrMFADisableInvalid):
		if email {
			s.writeMFADisableEmailInvalid(w, r, acct, "token_invalid")
			return
		}
		s.audit.Emit(r.Context(), "account.mfa_disable_failed", &acct.ID, map[string]any{"reason": "credentials_mismatch"})
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeMFAInvalidCode,
			"Invalid credentials", "the credentials did not match the current MFA enrollment"))
	default:
		s.log.Error("mfa.disabled_alert.transaction_failed")
		api.WriteProblem(w, api.ErrCapacity("could not clear MFA state"))
	}
}
