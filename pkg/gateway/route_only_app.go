// adr: 20261121170000
package gateway

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// refuseDirectRouteApp answers a request that reached a route split's route
// app other than through its source app's route rule (ADR-20261121170000
// amendment 6) and reports true; the caller returns. The route's app has
// the hosts every app gets (its platform host, deployment URLs, aliases)
// and may be given more, and none of them may serve it: the split moved one
// route of the source app, it did not publish a second app. The platform's
// own deployment smoke and route probes still reach it.
func (h *Handler) refuseDirectRouteApp(w http.ResponseWriter, r *http.Request, app App) bool {
	if app.RouteOfAppID == "" {
		return false
	}
	if source, ok := r.Context().Value(edgeRouteSourceKey{}).(App); ok && source.ID == app.RouteOfAppID {
		return false
	}
	keepFromSharedCaches(w)
	api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not served here",
		"this app serves one route of another app and answers only through that app's hostnames"))
	return true
}
