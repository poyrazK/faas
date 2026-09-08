// /v1/internal/apps/{slug}/service-endpoints — ADR-167 loopback reader for
// the gateway's live service-replica registry.
//
// This endpoint exposes routable replica identities to trusted in-box
// components. It is deliberately mounted on the loopback-only control listener:
// NodeID is an internal transport identity, not a customer-facing address, and
// the endpoint is a registry projection rather than a public proxy.
package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// internalServiceEndpointsHandler serves the currently routable replica set
// for one app. appLookup resolves the URL slug using the daemon's existing
// store connection; provider reads the gateway's authoritative target cache.
func internalServiceEndpointsHandler(provider gateway.ServiceEndpointProvider, appLookup gateway.ResolveSlugFn, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if logger != nil {
			logger.Debug("internal service endpoints poll", "remote", r.RemoteAddr, "path", logsanitize.Field(r.URL.Path))
		}
		if r.Method != http.MethodGet {
			writeProblemServiceEndpoints(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported on this endpoint")
			return
		}

		const prefix = "/v1/internal/apps/"
		const suffix = "/service-endpoints"
		rest := strings.TrimPrefix(r.URL.Path, prefix)
		if !strings.HasSuffix(rest, suffix) {
			writeProblemServiceEndpoints(w, http.StatusNotFound, "not_found", "path must match /v1/internal/apps/<slug>/service-endpoints")
			return
		}
		slug := strings.Trim(strings.TrimSuffix(rest, suffix), "/")
		if strings.Contains(slug, "/") {
			writeProblemServiceEndpoints(w, http.StatusNotFound, "not_found", "slug must be one path segment")
			return
		}
		if slug == "" {
			writeProblemServiceEndpoints(w, http.StatusBadRequest, "missing_slug", "path segment slug is required")
			return
		}
		if appLookup == nil {
			writeProblemServiceEndpoints(w, http.StatusServiceUnavailable, "lookup_unavailable", "slug→appID resolver is not wired in this build")
			return
		}
		appID, ok := appLookup(slug)
		if !ok || appID == "" {
			writeProblemServiceEndpoints(w, http.StatusNotFound, "app_not_found", "no app is routed for this slug")
			return
		}
		if provider == nil {
			writeProblemServiceEndpoints(w, http.StatusServiceUnavailable, "registry_unavailable", "service endpoint registry is not wired in this build")
			return
		}
		snapshot, err := provider.ServiceEndpoints(r.Context(), appID)
		if err != nil {
			writeProblemServiceEndpoints(w, http.StatusServiceUnavailable, "registry_unavailable", "service endpoint registry could not be reconciled")
			return
		}
		if snapshot.Endpoints == nil {
			snapshot.Endpoints = []gateway.ServiceEndpoint{}
		}
		writeServiceEndpointsJSON(w, http.StatusOK, serviceEndpointsResponseJSON{
			Slug:      slug,
			AppID:     appID,
			Endpoints: snapshot.Endpoints,
		})
	}
}

type serviceEndpointsResponseJSON struct {
	Slug      string                    `json:"slug"`
	AppID     string                    `json:"app_id"`
	Endpoints []gateway.ServiceEndpoint `json:"endpoints"`
}

func writeServiceEndpointsJSON(w http.ResponseWriter, status int, body serviceEndpointsResponseJSON) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	encoded, err := json.Marshal(body)
	if err != nil {
		return
	}
	_, _ = w.Write(encoded)
}

func writeProblemServiceEndpoints(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	api.WriteProblem(w, api.NewProblem(status, code, "Service endpoint read failed", detail))
}
