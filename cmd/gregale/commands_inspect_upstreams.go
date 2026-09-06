// gregale inspect <slug> --upstreams — operator-facing read
// surface for the ADR-098 §9.A captured-upstream table
// (issue #952).
//
// Renders the data_upstreams rows for an app as a tabular list,
// with a "N/M upstreams" quota stamp above the table (matching
// the secrets-list precedent at commands3.go:96). The §11
// invariant is preserved end-to-end: the API response DTO carries
// only HostRedactedHash + HostLast4 (handlers_upstreams.go:339),
// and this file renders only those fields — the plaintext host
// column in pgstore is NEVER on the wire.
//
// UX shape (human mode):
//
//	$ gregale inspect myapp --upstreams
//	myapp: 3/8 upstreams
//	  KIND        SCOPE            HOST_LAST4  PORT  SOURCE     LAST_RTT_MS  LAST_PROBED_AT
//	  postgres    primary          a1b2c3d4    5432  inferred   12           2026-08-18T12:34:56Z
//	  redis       cache            c3d4e5f6    6379  explicit   —            —
//
//	$ gregale inspect myapp --upstreams --scope=primary --json
//	{"upstreams":[...],"count":3,"quota_max":8,"scope":"primary"}
//
// Usage:
//
//	gregale inspect <slug> --upstreams [--scope <scope>] [--json]
package main

import (
	"context"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdInspectUpstreams implements `gregale inspect <slug>
// --upstreams [--scope <scope>] [--json]`. The slug and scope
// are pre-validated by cmdInspect (the verb-level dispatcher);
// this leaf only does the SDK round-trip + render branch.
//
// The render branch is JSON-aware at the top so a single
// `return jsonOut(...)` shape keeps the error/exit-code mapping
// consistent with cmdCronsInfo (commands_crons_info.go:62-65).
func cmdInspectUpstreams(slug, scope string) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	rows, count, quota, err := client.ListAppDataUpstreamsWithQuota(context.Background(), slug, scope)
	if err != nil {
		return printErr("Could not load upstreams", err)
	}
	if jsonOutput {
		// Emit the wrapped response so --json carries quota +
		// count + the scope filter (so a script can verify
		// which filter was applied by re-reading its own input).
		// The struct field name is `quota_max` on the wire
		// (handlers_upstreams.go:110-114) — the JSON tag on
		// DataUpstreamListResponse.Quota is the single source of
		// truth, so the test pins the round-trip verbatim.
		envelope := struct {
			Upstreams []api.DataUpstreamResponse `json:"upstreams"`
			Count     int                        `json:"count"`
			Quota     int                        `json:"quota_max"`
			Scope     string                     `json:"scope,omitempty"`
		}{Upstreams: rows, Count: count, Quota: quota, Scope: scope}
		return jsonOut(writeJSON(envelope))
	}
	renderUpstreamsList(osStdout, slug, rows, count, quota)
	return 0
}

// renderUpstreamsList writes the human-mode block: quota stamp,
// then a one-line header + one row per upstream. LAST_RTT_MS +
// LAST_PRObed_AT fall back to GlyphEmDash (output.go:104) when
// the field is unset. Column widths fit an 80-col terminal;
// host_last4 is an eight-character hash fragment; longer values
// truncate visually without breaking
// the human reader's eye (the row is space-delimited; --json
// is the parseable path).
func renderUpstreamsList(w io.Writer, slug string, rows []api.DataUpstreamResponse, count, quota int) {
	if len(rows) == 0 {
		_, _ = fmt.Fprintf(w, "%s: no upstreams (0/%d)\n", slug, quota)
		return
	}
	_, _ = fmt.Fprintf(w, "%s: %d/%d upstreams\n", slug, count, quota)
	_, _ = fmt.Fprintf(w, "  %-12s %-16s %-12s %-6s %-10s %-12s %s\n",
		"KIND", "SCOPE", "HOST_LAST4", "PORT", "SOURCE", "LAST_RTT_MS", "LAST_PROBED_AT")
	for _, r := range rows {
		last4 := r.HostLast4
		if last4 == "" {
			last4 = GlyphEmDash
		}
		port := fmt.Sprintf("%d", r.Port)
		rtt := GlyphEmDash
		if r.LastRTTMs != nil {
			rtt = fmt.Sprintf("%d", *r.LastRTTMs)
		}
		probed := GlyphEmDash
		if r.LastProbedAt != "" {
			probed = r.LastProbedAt
		}
		_, _ = fmt.Fprintf(w, "  %-12s %-16s %-12s %-6s %-10s %-12s %s\n",
			r.Kind, r.Scope, last4, port, r.Source, rtt, probed)
	}
}
