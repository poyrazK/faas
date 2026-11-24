package consumersignup

import (
	"embed"
	"net/http"
)

//go:embed signup.html signup.js signup.css upgrade.html upgrade.js
var pageFiles embed.FS

// Page serves the hosted sign-up page while the app takes sign-ups.
func (s *Service) Page(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.publicApp(w, r, true); !ok {
		return
	}
	servePageFile(w, "signup.html", "text/html; charset=utf-8")
}

// UpgradePage serves the hosted upgrade page (ADR-20261124094745); the
// link's token is checked by the JSON calls the page makes.
func (s *Service) UpgradePage(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.publicApp(w, r, true); !ok {
		return
	}
	servePageFile(w, "upgrade.html", "text/html; charset=utf-8")
}

// UpgradeScript serves the upgrade page's script.
func (s *Service) UpgradeScript(w http.ResponseWriter, _ *http.Request) {
	servePageFile(w, "upgrade.js", "text/javascript; charset=utf-8")
}

// Script serves the sign-up page's script.
func (s *Service) Script(w http.ResponseWriter, _ *http.Request) {
	servePageFile(w, "signup.js", "text/javascript; charset=utf-8")
}

// Style serves the sign-up page's stylesheet.
func (s *Service) Style(w http.ResponseWriter, _ *http.Request) {
	servePageFile(w, "signup.css", "text/css; charset=utf-8")
}

func servePageFile(w http.ResponseWriter, name, contentType string) {
	content, err := pageFiles.ReadFile(name)
	if err != nil {
		http.Error(w, "Sign-up page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; "+
		"frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	_, _ = w.Write(content)
}
