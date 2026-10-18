package datarepair

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/state"
)

// Review serves a data-free shell; every read and action still crosses the
// normal management authentication, scope and MFA middleware.
func (s *Managed) Review(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug")); !ok {
		return
	}
	name, kind, ok := repairReviewAsset(r.PathValue("asset"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'")
	b, err := web.ReadFile(name)
	if err != nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write(b)
}

func repairReviewAsset(asset string) (string, string, bool) {
	switch asset {
	case "":
		return "web/managed.html", "text/html; charset=utf-8", true
	case "managed.css":
		return "web/app.css", "text/css; charset=utf-8", true
	case "managed.js", "sources.js", "doctor.js", "history.js", "checks.js", "validation.js", "verification.js", "replacement.js", "resolution.js":
		return "web/" + asset, "text/javascript; charset=utf-8", true
	default:
		return "", "", false
	}
}
