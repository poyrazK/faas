package apidserver

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mailoutbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) queueMFADisableEmail(w http.ResponseWriter, r *http.Request, acct state.Account) bool {
	if s.mfaDisableMailOutboxRequested == nil || !s.mfaDisableMailOutboxRequested() {
		return false
	}
	if s.deletionMailOutbox == nil || s.deletionMailOutbox.QueueMFADisable(r.Context(), acct.ID, acct.Email) != nil {
		s.log.Error("mfa.disable_email.outbox_save_failed", "account_id", acct.ID)
		api.WriteProblem(w, api.ErrCapacity("could not create MFA disable request"))
		return true
	}
	s.audit.Emit(r.Context(), "account.mfa_disable_email_requested", &acct.ID, nil)
	writeJSON(w, http.StatusOK, api.MFADisableEmailResponse{})
	return true
}

func (s *server) consumeDurableMFADisableEmail(w http.ResponseWriter, r *http.Request, acct state.Account, raw []byte, alertRequested bool) bool {
	if alertRequested {
		return s.consumeMFADisabledAlertCredential(w, r, acct, raw)
	}
	if s.mfaDisableCredentials == nil {
		if len(raw) == api.MFADisableTokenBytes {
			s.writeMFADisableEmailInvalid(w, r, acct, "token_invalid")
			return true
		}
		return false
	}
	readyAt, err := s.mfaDisableCredentials.Redeem(r.Context(), acct.ID, api.HashToken(raw))
	if errors.Is(err, mailoutbox.ErrMFADisableUnknown) {
		// A legacy age-sealed token cannot be a raw 32-byte credential. Keep
		// pruned/unknown durable links invalid without requiring a host key.
		if len(raw) == api.MFADisableTokenBytes {
			s.writeMFADisableEmailInvalid(w, r, acct, "token_invalid")
			return true
		}
		return false
	}
	switch {
	case err == nil:
		s.audit.Emit(r.Context(), "account.mfa_disabled", &acct.ID, map[string]any{"method": "email"})
		writeJSON(w, http.StatusOK, api.MFADisableEmailConfirmResponse{})
	case errors.Is(err, mailoutbox.ErrMFADisableCooldown):
		s.writeMFADisableCooldown(w, readyAt)
	case errors.Is(err, mailoutbox.ErrMFADisableInvalid):
		s.writeMFADisableEmailInvalid(w, r, acct, "token_invalid")
	default:
		s.log.Error("mfa.disable_email.transaction_failed")
		api.WriteProblem(w, api.ErrCapacity("could not confirm MFA disable request"))
	}
	return true
}

func (s *server) writeMFADisableCooldown(w http.ResponseWriter, readyAt time.Time) {
	retryAfter := int(math.Ceil(time.Until(readyAt).Seconds()))
	if retryAfter < 1 {
		retryAfter = 1
	}
	api.WriteProblem(w, api.NewProblem(http.StatusTooEarly, api.CodeMFADisableCooldown,
		"MFA disable cooldown active", "confirm this request after the 24-hour waiting period").WithHeader("Retry-After", strconv.Itoa(retryAfter)))
}
