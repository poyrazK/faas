// Package dashboard holds the server-rendered dashboard surface that
// apid exposes for M7.5 (ADR-011). The decision is to ship a thin Go
// html/template + HTMX shell — no SPA build chain, no JS framework —
// so the whole funnel fits inside the 6 GB control-plane slice
// (spec §13). gatewayd reverse-proxies /dashboard/* and /oauth/* to
// apid's loopback listener so the §11 single-public-listener invariant
// stays intact (ADR-011).
//
// Templates live under templates/ and are baked into the binary via
// embed.FS so deploys ship one artefact per daemon (CLAUDE.md).
// Slice 2 ships just enough to prove the surface renders; slice 4
// fills in the real data.
package dashboard

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"sync"
)

//go:embed templates/*.html
var tmplFS embed.FS

// Page is the data each dashboard handler hands to Render. Keeping it
// in this package (not pkg/api) means dashboard handlers don't grow
// new fields in the public DTO surface by accident.
type Page struct {
	// Title is the <title> tag content.
	Title string
	// Flash is a one-line status banner rendered above the page body.
	// Used by /login and /auth/verify to surface "check your email".
	Flash string
	// Account is the signed-in account for authed pages. nil for
	// /login + /auth/*; dashboardAuth rejects unauthed requests for the
	// rest of /dashboard/*.
	Account *AccountView
	// Body is the per-page template name (without the .html suffix).
	// Render looks up templates/<Body>.html inside the layout.
	Body string
	// Data is the page-specific payload (struct, map, etc.).
	Data any
}

// AccountView is the dashboard-facing slice of state.Account. Never
// log secrets here. Source data is pkg/state.Account; slices 3+4 expand.
type AccountView struct {
	ID            string
	Email         string
	Plan          string
	AppCount      int
}

// Render writes the page to w. It parses the templates on first use
// and caches the parsed tree in a sync.Once — the hot path is a
// single Execute call.
//
// Body is the per-page template name ("index", "apps_list", …). It
// MUST be present under templates/ or Render writes a 500 problem.
func Render(w http.ResponseWriter, log *slog.Logger, page Page) error {
	if page.Body == "" {
		page.Body = "index"
	}
	t, err := parseTemplates()
	if err != nil {
		log.Error("dashboard template parse failed", "err", err)
		return err
	}
	tplName := page.Body + ".html"
	if t.Lookup(tplName) == nil {
		log.Error("dashboard template missing", "name", page.Body)
		return fmt.Errorf("dashboard: template %q not found", page.Body)
	}
	var buf bytes.Buffer
	// Execute the page template directly with the Page struct. Each
	// page template defines the full <html>…</html> wrapper (not a
	// shared layout) — slices stay small enough that the duplication
	// is cheaper than a layout-include dance.
	if err := t.ExecuteTemplate(&buf, tplName, page); err != nil {
		log.Error("dashboard template execute failed", "err", err, "page", page.Body)
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, err = buf.WriteTo(w)
	return err
}

var (
	tmplOnce sync.Once
	tmplTree *template.Template
	tmplErr  error
)

func parseTemplates() (*template.Template, error) {
	tmplOnce.Do(func() {
		// Parse the layout + every page so {{template "name"}} lookups
		// resolve inside the layout. Failure is fatal for the daemon.
		tmplTree, tmplErr = template.New("").ParseFS(tmplFS, "templates/*.html")
	})
	return tmplTree, tmplErr
}
