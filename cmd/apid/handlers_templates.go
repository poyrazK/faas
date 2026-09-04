// handlers_templates.go — Issue #961 / Mega-B PR-3.
//
// GET /v1/templates — the dashboard's source of truth for the
// template catalog. Mirrors cmd/gregale/templates.Names so the CLI
// and the dashboard agree on the list without sharing the embed FS.
//
// Why an HTTP endpoint instead of importing cmd/gregale/templates
// from pkg/dashboard: cmd/gregale is a main package — pkg/dashboard
// can't import it. The CLI's runtime validator already reads
// templates.Exists → templates.Names locally; a server endpoint keeps
// the dashboard and the CLI in sync without a refactor (a divergent
// release would show a 13-entry list on one and a 14-entry list on
// the other — the deploy doc notes the pairing requirement).
//
// The endpoint is cookie-session-authenticated (dashboardChain) — same
// gate the rest of the dashboard surface uses. No API-key auth; this
// is a customer-facing surface, not an automation hook.
package main

import (
	"net/http"
	"sort"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard/views"
)

// templateView is the wire shape — one row in the response. Kept
// separate from views.AppsNewTemplateView so the API shape is not
// tied to a dashboard-local render struct.
type templateView struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
}

// templateDescription returns the one-line customer-facing blurb for
// each template name. Mirrors the README in
// cmd/gregale/templates/<name>/README.md so the wizard copy matches
// the per-template docs. Unknown names return "" — the wizard filters
// these out of the <select>.
func templateDescription(name string) string {
	switch name {
	case "hello-node":
		return "minimal Node.js HTTP server — first-touch smoke test"
	case "hello-python":
		return "minimal Python 3.12 HTTP server — first-touch smoke test"
	case "hello-go":
		return "minimal Go HTTP server — first-touch smoke test"
	case "cron-example":
		return "scheduled cron job worker — fires every minute"
	case "function-node", "function-node24":
		return "Node.js function runtime — customise the handler"
	case "function-python", "function-python313":
		return "Python function runtime — customise the handler"
	case "function-go":
		return "Go function runtime — customise the handler"
	case "s3-uploader":
		return "S3-compatible PUT/GET adapter — bring your own bucket"
	case "slack-bot":
		return "Slack slash-command handler — bring your own tokens"
	case "rest-api-postgres":
		return "Postgres-backed REST API — bring your own connection string"
	case "cron-worker":
		return "scheduled job worker with retries — bring your own schedule"
	case "webhook-receiver":
		return "signed webhook receiver — bring your own upstream"
	case "ai-chat":
		return "OpenAI-compatible chat scaffold — bring your own key"
	}
	return ""
}

// listTemplates is GET /v1/templates. Returns every template name
// in templates.Names, plus its category (templates.CategoryFor) and
// description (templateDescription). Ordered by CategoryOrder then
// alphabetically inside each category so the wizard's <select>
// groups "hello" before "function" before "stateless-contract"
// before "ai" — the same order docs.gregale.dev/templates uses.
//
// Cookie-session-authenticated. API-key auth would surface the
// catalog to automation that has no business browsing templates,
// so the gate stays dashboard-only.
func (s *server) listTemplates(w http.ResponseWriter, r *http.Request) {
	const op = "listTemplates"

	acct, ok := AccountFrom(r.Context())
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
			"Unauthorized", "sign in to list templates"))
		return
	}
	_ = acct // not used today; logged for the audit trail parity.
	_ = op

	rows := make([]templateView, 0, len(templates.Names))
	for _, name := range templates.Names {
		cat := templates.CategoryFor(name)
		desc := templateDescription(name)
		if cat == "" || desc == "" {
			// Defensive: a template added to embed.FS but not
			// wired into CategoryFor or templateDescription would
			// 500 the dropdown. Skip it; the wizard renders the
			// remaining templates cleanly.
			continue
		}
		rows = append(rows, templateView{
			Name:        name,
			Category:    cat,
			Description: desc,
		})
	}
	// Stable sort by category order, then name. The category
	// ordinal matches the docs taxonomy.
	catOrder := map[string]int{}
	for i, c := range templates.CategoryOrder {
		catOrder[c] = i
	}
	sort.SliceStable(rows, func(i, j int) bool {
		oi, oj := catOrder[rows[i].Category], catOrder[rows[j].Category]
		if oi != oj {
			return oi < oj
		}
		return rows[i].Name < rows[j].Name
	})
	writeJSON(w, http.StatusOK, rows)
}

// projectAppsNewTemplates converts the wire templateView slice into
// the dashboard-local views.AppsNewTemplateView so the template can
// stay a pure renderer. Same 15 rows as /v1/templates returns; the
// dashboard never recomputes descriptions / categories client-side.
func projectAppsNewTemplates(wire []templateView) []views.AppsNewTemplateView {
	out := make([]views.AppsNewTemplateView, 0, len(wire))
	for _, t := range wire {
		out = append(out, views.AppsNewTemplateView{
			Name:        t.Name,
			Category:    t.Category,
			Description: t.Description,
		})
	}
	return out
}
