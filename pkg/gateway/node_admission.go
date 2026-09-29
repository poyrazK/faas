package gateway

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
)

// Node refusal happens before guest execution. It is a bounded overflow
// outcome, not evidence that the placement is stale or an app replay signal.
func writeNodeAdmissionRefusal(w http.ResponseWriter, err error) bool {
	p, ok := grpcerr.FromStatus(err)
	if !ok || p == nil || (p.Code != api.CodeConcurrencyThrottled && p.Code != api.CodeHTTPAdmissionUnavailable) {
		return false
	}
	if p.Code == api.CodeConcurrencyThrottled {
		p.Title = "App busy"
		p.Detail = "This app is handling its current request limit. Retry shortly."
	} else {
		p.Title = "Request admission unavailable"
		p.Detail = "Gregale could not verify this app's request capacity. Retry shortly."
	}
	p = p.WithHeader("Retry-After", "1")
	api.WriteProblem(w, p)
	return true
}
