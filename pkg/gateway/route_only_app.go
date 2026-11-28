// adr: 20261121170000
package gateway

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/routeprobe"
)

// refuseDirectRouteApp answers a request that reached a route split's route
// app other than through its source app's route rule (ADR-20261121170000
// amendment 6) and reports true; the caller returns. The route's app has
// the hosts every app gets (its platform host, deployment URLs, aliases)
// and may be given more, and none of them may serve it: the split moved one
// route of the source app, it did not publish a second app. The platform's
// own deployment smoke and route probes still reach it.
func (h *Handler) refuseDirectRouteApp(w http.ResponseWriter, r *http.Request, app App) bool {
	if !directRouteAppRequest(r, app) {
		return false
	}
	keepFromSharedCaches(w)
	api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not served here",
		"this app serves one route of another app and answers only through that app's hostnames"))
	return true
}

// refuseDirectRouteAppEarly refuses a direct request to a route app before
// the serving path spends anything on it (traffic policy, budget admission,
// the request-ID journal). A request carrying the platform's smoke or
// route-probe headers waits for their authorization and the later
// refuseDirectRouteApp check.
func (h *Handler) refuseDirectRouteAppEarly(w http.ResponseWriter, r *http.Request, app App) bool {
	if r.Header.Get(apihostingreceipt.PlatformSmokeHeader) != "" ||
		r.Header.Get(apihostingreceipt.PlatformSmokeTokenHeader) != "" ||
		r.Header.Get(routeprobe.DeploymentHeader) != "" || r.Header.Get(routeprobe.TokenHeader) != "" {
		return false
	}
	return h.refuseDirectRouteApp(w, r, app)
}

// directRouteAppRequest reports whether app is a route app that r did not
// reach through its source app's route rule.
func directRouteAppRequest(r *http.Request, app App) bool {
	if app.RouteOfAppID == "" {
		return false
	}
	source, ok := r.Context().Value(edgeRouteSourceKey{}).(App)
	return !ok || source.ID != app.RouteOfAppID
}
