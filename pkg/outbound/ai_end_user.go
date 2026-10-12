package outbound

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// aiCallerKeys reads an AI call's optional X-Gregale-AI-User and
// X-Gregale-AI-Session headers (ADR-994). The end user attributes and
// limits the call; the session, or else the end user, is the key-pool
// affinity key. Each must be one valid ID. The gateway never forwards
// X-Gregale-* headers, so neither reaches the provider.
func aiCallerKeys(r *http.Request, integration Integration) (endUser, affinity string, ok bool) {
	if integration.AIProvider == "" {
		return "", "", true
	}
	endUser, ok = singleAIHeader(r, api.OutboundAIUserHeader)
	if !ok {
		return "", "", false
	}
	session, ok := singleAIHeader(r, api.OutboundAISessionHeader)
	if !ok {
		return "", "", false
	}
	affinity = session
	if affinity == "" {
		affinity = endUser
	}
	return endUser, affinity, true
}

func singleAIHeader(r *http.Request, name string) (string, bool) {
	values := r.Header.Values(name)
	switch {
	case len(values) == 0:
		return "", true
	case len(values) == 1 && api.ValidOutboundAIUserID(values[0]):
		return values[0], true
	default:
		return "", false
	}
}
