package apidserver

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	dashboardIncidentAction = "dashboard_incident"
	dashboardIncidentCookie = "dashboard_incident_csrf"
)

// renderIncidents serves GET /dashboard/incidents (ADR-20261022193512):
// the account's open and recent incidents.
func (s *server) renderIncidents(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	data := dashboard.IncidentsData{Enabled: s.incidents.Enabled}
	if data.Enabled {
		data = dashboard.NewIncidentsData(s.incidents.Recent(r.Context(), acct), time.Now())
	}
	s.renderCustomDashboardPage(w, r, acct, "Incidents", "incidents", data)
}

// renderIncident serves GET /dashboard/incidents/{id}: the evidence, the
// notes, and the acknowledge and note forms.
func (s *server) renderIncident(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	data := dashboard.IncidentData{Enabled: s.incidents.Enabled}
	if data.Enabled {
		detail, err := s.incidents.Detail(r.Context(), acct, r.PathValue("id"))
		switch {
		case errors.Is(err, state.ErrNotFound):
			http.NotFound(w, r)
			return
		case err != nil:
			s.log.WarnContext(r.Context(), "dashboard: read incident", "err", err)
			data.Error = "This incident could not be loaded; try again shortly."
		default:
			data = dashboard.NewIncidentData(detail, time.Now())
			data.CSRF = s.issueDashboardIncidentCSRF(w, acct)
		}
	}
	s.renderCustomDashboardPage(w, r, acct, "Incident", "incident", data)
}

func (s *server) issueDashboardIncidentCSRF(w http.ResponseWriter, acct state.Account) string {
	if s.sessions == nil {
		return ""
	}
	token, err := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardIncidentAction, acct.ID, dashboardIncidentCookie)
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{Name: dashboardIncidentCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.domain != "",
		SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	return token
}

// dashboardIncidentAcknowledge serves POST
// /dashboard/incidents/{id}/acknowledge through the JSON handler.
func (s *server) dashboardIncidentAcknowledge(w http.ResponseWriter, r *http.Request) {
	s.dashboardIncidentWrite(w, r, "acknowledge", false)
}

// dashboardIncidentNote serves POST /dashboard/incidents/{id}/notes through
// the JSON handler, so validation and the cap are the API's.
func (s *server) dashboardIncidentNote(w http.ResponseWriter, r *http.Request) {
	s.dashboardIncidentWrite(w, r, "notes", true)
}

func (s *server) dashboardIncidentWrite(w http.ResponseWriter, r *http.Request, action string, withBody bool) {
	w.Header().Set("Cache-Control", "no-store")
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardIncidentAction, acct.ID, dashboardIncidentCookie) != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid incident CSRF token; reload the page"))
		return
	}
	if r.ParseForm() != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid incident form"))
		return
	}
	handler, body := s.incidents.Acknowledge, any(nil)
	if withBody {
		handler, body = s.incidents.AddNote, api.IncidentNoteRequest{Body: r.PostForm.Get("body")}
	}
	id := r.PathValue("id")
	resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodPost, "/v1/incidents/"+url.PathEscape(id)+"/"+action, id, body, handler)
	if resp.Code >= 400 {
		copyMCPAlertProblem(w, resp.Code, resp.Body.Bytes())
		return
	}
	http.Redirect(w, r, "/dashboard/incidents/"+url.PathEscape(id)+"#notes", http.StatusSeeOther)
}
