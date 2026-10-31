package product

import (
	"embed"
	"net/http"
)

//go:embed portal.html portal.js portal-core.js portal-plans.js portal-team.js portal-guidance.js portal-help.js portal-notifications.js portal-activity.js portal-run.js portal-history.js portal-email.js portal-signup.js portal.css email-unsubscribe.html email-unsubscribe.js
var portalFiles embed.FS

func (s *Service) PortalPage(w http.ResponseWriter, r *http.Request) {
	servePortalFile(w, r, "portal.html", "text/html; charset=utf-8")
}
func (s *Service) PortalScript(w http.ResponseWriter, r *http.Request) {
	servePortalFile(w, r, "portal.js", "text/javascript; charset=utf-8")
}
func (s *Service) PortalCore(w http.ResponseWriter, r *http.Request) {
	servePortalFile(w, r, "portal-core.js", "text/javascript; charset=utf-8")
}
func (s *Service) PortalPlans(w http.ResponseWriter, r *http.Request) {
	servePortalFile(w, r, "portal-plans.js", "text/javascript; charset=utf-8")
}
func (s *Service) PortalTeam(w http.ResponseWriter, r *http.Request) {
	servePortalFile(w, r, "portal-team.js", "text/javascript; charset=utf-8")
}
func (s *Service) PortalGuidance(w http.ResponseWriter, r *http.Request) {
	servePortalFile(w, r, "portal-guidance.js", "text/javascript; charset=utf-8")
}
func (s *Service) PortalHelp(w http.ResponseWriter, r *http.Request) {
	servePortalFile(w, r, "portal-help.js", "text/javascript; charset=utf-8")
}
func (s *Service) PortalEmail(w http.ResponseWriter, r *http.Request) {
	servePortalFile(w, r, "portal-email.js", "text/javascript; charset=utf-8")
}
func (s *Service) PortalNotifications(w http.ResponseWriter, r *http.Request) {
	servePortalFile(w, r, "portal-notifications.js", "text/javascript; charset=utf-8")
}
func (s *Service) PortalActivity(w http.ResponseWriter, r *http.Request) {
	servePortalFile(w, r, "portal-activity.js", "text/javascript; charset=utf-8")
}
func (s *Service) PortalRun(w http.ResponseWriter, r *http.Request) {
	servePortalFile(w, r, "portal-run.js", "text/javascript; charset=utf-8")
}
func (s *Service) PortalHistory(w http.ResponseWriter, r *http.Request) {
	servePortalFile(w, r, "portal-history.js", "text/javascript; charset=utf-8")
}
func (s *Service) PortalSignup(w http.ResponseWriter, r *http.Request) {
	servePortalFile(w, r, "portal-signup.js", "text/javascript; charset=utf-8")
}
func (s *Service) PortalStyle(w http.ResponseWriter, r *http.Request) {
	servePortalFile(w, r, "portal.css", "text/css; charset=utf-8")
}

func servePortalFile(w http.ResponseWriter, r *http.Request, name, contentType string) {
	portalHeaders(w, contentType)
	content, err := portalFiles.ReadFile(name)
	if err != nil {
		http.Error(w, "Portal unavailable", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(content)
}
func portalHeaders(w http.ResponseWriter, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
}
