package apidserver

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mcpcatalog"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const dashboardMCPRecoveryAction = "dashboard_mcp_recovery"
const dashboardMCPRecoveryCookie = "dashboard_mcp_recovery_csrf"

func (s *server) dashboardMCPRetirements(w http.ResponseWriter, r *http.Request, ctx context.Context, account state.Account, query url.Values, appsKnown bool, data *dashboard.MCPOperationsData) {
	offset, err := dashboardMCPOffset(query, "retirement_offset")
	if err != nil || offset > api.MCPOAuthRetirementMaxOffset {
		data.RetirementError = "Invalid retirement offset. Clear the filters and try again."
		return
	}
	source, ok := s.store.(state.MCPOAuthRetirementStore)
	if !ok || !appsKnown {
		data.RetirementError = "Grant retirement inventory is unavailable."
		return
	}
	snapshot, err := source.ListMCPOAuthRetirements(ctx, account.ID, offset)
	if err != nil {
		data.RetirementError = "Grant retirement inventory is unavailable."
		return
	}
	// This ledger is account-wide, including deleted apps; do not silently
	// filter it. Only a company app the session may no longer see leaves it,
	// deleted or not.
	snapshot, err = mcpcatalog.NewRetirementVisibility(r, s.store, account).Filter(snapshot)
	if err != nil {
		data.RetirementError = "Grant retirement inventory is unavailable."
		return
	}
	for _, record := range snapshot.Items {
		recoverable := record.RetainUntil.After(time.Now().UTC()) && record.State != "installed" && record.State != "provider_acknowledged" && record.State != "manually_acknowledged" && record.State != "expired"
		canRetry := recoverable && record.State != "awaiting_issuance" && record.Reason != "issuance_unknown"
		data.Retirements = append(data.Retirements, dashboard.MCPRetirementView{ID: record.ID, App: record.AppSlug, Role: record.Role, State: record.State, Reason: record.Reason, RetainUntil: dashboardMCPTime(&record.RetainUntil), Recoverable: recoverable, CanRetry: canRetry})
	}
	if snapshot.NextOffset > 0 {
		data.NextRetirementsURL = dashboardMCPURL(query, "retirement_offset", strconv.Itoa(snapshot.NextOffset)) + "#retirements"
	}
	if s.sessions == nil {
		return
	}
	token, err := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardMCPRecoveryAction, account.ID, dashboardMCPRecoveryCookie)
	if err != nil {
		return
	}
	data.ActionCSRF = token
	http.SetCookie(w, &http.Cookie{Name: dashboardMCPRecoveryCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
}

func (s *server) dashboardMCPRetirementAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	account, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardMCPRecoveryAction, account.ID, dashboardMCPRecoveryCookie); err != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid recovery CSRF token; reload the dashboard"))
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid recovery form"))
		return
	}
	for key, values := range r.PostForm {
		if len(values) != 1 || (key != "csrf_token" && key != "action" && key != "provider_confirmed") {
			api.WriteProblem(w, api.ErrValidation("Invalid recovery form"))
			return
		}
	}
	action := r.PostForm.Get("action")
	if action != "retry" && action != "acknowledge_manual" {
		api.WriteProblem(w, api.ErrValidation("Invalid recovery action"))
		return
	}
	if action == "acknowledge_manual" && r.PostForm.Get("provider_confirmed") != "yes" {
		api.WriteProblem(w, api.ErrValidation("Confirm that the grant was resolved at the provider first"))
		return
	}
	// Reuse account-scoped API validation and atomic lease/state fences. Never opt
	// into revoking an active grant from this dashboard action.
	response := s.forwardDashboardEdgeRuleJSON(r, account, http.MethodPost, "/v1/mcp-oauth-retirements/"+url.PathEscape(r.PathValue("id")), r.PathValue("id"), api.MCPOAuthRetirementAction{Action: action}, dashboardJSONHandler(mcpcatalog.RetirementHandler(s.store)))
	if response.Code >= 400 {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(response.Code)
		_, _ = w.Write(response.Body.Bytes())
		return
	}
	http.Redirect(w, r, "/dashboard/mcp#retirements", http.StatusSeeOther)
}
