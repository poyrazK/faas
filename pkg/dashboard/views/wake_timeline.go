// wake_timeline.go — pkg/dashboard/views — PR-A wake-narrative table
// renderer. Mirrors the palette + accessibility discipline of render.go
// (token-only colors, no JS, role="img" + aria-label on any SVGs,
// html/template-safe). The exported functions return `template.HTML`
// blocks pre-rendered at the handler edge so pkg/dashboard stays free
// of html/template FuncMap wiring (the convention established by
// pkg/dashboard/stages/stages.go — see package doc).
//
// The dashboard does NOT use template.FuncMap (verified at
// pkg/dashboard/dashboard.go:903 — templates parsed via
// template.New("").ParseFS). Pre-rendering at the handler edge keeps
// the parse site consistent with the rest of the dashboard.
//
// G203 precedent: template.HTML casts are gated by a //nolint:gosec
// annotation with a textual sanitization rationale. Mirrors the
// precedent at pkg/dashboard/views/render.go:274/311 and is the
// blessed shape for dashboard-only html emission.
package views

import (
	"fmt"
	"html/template"
	"sort"
	"strings"
)

// WakeTimelineRow is one row of the per-app wake-timeline page's
// table. Stamped by pkg/state.pgstore.LookupBootStartedForWakes via
// the same LEFT JOIN LATERAL shape used by the app-detail page's
// "Recent wakes" table (PR #1015).
//
// Trigger is the closed-enum string from pkg/sched/triggers.go
// ("" = absent, dashboard renders em-dash). QueuedCount /
// ConcurrencyAtAdmit / ReadyInMS mirror the events.WakeBootMeta
// surface; zero values render em-dash so pre-PR-A fleet rows look
// identical to "field absent" rows.
//
// AtCapacityPresent distinguishes "jsonb key absent" (pre-PR-A fleet
// row that lacks the at_capacity key entirely) from "jsonb key
// present and explicitly false" (PR-A row that was admitted below
// the cap). The dashboard renders em-dash when AtCapacityPresent
// is false (we don't know), Yes when AtCapacity is true (at the
// cap), and No when AtCapacity is false and AtCapacityPresent is
// true (definitely not at the cap). This is the em-dash-on-absent
// convention for the rest of the row's fields.
type WakeTimelineRow struct {
	At                 string // pre-formatted RFC 3339 at the handler
	Kind               string // wake.boot_started | wake.boot_completed | wake.boot_failed | …
	State              string // mirror the instance State column on the app_detail recent-wakes table
	Trigger            string // "" if absent
	TriggerClass       string // request source class; "" for non-request wakes
	QueuedCount        int    // 0 if absent
	ConcurrencyAtAdmit int    // 0 if absent
	AtCapacity         bool   // present value; only meaningful when AtCapacityPresent is true
	AtCapacityPresent  bool   // true when the at_capacity key was in jsonb; false = absent
	ReadyInMS          int    // 0 if no boot_completed row
}

