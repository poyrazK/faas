// Package githubconnect keeps browser connection failures in the dashboard
// while retaining RFC 7807 responses for API clients.
package githubconnect

import (
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
)

func WriteProblem(w http.ResponseWriter, r *http.Request, p *api.Problem) {
	if !api.AcceptsHTML(r) {
		api.WriteProblemForRequest(w, r, p)
		return
	}
	// Never put provider prose, OAuth codes, state, or credentials in the URL.
	// The destination is fixed; the dashboard owns the copy for stable codes.
	q := url.Values{"section": {"integrations"}, "github": {"connect-failed"}, "github_error": {p.Code}}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Add("Vary", "Accept")
	http.Redirect(w, r, "/dashboard/settings?"+q.Encode(), http.StatusSeeOther)
}
