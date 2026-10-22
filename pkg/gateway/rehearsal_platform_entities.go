// adr: 20261022191742
package gateway

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// entitiesUnavailableRoute refuses durable entity calls. An entity's value and
// version come from running its handler against production entity state,
// which a contained run can neither read nor change.
func entitiesUnavailableRoute(w http.ResponseWriter, _ *http.Request, _ rehearsalPlatformCall) {
	api.WriteProblem(w, api.NewProblem(http.StatusNotImplemented, api.CodeNotImplemented, "Durable entities unavailable in a rehearsal",
		"a contained rehearsal cannot read or change production entity state; drive the entity handler from the recipe").
		WithDocs("https://gregale.dev/docs/scenario-tests#contain-serving-vm-outbound-traffic"))
}
