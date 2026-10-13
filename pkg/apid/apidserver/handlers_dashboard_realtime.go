package apidserver

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func parseAppRealtimePath(rest string) (string, bool) {
	rest = strings.TrimSuffix(rest, "/")
	if !strings.HasSuffix(rest, "/realtime") {
		return "", false
	}
	slug := strings.TrimSuffix(rest, "/realtime")
	return slug, slug != "" && !strings.Contains(slug, "/")
}

// renderAppRealtime is the dashboard shell for the realtime inspector; the
// managedrealtime package loads the data (ADR-982).
func (s *server) renderAppRealtime(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	_, allowed := s.realtime.RealtimePlanLimits(acct)
	data := dashboard.RealtimeInspectorData{Slug: slug, Enabled: allowed && s.realtime.HistoryPreviewEnabled, After: "0", WebhookURL: "/dashboard/apps/" + url.PathEscape(slug) + "/webhooks"}
	if data.Enabled {
		s.realtime.LoadInspector(r, acct, app, &data)
	}
	if data.ReplayJobURL != "" {
		token, tokenErr := middleware.IssueForAuthenticatedNamed(s.sessions, "realtime_replay_job", acct.ID, "faas_replay_job_csrf")
		if tokenErr == nil {
			data.ReplayJobCSRF = token
			http.SetCookie(w, &http.Cookie{Name: "faas_replay_job_csrf", Value: token, Path: "/", HttpOnly: true, Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
		}
	}
	count, _ := s.store.CountDeployedApps(r.Context(), acct.ID)
	view, _ := AccountFrom(r.Context())
	page := dashboard.Page{Title: "Realtime — " + slug, Body: "realtime_inspector", Account: dashboardAccountView(view, count), Data: data}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}
