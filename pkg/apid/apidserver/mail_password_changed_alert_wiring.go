package apidserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mailoutbox"
	"github.com/onebox-faas/faas/pkg/auth"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) passwordChangedAlertRequested() bool {
	return s.passwordChangedAlertOutboxRequested != nil && s.passwordChangedAlertOutboxRequested()
}

// Selection happens once. A missing configuration or late withdrawal fails the
// selected change; it cannot fall back to an unalerted password mutation.
func (s *server) tryPasswordChangedAlert(w http.ResponseWriter, r *http.Request, acct state.Account, plain string) bool {
	if !s.passwordChangedAlertRequested() {
		return false
	}
	if s.deletionMailOutbox == nil {
		s.writePasswordChangedAlertError(w, r, acct, internalmail.ErrDisabled)
		return true
	}
	ts, has := authmw.StepUpFrom(r)
	fresh := has && !ts.IsZero() && time.Since(ts) <= setPasswordStepUpTTL
	change, err := s.deletionMailOutbox.PreparePasswordChange(r.Context(), acct.ID, acct.Email, r.FormValue("current_password"), fresh)
	if err != nil {
		s.writePasswordChangedAlertError(w, r, acct, err)
		return true
	}
	phc, err := auth.Encode(plain)
	if err != nil {
		s.writePasswordChangedAlertError(w, r, acct, err)
		return true
	}
	if err := s.deletionMailOutbox.CommitPasswordChange(r.Context(), change, phc); err != nil {
		s.writePasswordChangedAlertError(w, r, acct, err)
		return true
	}
	s.finishSetPassword(w, r, acct, change.Proof(), change.Replacing())
	return true
}

func (s *server) writePasswordChangedAlertError(w http.ResponseWriter, r *http.Request, acct state.Account, err error) {
	switch {
	case errors.Is(err, mailoutbox.ErrPasswordChangeMFARequired):
		s.audit.Emit(r.Context(), "auth.mfa_gate_hit", &acct.ID, map[string]any{"path": setPasswordPath, "method": http.MethodPost})
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeMFARequired, "MFA required", "complete /v1/account/mfa/enroll or /v1/account/mfa/confirm to access this route"))
	case errors.Is(err, mailoutbox.ErrPasswordChangeStepUp):
		reason := "missing"
		if ts, has := authmw.StepUpFrom(r); has && !ts.IsZero() {
			reason = "expired"
		}
		s.audit.Emit(r.Context(), "auth.step_up_required", &acct.ID, map[string]any{"path": setPasswordPath, "method": http.MethodPost, "reason": reason, "ttl_sec": int(setPasswordStepUpTTL.Seconds())})
		api.WriteProblem(w, api.ErrStepUpRequired())
	case errors.Is(err, mailoutbox.ErrPasswordChangeInvalid):
		s.audit.Emit(r.Context(), "account.password_set_denied", &acct.ID, map[string]any{"reason": "current_password"})
		api.WriteProblem(w, api.ErrInvalidCredentials())
	default:
		s.log.Error("password_changed_alert.transaction_failed", "account_id", acct.ID)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error", "Internal Error", "failed to set password"))
	}
}

func (s *server) applyPasswordResetWithAlert(ctx context.Context, hash []byte, phc string) (string, *api.Problem) {
	if s.deletionMailOutbox == nil {
		return "", s.passwordResetAlertError(internalmail.ErrDisabled)
	}
	id, err := s.deletionMailOutbox.ResetPasswordWithAlert(ctx, hash, phc)
	if errors.Is(err, mailoutbox.ErrPasswordResetUnknown) {
		id, err = s.deletionMailOutbox.ResetLegacyPasswordWithAlert(ctx, hash, phc)
	}
	if err != nil {
		return "", s.passwordResetAlertError(err)
	}
	return id, nil
}

func (s *server) passwordResetAlertError(err error) *api.Problem {
	if errors.Is(err, mailoutbox.ErrPasswordResetInvalid) {
		return api.ErrResetTokenInvalid()
	}
	s.log.Error("password_reset.alert_transaction_failed")
	return api.NewProblem(http.StatusInternalServerError, "internal_error", "Internal Error", "failed to reset password")
}
