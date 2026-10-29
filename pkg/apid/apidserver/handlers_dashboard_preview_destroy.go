package apidserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/onebox-faas/faas/pkg/middleware"
)

const (
	dashboardPreviewDestroyAction     = "preview_destroy"
	dashboardPreviewDestroyCSRFCookie = "faas_csrf_preview_destroy"
)

// issueDashboardPreviewDestroyCSRF mints the token the previews page's
// Tear down forms carry. An empty token leaves the forms unusable rather
// than unprotected; the handler refuses them.
func (s *server) issueDashboardPreviewDestroyCSRF(w http.ResponseWriter, accountID string) string {
	if s.sessions == nil {
		return ""
	}
	token, err := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardPreviewDestroyAction, accountID, dashboardPreviewDestroyCSRFCookie)
	if err != nil {
		s.log.Warn("dashboard previews: issue csrf", "account_id", accountID, "err", err)
		return ""
	}
	http.SetCookie(w, &http.Cookie{Name: dashboardPreviewDestroyCSRFCookie, Value: token, Path: "/", HttpOnly: true,
		Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	return token
}

// dashboardDestroyPreview handles the Tear down form on /dashboard/previews:
// POST /dashboard/apps/{slug}/preview/{previewSlug}/destroy. It applies the
// same environment-wide teardown as POST /v1/preview/{slug}/destroy and
// returns to the previews page.
func (s *server) dashboardDestroyPreview(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	if !s.verifyDashboardConfigCSRF(w, r, dashboardPreviewDestroyAction, dashboardPreviewDestroyCSRFCookie, acct.ID) {
		return
	}
	app, err := s.store.AppBySlug(r.Context(), r.PathValue("previewSlug"))
	if err != nil || app.AccountID != acct.ID || app.PreviewOfSlug == "" || app.PreviewOfSlug != r.PathValue("slug") {
		http.NotFound(w, r)
		return
	}
	auditKind := "preview.destroyed_by_customer"
	if app.PreviewPrNumber == 0 {
		auditKind = "dev_session.destroyed"
	}
	// The teardown helpers write problems for the JSON API; the dashboard
	// reports the outcome on the page it returns to instead.
	outcome := "destroyed"
	if !s.tearDownPreviewEnvironment(httptest.NewRecorder(), r, acct, app, auditKind) {
		outcome = "error"
	}
	http.Redirect(w, r, "/dashboard/previews?"+url.Values{outcome: {app.Slug}}.Encode(), http.StatusSeeOther)
}
