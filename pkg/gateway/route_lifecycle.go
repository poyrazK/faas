package gateway

import (
	"context"
	"github.com/onebox-faas/faas/pkg/routelifecycle"
	"net/http"
	"strings"
)

func (h *Handler) applyRouteLifecycle(w http.ResponseWriter, r *http.Request, app App, path, method string) {
	resolver, ok := h.declaredRoutes.(interface {
		ResolveRouteLifecycle(context.Context, App, string, string) (routelifecycle.Metadata, error)
	})
	if !ok {
		return
	}
	metadata, err := resolver.ResolveRouteLifecycle(r.Context(), app, path, method)
	if err == nil {
		metadata.Apply(w.Header())
	}
}

// Resolve metadata only after the final target (including canary/fallback) is selected.
func (h *Handler) applyDeploymentRouteLifecycle(w http.ResponseWriter, r *http.Request, app App, deploymentID, path, method string) *http.Request {
	resolver, ok := h.declaredRoutes.(interface {
		ResolveDeploymentRouteLifecycle(context.Context, App, string, string, string) (routelifecycle.Metadata, error)
	})
	if !ok || deploymentID == "" {
		return r
	}
	metadata, err := resolver.ResolveDeploymentRouteLifecycle(r.Context(), app, deploymentID, path, method)
	if err == nil {
		metadata.Apply(w.Header())
		r = r.WithContext(context.WithValue(r.Context(), routeLifecycleContextKey{}, metadata))
	}
	return r
}

// Lifecycle guidance is resolved at replay time, never copied from stored headers.
func isCachedRouteLifecycleHeader(name, value string) bool {
	return strings.EqualFold(name, "Deprecation") || strings.EqualFold(name, "Sunset") || strings.EqualFold(name, "Link") && strings.Contains(strings.ToLower(value), "successor-version")
}

type routeLifecycleContextKey struct{}

func platformOwnsLifecycleHeader(ctx context.Context, name string) bool {
	metadata, _ := ctx.Value(routeLifecycleContextKey{}).(routelifecycle.Metadata)
	return strings.EqualFold(name, "Deprecation") && !metadata.DeprecatedAt.IsZero() || strings.EqualFold(name, "Sunset") && !metadata.SunsetAt.IsZero()
}

type lifecycleRequestRouteKey struct{}
type lifecycleRequestRoute struct{ path, method string }

func withLifecycleRequestRoute(r *http.Request, path, method string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), lifecycleRequestRouteKey{}, lifecycleRequestRoute{path, method}))
}

func (h *Handler) applyCachedRouteLifecycle(w http.ResponseWriter, r *http.Request, app App, entry *cacheEntry) {
	// Do not recapture a stale body as a new response from a failed origin,
	// or extend its expiry merely because it was replayed.
	for current := w; current != nil; {
		if writer, ok := current.(*cacheWriter); ok {
			writer.bypass = true
			writer.buf.Reset()
			break
		}
		unwrapper, ok := current.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		current = unwrapper.Unwrap()
	}
	// A stale-on-error reply may already carry metadata from a failed origin.
	// Remove it even if this old entry has no origin identity.
	w.Header().Del("Deprecation")
	w.Header().Del("Sunset")
	links := w.Header().Values("Link")
	w.Header().Del("Link")
	for _, value := range links {
		if !isCachedRouteLifecycleHeader("Link", value) {
			w.Header().Add("Link", value)
		}
	}
	if entry == nil || entry.servedDeploymentID == "" || entry.key.AppID != app.ID {
		return
	}
	if entry.key.DeploymentID != "" && entry.key.DeploymentID != entry.servedDeploymentID {
		return
	}
	if app.PinnedDeploymentID != "" && app.PinnedDeploymentID != entry.servedDeploymentID {
		return
	}
	route, ok := r.Context().Value(lifecycleRequestRouteKey{}).(lifecycleRequestRoute)
	if !ok {
		route = lifecycleRequestRoute{r.URL.Path, r.Method}
	}
	h.applyDeploymentRouteLifecycle(w, r, app, entry.servedDeploymentID, route.path, route.method)
}
