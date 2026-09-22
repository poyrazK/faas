// cmd/gatewayd-internal/app_logs_dispatcher.go — ?archive=1
// routing between the live AppLogsHandler and the new
// ArchiveLogsHandler (issue #562 PR-B). Lives on the same mux
// path (`GET /v1/apps/{slug}/logs`) so the public URL stays
// singular; the dispatcher reads r.URL.Query().Get("archive")
// and forwards to the right handler.
//
// Why a dispatcher (and not a separate route):
//
//   - The customer-facing URL surface is one shape
//     (`/v1/apps/{slug}/logs`). A separate route
//     (`/v1/apps/{slug}/logs/archive`) would force the
//     dashboard + CLI to pick a mount point at request time
//     and bolt on cross-URL correlation by hand.
//   - The mux that mounts the handlers is created in
//     cmd/gatewayd-internal/run.go::runWithDeps; the dispatcher
//     is the single seam where the "archive vs live" choice
//     happens, so a future flag (e.g. `?source=ring|spool|archive`)
//     would land here without touching either handler.
//
// Why one mux (and not http.ServeMux with two routes): the
// stdlib mux matches on the path only — query strings don't
// participate in routing. A separate route would need to
// disambiguate via a sub-mux + path segments, and the
// dashboard's "tap to switch" UX prefers a query flag.

package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// appLogsDispatcher routes one inbound request to either the
// live stream (no ?archive=1) or the bucket-proxy archive
// read-back (?archive=1). The two handler fields are typed to
// http.Handler so a future third source (e.g. the local spool
// when the customer wants "before the next flush") can land
// here without changing the dispatcher shape. nil-safe — both
// fields fall through to the apid proxy if missing.
type appLogsDispatcher struct {
	live    http.Handler
	archive http.Handler
}

// ServeHTTP is the single entry point the run.go logsMux
// dispatches to. The query is read once and the right
// handler runs to completion (the SSE response is
// long-lived, so the dispatcher doesn't return until the
// chosen handler does — same shape as the live handler's
// direct ServeHTTP).
func (d *appLogsDispatcher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("archive") == "1" {
		if d.archive != nil {
			d.archive.ServeHTTP(w, r)
			return
		}
		// Archive not wired — handler not constructed in
		// this build (test seam). Fail loud with a stable
		// 503 so the SDK can branch without exposing gateway wiring.
		api.WriteProblem(w, api.ErrLogArchiveUnavailable())
		return
	}
	if d.live != nil {
		d.live.ServeHTTP(w, r)
		return
	}
	api.WriteProblem(w, api.ErrAppLogsUnavailable())
}
