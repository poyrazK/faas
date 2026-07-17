// dashboard handlers (spec §14 M7.5, ADR-011).
//
// Slice 2 ships only the public-facing plumbing: routes mounted on the
// loopback listener (127.0.0.1:8081), a dashboard layout that proves
// the embed.FS + html/template + HTMX pipeline works end-to-end, and
// the §11 middleware (RequestID, Recovery, AuthLimit). Slice 3 lands
// the magic-link login + Resend/Postmark + sessions. Slice 4 fills the
// pages with real data.
//
// gatewayd reverse-proxies /dashboard/* and /oauth/* here so the
// single-public-listener invariant (spec §11) survives.
package main

import (
	"log/slog"
	"net/http"

	"github.com/onebox-faas/faas/pkg/dashboard"
)

// dashboardHandler is the slice-2 entry point. Reads the path, picks
// the right body template, and calls pkg/dashboard.Render. Handlers
// stay tiny (spec §Conventions) — anything over ~30 lines extracts.
//
// Slice 4 expands this to render the apps list, app detail, usage,
// billing, and account pages. Slice 3 wires sessionAuth in front
// (server.go) so by the time a request reaches here the caller is
// authenticated.
func (s *server) dashboardHandler(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := "index"
		page := dashboard.Page{
			Title: "Overview",
			Body:  body,
			// Slice 3 sets Account from the auth context; slice 2
			// ships the unauthed layout.
		}
		if acct, ok := AccountFrom(r.Context()); ok {
			page.Account = &dashboard.AccountView{
				ID:       acct.ID,
				Email:    acct.Email,
				Plan:     string(acct.Plan),
				AppCount: 0, // slice 4 wires CountDeployedApps
			}
		}
		if err := dashboard.Render(w, log, page); err != nil {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"type":"about:blank","title":"render","status":500,"detail":"dashboard render failed"}`))
		}
	}
}
