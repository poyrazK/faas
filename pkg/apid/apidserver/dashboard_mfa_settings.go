package apidserver

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

// dashboardMFASettings is reachable after the first factor so a customer can
// enroll or recover without an already-verified session or CLI API key.
func (s *server) dashboardMFASettings(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	_, passwordErr := s.store.AccountPasswordByAccountID(r.Context(), acct.ID)
	if passwordErr != nil && !errors.Is(passwordErr, state.ErrNotFound) {
		renderProblem(w, s.log, passwordErr)
		return
	}
	page := dashboard.Page{
		Title: "Two-factor authentication", Body: "mfa_settings", Account: acctViewFrom(acct),
		Data: map[string]any{"Enrolled": acct.MFAEnrolled(), "PendingSetup": len(acct.MFASecretEncrypted) > 0, "HasPassword": passwordErr == nil, "Next": dashboardMFANext(r.URL.Query().Get("next"))},
	}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, s.log, err)
	}
}