// RenderWakeTimelineTable returns the pre-rendered <table> HTML
// block for the new app_wake_timeline.html template's
// {{ .Data.RenderTable }} field. The handler is the caller; the
// template inlines the result.
//
// Columns: Started | State | Kind | Trigger | Queued | Concurrency
// | At cap | Ready (ms). The same column set the recent-wakes
// table on app_detail.html uses (PR #1015 + this PR-A), so the
// per-app page is consistent with the per-app-detail summary.
//
// Security: every interpolated value is escaped via
// template.HTMLEscapeString; the surrounding <table>/<tr>/<th>
// chassis is gated behind a single template.HTML cast with
// gosec:false (the values are pre-escaped, no dynamic JS).
func RenderWakeTimelineTable(rows []WakeTimelineRow) template.HTML {
	var b strings.Builder
	b.WriteString(`<table class="wake-timeline-table"><thead><tr>`) //nolint:gosec // G203: html/template-safe chassis, all values escaped below
	b.WriteString(`<th>Started</th><th>State</th><th>Kind</th>`)
	b.WriteString(`<th>Trigger</th><th>Queued</th><th>Concurrency</th>`)
	b.WriteString(`<th>At cap</th><th>Ready</th>`)
	b.WriteString(`</tr></thead><tbody>`)
	for _, r := range rows {
		b.WriteString(`<tr>`)
		b.WriteString(`<td>` + template.HTMLEscapeString(r.At) + `</td>`)
		b.WriteString(`<td>` + template.HTMLEscapeString(r.State) + `</td>`)
		b.WriteString(`<td>` + template.HTMLEscapeString(r.Kind) + `</td>`)
		trigger := r.Trigger
		if trigger == "" {
			trigger = "—"
		}
		b.WriteString(`<td><code>` + template.HTMLEscapeString(trigger) + `</code></td>`)
		b.WriteString(`<td>` + renderCellInt(r.QueuedCount) + `</td>`)
		b.WriteString(`<td>` + renderCellInt(r.ConcurrencyAtAdmit) + `</td>`)
		b.WriteString(`<td>` + renderCellAtCap(r.AtCapacity, r.AtCapacityPresent) + `</td>`)
		b.WriteString(`<td>` + renderCellReadyMS(r.ReadyInMS) + `</td>`)
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)
	return template.HTML(b.String()) //nolint:gosec // G203: chassis static, values escaped via template.HTMLEscapeString above
}

// RenderTriggerHistogram renders the 24h trigger histogram as a
// stable-ordered inline code-block-sized list. Used by the page
// summary card. counts is `trigger → N` where the keys come from
// the WakeBootMeta.Trigger values stamped by schedd; empty map
// renders as a single em-dash cell.
func RenderTriggerHistogram(counts map[string]int) template.HTML {
	if len(counts) == 0 {
		return template.HTML(`<span class="cell-empty">—</span>`) //nolint:gosec // G203: static chassis, no user input
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(`<code>`)
		b.WriteString(template.HTMLEscapeString(k))
		b.WriteString(`</code>=`)
		b.WriteString(fmt.Sprintf(`%d`, counts[k]))
	}
	return template.HTML(b.String()) //nolint:gosec // G203: chassis static, values escaped via template.HTMLEscapeString above
}

// renderCellInt renders an int cell, em-dash on zero (matches the
// app_detail.html em-dash convention for absent values).
func renderCellInt(n int) string {
	if n == 0 {
		return `<span class="cell-empty">—</span>`
	}
	return fmt.Sprintf(`%d`, n)
}

// renderCellAtCap renders the At cap boolean cell — em-dash on
// absent (pre-PR-A fleet row that lacks the at_capacity key;
// AtCapacityPresent=false), green "Yes" badge on at-cap
// (AtCapacity=true), grey "No" badge on explicitly-not-at-cap
// (AtCapacity=false AND AtCapacityPresent=true). Reuses the
// badge-yes / badge-no CSS tokens from the recent-wakes table
// (app_detail.html). The em-dash-on-absent branch is the
// dashboard's em-dash convention for "we don't know" — the
// PR-A review cluster (PR #1031) flagged the prior Yes/No-only
// implementation as misleading for pre-PR-A fleet rows.
func renderCellAtCap(b, present bool) string {
	if !present {
		return `<span class="cell-empty">—</span>`
	}
	if b {
		return `<span class="badge-yes">Yes</span>`
	}
	return `<span class="badge-no">No</span>`
}

// renderCellReadyMS renders the ready cell — "{ms} ms" on positive,
// em-dash on zero (the wake is still booting or rejected; no
// boot_completed row to compute from).
func renderCellReadyMS(ms int) string {
	if ms == 0 {
		return `<span class="cell-empty">—</span>`
	}
	return fmt.Sprintf(`%d ms`, ms)
}
