package apidserver

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	dashboardPostgresOverageAction     = "postgres_overage"
	dashboardPostgresOverageCSRFCookie = "faas_csrf_postgres_overage"
)

// attachManagedPostgresOverage adds the compute overage form to
// /dashboard/postgres for an account that may use overage
// (ADR-20261025094915). Others see no form, as on Free.
func (s *server) attachManagedPostgresOverage(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, data *dashboard.ManagedPostgresData) {
	if s.managedPostgres == nil || data.Error != "" || !s.managedPostgresOverageAvailable(acct) {
		return
	}
	limits, ok := api.ManagedPostgresLimitsFor(acct.Plan)
	if !ok {
		return
	}
	enabled, err := s.managedPostgres.Overage(r.Context(), acct.ResourceOwnerID())
	if err != nil {
		log.Warn("dashboard renderManagedPostgres: read overage", "account_id", acct.ID, "err", err)
		return
	}
	token, err := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardPostgresOverageAction, acct.ID, dashboardPostgresOverageCSRFCookie)
	if err != nil {
		log.Warn("dashboard renderManagedPostgres: csrf issue overage", "account_id", acct.ID, "err", err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: dashboardPostgresOverageCSRFCookie, Value: token, Path: "/", HttpOnly: true,
		Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds()),
	})
	item := &dashboard.ManagedPostgresOverageItem{
		Enabled:        enabled,
		AllowanceLabel: dashboardComputeUnitHours(limits.MonthlyComputeUnitSeconds),
		CeilingLabel:   dashboardComputeUnitHours(managedPostgresComputeCeiling(limits, true)),
		ConfirmToken:   token,
	}
	switch r.URL.Query().Get("overage") {
	case "on":
		item.Flash = "Compute overage is on."
	case "off":
		item.Flash = "Compute overage is off."
	}
	data.Overage = item
}

func dashboardComputeUnitHours(seconds int64) string {
	return fmt.Sprintf("%d compute-unit hours", seconds/3600)
}

// dashboardPostgresOverage handles the compute overage form on
// /dashboard/postgres. It is the same change as PUT /v1/postgres/overage.
func (s *server) dashboardPostgresOverage(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardPostgresOverageAction, acct.ID, dashboardPostgresOverageCSRFCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid CSRF token", "please reload the page and try again"))
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad form", err.Error()))
		return
	}
	choice := strings.ToLower(strings.TrimSpace(r.FormValue("enabled")))
	if choice != "on" && choice != "off" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad overage choice", "enabled must be on or off"))
		return
	}
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	if !s.managedPostgresOverageAvailable(acct) {
		managedPostgresPlanDenied(w, acct.Plan, "compute overage needs a paid plan; the monthly compute allowance is a hard limit on this plan")
		return
	}
	subject := acct.ResourceOwnerID()
	if err := s.managedPostgres.SetOverage(r.Context(), subject, choice == "on"); err != nil {
		managedPostgresProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "managed_postgres.overage.changed", &acct.ID, map[string]any{
		"subject_id": subject, "enabled": choice == "on", "surface": "dashboard",
	})
	http.Redirect(w, r, "/dashboard/postgres?overage="+choice, http.StatusSeeOther)
}
