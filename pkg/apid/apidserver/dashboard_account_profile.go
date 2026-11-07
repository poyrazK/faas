package apidserver

import (
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// Account page profile forms (ADR 20261107103000): resend verification,
// change sign-in email and display name.

const (
	dashboardDisplayNameAction = "account_display_name"
	dashboardDisplayNameCookie = "faas_csrf_account_display_name"
)

// fillAccountProfileForms mints the form tokens the account page renders.
func (s *server) fillAccountProfileForms(w http.ResponseWriter, r *http.Request, acct state.Account, data *dashboard.AccountData) error {
	var err error
	if !acct.EmailVerified() {
		if data.VerifyResendConfirmToken, err = s.issueDashboardFormToken(w, dashboardVerifyResendAction, dashboardVerifyResendCSRFCookie, acct.ID); err != nil {
			return err
		}
	}
	if data.EmailChangeConfirmToken, err = s.issueDashboardFormToken(w, dashboardEmailChangeAct, dashboardEmailChangeCSRFC, acct.ID); err != nil {
		return err
	}
	if !acct.MFAEnrolled() {
		_, perr := s.store.AccountPasswordByAccountID(r.Context(), acct.ID)
		data.EmailChangeNeedsPassword = perr == nil
	}
	if data.DisplayNameConfirmToken, err = s.issueDashboardFormToken(w, dashboardDisplayNameAction, dashboardDisplayNameCookie, acct.ID); err != nil {
		return err
	}
	if flow, ok := s.store.(state.AccountFlowStore); ok {
		data.DisplayName, _ = flow.AccountDisplayName(r.Context(), acct.ID)
	}
	return nil
}

// accountFlowFlash is the account page banner after one of these forms.
func accountFlowFlash(r *http.Request) string {
	q := r.URL.Query()
	switch {
	case q.Get("verification") == "sent":
		return "If your address is still unverified, a new 24-hour verification link is on its way."
	case q.Get("email_change") == "sent":
		return "Check the new address: open the link we sent within 1 hour to finish the change."
	case q.Get("display_name") == "saved":
		return "Display name saved."
	}
	return ""
}

// dashboardSetDisplayName is POST /dashboard/account/display-name.
func (s *server) dashboardSetDisplayName(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardDisplayNameAction, acct.ID, dashboardDisplayNameCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	if problem := s.setDisplayName(r, acct, r.FormValue("display_name")); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	http.Redirect(w, r, "/dashboard/account?display_name=saved", http.StatusSeeOther)
}

// patchAccountProfile is PATCH /v1/account.
func (s *server) patchAccountProfile(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var in struct {
		DisplayName *string `json:"display_name"`
	}
	if err := decodeJSON(r, &in); err != nil || in.DisplayName == nil {
		api.WriteProblem(w, api.ErrValidation(`body must be {"display_name": "..."}`))
		return
	}
	if problem := s.setDisplayName(r, acct, *in.DisplayName); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"display_name": strings.TrimSpace(*in.DisplayName)})
}

func (s *server) setDisplayName(r *http.Request, acct state.Account, raw string) *api.Problem {
	name, err := api.ValidateDisplayName(raw)
	if err != nil {
		return api.ErrValidation(err.Error())
	}
	flow, ok := s.store.(state.AccountFlowStore)
	if !ok {
		return api.ErrCapacity("display names are unavailable")
	}
	if err := flow.SetAccountDisplayName(r.Context(), acct.ID, name); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Account not found", "the account no longer exists")
		}
		s.log.Error("display_name.set", "err", err, "account_id", acct.ID)
		return api.ErrCapacity("could not save the display name; try again")
	}
	s.audit.Emit(r.Context(), "account.display_name_set", &acct.ID, nil)
	return nil
}
