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
func (s *server) dashboardHandler(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Path → body template. Defaults to "index" for / and
		// /dashboard/. Slice 4 expands to apps, apps/{slug}, usage,
		// billing, account.
		body := "index"
		switch r.URL.Path {
		case "/login", "/dashboard/login":
			body = "login"
		case "/dashboard/apps", "/dashboard/apps/":
			// Slice 4 renders the apps list here.
			body = "index"
		}
		page := dashboard.Page{
			Title: "Overview",
			Body:  body,
			// Slice 3 replaces this with a real session lookup;
			// slice 2 ships the unauthed layout to prove the
			// surface renders.
		}
		if err := dashboard.Render(w, log, page); err != nil {
			// Render already logged; reply with a problem.
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"type":"about:blank","title":"render","status":500,"detail":"dashboard render failed"}`))
		}
	}
}

// loginPlaceholder is the slice-2 /login handler. Slice 3 replaces
// this with the magic-link request flow (POST → mailer; consume →
// session cookie).
func (s *server) loginPlaceholder(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page := dashboard.Page{
			Title: "Sign in",
			Body:  "login",
		}
		if err := dashboard.Render(w, log, page); err != nil {
			http.Error(w, "render failed", http.StatusInternalServerError)
		}
	}
}
