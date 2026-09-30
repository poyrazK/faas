// adr: 375
package gateway

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

func (h *Handler) WithEdgeTargetPolicyLoader(load ResolveTargetAppPolicy) *Handler {
	h.resolveTargetPolicy = load
	return h
}

func (h *Handler) resolveEdgeTargetPolicy(r *http.Request, slug string) (App, bool) {
	if h.resolveTargetPolicy == nil {
		return h.resolveTargetApp(r.Context(), slug)
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.TrafficPublicHostReadTimeout)
	defer cancel()
	app, found, err := h.resolveTargetPolicy(ctx, slug)
	if err == nil {
		err = ctx.Err()
	}
	if err == nil && !found {
		err = errors.New("selected public edge target is unavailable")
	}
	if err != nil {
		*r = *r.WithContext(context.WithValue(r.Context(), hostPolicyLookupFailureKey{}, err))
		return App{}, false
	}
	return app, true
}
