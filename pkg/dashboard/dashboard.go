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
	ID       string
	Email    string
	Plan     string
	AppCount int
}

// IndexData is the /dashboard/ overview payload.
type IndexData struct {
	DeployedAppCount int
	Plan             string
}

// AppListItem is one row on /dashboard/apps.
type AppListItem struct {
	Slug         string
	Status       string
	URL          string
	LastDeployed string // empty when no deploys yet
	// StateBadge* is the cold-wake status glyph ux_spec §6.3 asks
	// for: ● running / ◌ sleeping / ⟳ waking / · idle (failed/
	// stopped). Set by renderAppsList via BadgeFor based on the
	// newest instance row for the app; "" until the dashboard
	// plumbs it (kept on the type so templates can render an empty
	// glyph during partial migrations).
	StateBadge      string
	StateBadgeGlyph string
	StateBadgeLabel string
}

// ManifestView is the runner-scaffold snapshot shown on the app detail
// page. Names are JSONish to avoid a second copy of pkg/api.AppManifest.
type ManifestView struct {
	Entrypoint []string
	Env        map[string]string
	WorkingDir string
	Port       int
	Healthz    string
	User       string
}

// DeploymentItem is one row on the app detail page's deploy list.
type DeploymentItem struct {
	ID        string
	Status    string
	Kind      string
	CreatedAt string
	Error     string
}

// CronItem is one row on the app detail page's crons tab.
type CronItem struct {
	ID          string
	Schedule    string
	Path        string
	Enabled     bool
	LastFiredAt string // empty until first fire
}

// AppDetailData combines the bits the app detail page renders.
type AppDetailData struct {
	App         AppListItem
	Manifest    ManifestView
	Deployments []DeploymentItem
	Crons       []CronItem
}

// UsageData is the /dashboard/usage page payload.
type UsageData struct {
	Month           string
	UsedGBHours     float64
	IncludedGBHours int64
	OverageGBHours  float64
	UsedPct         float64 // 0..100+
}

// BillingData is the /dashboard/billing page payload.
type BillingData struct {
	Plan     string
	RAMMB    int
	Included int64
	AppsCap  int
	AppLayer int
	IdleSec  int
}

// APIKeyItem is one row on the /dashboard/account page's keys tab.
type APIKeyItem struct {
	ID         string
	Prefix     string
	Label      string
	CreatedAt  string
	LastUsedAt string // empty until first use
}

// AccountData is the /dashboard/account page payload.
type AccountData struct {
	Keys []APIKeyItem
	// ShowDelete + DeleteConfirmToken drive the "Danger zone" partial
	// in templates/account.html. The token is the literal string
	// "delete:yes" the POST form must echo back (see
	// cmd/apid/dashboard_delete.go::confirmTokenMatches). ShowRestore
	// + RestoreUntil render the matching "Restore account" form when
	// the row is in deleted_pending — the deadline is the human-
	// readable restore_until the dashboard template surfaces.
	ShowDelete          bool
	DeleteConfirmToken  string
	ShowRestore         bool
	RestoreUntil        string
	RestoreConfirmToken string
	// FlashSurface holds "scheduled for deletion" / "restored" banners
	// the dashboard reads from ?deleted=1 / ?restored=1 in the URL.
	// Kept here (not Page.Flash) so the danger-zone partial stays a
	// self-contained block the layout file can render unconditionally.
	FlashSurface string
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
