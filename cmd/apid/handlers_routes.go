package main

// Per-app fleet route reader (issues #273, #2416 / ADR-093).
//
// GET /v1/apps/{slug}/routes
//
// Read-only, scoped to api.ScopesReadSurface (admin or apps:read).
// No MFA required — the primary caller is an API key. IDOR-safe
// via the existing loadApp (cross-account slug → 404, not 200
// with another tenant's route labels — leaking per-route labels
// would let a customer enumerate another tenant's API surface).
//
// Production reads the control-plane Prometheus aggregate. Prometheus
// discovers every active compute gateway through apid's registry-backed
// HTTP-SD endpoint, so the route union covers the fleet without exposing
// gatewayd-internal's unauthenticated loopback control endpoint over the
// network. Scrape health supplies explicit live, partial, and unavailable
// states. Single-box development can still use the original loopback reader
// when Prometheus is not configured.
//
// Wire format
//
//	200  {"slug":"...","app_id":"..." (omitted if empty),
//	      "routes":["GET /users","..."],
//	      "source":"live",
//	      "collectors_expected":3,
//	      "collectors_healthy":3}              on complete collection
//	200  {"routes":[...],"source":"partial",
//	      "collectors_expected":3,
//	      "collectors_healthy":2}              on partial collection
//	200  {"slug":"...","routes":[],
//	      "source":"unavailable"}             on total bridge failure
//	404  problem+json                         on cross-account slug
//
// Why this lives on apid, not the gatewayd public listener:
//   - The auth chain (ScopesReadSurface) lives in apid where it
//     belongs; the per-account rate limit applies naturally.
//   - Prometheus is already the authoritative fleet aggregate and is
//     reachable only on control-plane loopback.
//   - The gateway control listener stays loopback-only by design (ADR-070);
//     exposing it remotely would require a separate authenticated protocol.

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// routesDialTimeout bounds both local Prometheus queries and the legacy
// single-box gateway control call.
const routesDialTimeout = 2 * time.Second

// getAppRoutes serves GET /v1/apps/{slug}/routes. The auth chain
// matches /v1/apps/{slug}/metrics (read-only, no MFA, primary
// caller is an API key with ScopesReadSurface). IDOR-safe via
// loadApp — cross-account slug is a 404, not a 200 with another
// tenant's route labels.
func (s *server) getAppRoutes(w http.ResponseWriter, r *http.Request, acct state.Account) { //nolint:contextcheck // loadApp takes r and uses r.Context() for its DB calls.
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		// loadApp already wrote the 404.
		return
	}
	snapshot := s.collectObservedRoutes(r.Context(), app.ID, app.Slug)
	w.Header().Set("X-Faas-Routes-State", snapshot.Source)
	writeJSON(w, http.StatusOK, api.AppRoutesResponse{
		Slug:               app.Slug,
		AppID:              app.ID,
		Routes:             snapshot.Routes,
		Source:             snapshot.Source,
		CollectorsExpected: snapshot.CollectorsExpected,
		CollectorsHealthy:  snapshot.CollectorsHealthy,
		CapHit:             snapshot.CapHit,
	})
}

// routesUpstreamResponse mirrors cmd/gatewayd-internal/
// routes_handler.go's routesResponseJSON. Kept as a separate
// type from api.AppRoutesResponse because the field renames
// (omitempty for AppID) and the Source field (only present on
// the apid side) shouldn't bleed across the package boundary.
//
// CapHit (ADR-093 Tier B item #1) flows through unchanged from
// the gatewayd wire shape to api.AppRoutesResponse.CapHit — the
// three types must stay in sync when adding fields, per the
// `tier-b-shape-drift` repo memory.
type routesUpstreamResponse struct {
	Slug   string   `json:"slug"`
	AppID  string   `json:"app_id"`
	Routes []string `json:"routes"`
	CapHit bool     `json:"cap_hit"`
}
