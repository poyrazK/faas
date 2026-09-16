package main

// `gregale apps routes <slug>` — operator shell entry point for
// ADR-093's per-route observability surface. Lists the admitted
// route label set (the bounded "METHOD /raw/path" array the
// gatewayd-internal control listener emits) plus the cap_hit flag
// so the operator can tell "5 routes + __route_other__ (one
// over-cap request)" from "50 routes + __route_other__ (cap
// saturated)" without counting the array.
//
// Reachable two ways:
//   - gregale apps routes <slug>       (dispatchApps arm in main.go:175-184)
//   - gregale app <slug> routes        (cmdAppDispatch arm in commands5.go:607)
//
// Both dispatchers thread (slug, args[2:]) so the leaf signature
// matches cmdAppSecurity (commands_app_security.go:72) — same
// (slug, args) shape, no flag parsing, single authed round-trip.
//
// Out of scope: anything that would need a server change (per-plan
// cap override, route_label_count metric, etc.) — those are Tier C.

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdAppsRoutes implements `gregale apps routes <slug>` /
// `gregale app <slug> routes`. Reads the bounded per-route label
// snapshot for one app via the SDK-shaped GET /v1/apps/{slug}/routes
// endpoint (apid reverse-proxies the gatewayd-internal control
// listener). The flag set is intentionally empty — the surface is
// read-only and the customer can render the response as text or JSON
// via the package-level --json toggle already wired by every other
// leaf.
//
// Why no flags: --cap-only / --sort / --filter would each be a
// future-shape-change that the existing per-app routes endpoint
// can't honour without a server patch. Punting keeps the leaf
// honest with the wire shape and lets the dashboard do any
// client-side filtering.
func cmdAppsRoutes(slug string, args []string) int {
	_ = args // no flags yet; future flags land here alongside a server DTO bump
	if slug == "" {
		PrintUsage(os.Stderr, "usage: gregale apps routes <slug>", "apps")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetAppRoutes(context.Background(), slug)
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	return renderAppsRoutesText(osStdout, slug, resp)
}

// renderAppsRoutesText renders the AppRoutesResponse as a 2-column
// SLUG/ROUTE table plus a `source:` chip + the cap_hit flag.
//
// Layout:
//   - 1 header line ("Routes for <slug>")
//   - N route rows (or a single "no routes" line if Routes is empty)
//   - 1 footer line ("source: <source>" + " cap_hit: true|false")
//
// The cap_hit chip is rendered as a literal "true"/"false" string
// rather than an emoji/symbol so the operator can pipe the output
// through grep / awk without losing information. When source is
// "unavailable" we suppress cap_hit (the gatewayd-internal dial
// failed, so the cap state is unknown — see
// cmd/apid/handlers_routes.go:147-152 for the unavailable path
// shape and the comment at 181-184 explaining the omission).
func renderAppsRoutesText(w io.Writer, slug string, resp api.AppRoutesResponse) int {
	_, _ = fmt.Fprintf(w, "Routes for %s\n", slug)
	if len(resp.Routes) == 0 {
		_, _ = fmt.Fprintln(w, "  (no routes)")
	} else {
		for _, r := range resp.Routes {
			_, _ = fmt.Fprintf(w, "  %s\n", r)
		}
	}
	if resp.Source == api.AppRoutesSourceUnavailable {
		_, _ = fmt.Fprintf(w, "source: unavailable (collectors: %d/%d; cap_hit unknown)\n", resp.CollectorsHealthy, resp.CollectorsExpected)
	} else {
		_, _ = fmt.Fprintf(w, "source: %s\n", resp.Source)
		_, _ = fmt.Fprintf(w, "collectors: %d/%d\n", resp.CollectorsHealthy, resp.CollectorsExpected)
		_, _ = fmt.Fprintf(w, "cap_hit: %t\n", resp.CapHit)
		if resp.CapHit {
			_, _ = fmt.Fprintln(w, "  (the app has hit the 50-route cap; additional routes are collapsing into __route_other__)")
		}
	}
	return 0
}
