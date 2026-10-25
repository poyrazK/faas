package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/logsanitize"
)

const mcpInstanceProbeMaxBodyBytes = 64 << 10

type mcpInstanceProbeTargetResolver func(context.Context, string, string, string) (gateway.Target, bool, error)

// internalMCPInstanceProbeHandler serves the loopback-only direct replica
// probe used by apid's authenticated MCP health endpoint. Its resolver must
// validate the app, deployment, and RUNNING instance from durable state; the
// request never accepts an arbitrary node or guest URL.
func internalMCPInstanceProbeHandler(
	appLookup gateway.ResolveSlugFn,
	resolveTarget mcpInstanceProbeTargetResolver,
	forward func(gateway.Target) http.Handler,
	logger *slog.Logger,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if logger != nil {
			logger.Debug("internal MCP instance health probe", "path", logsanitize.Field(r.URL.Path))
		}
		if r.Method != http.MethodGet {
			writeMCPInstanceProbeProblem(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported on this endpoint")
			return
		}

		const prefix = "/v1/internal/apps/"
		const suffix = "/mcp-health"
		rest := strings.TrimPrefix(r.URL.Path, prefix)
		if !strings.HasSuffix(rest, suffix) {
			writeMCPInstanceProbeProblem(w, http.StatusNotFound, "not_found", "path must match /v1/internal/apps/<slug>/mcp-health")
			return
		}
		slug := strings.Trim(strings.TrimSuffix(rest, suffix), "/")
		if slug == "" || strings.Contains(slug, "/") {
			writeMCPInstanceProbeProblem(w, http.StatusBadRequest, "invalid_slug", "slug must be one non-empty path segment")
			return
		}
		if appLookup == nil || resolveTarget == nil || forward == nil {
			writeMCPInstanceProbeProblem(w, http.StatusServiceUnavailable, "probe_unavailable", "MCP instance probe is not wired in this build")
			return
		}
		deploymentID := strings.TrimSpace(r.URL.Query().Get("deployment_id"))
		instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
		host := strings.TrimSpace(r.URL.Query().Get("host"))
		if deploymentID == "" || instanceID == "" || !api.ValidMCPProbeHost(host) {
			writeMCPInstanceProbeProblem(w, http.StatusBadRequest, "invalid_request", "deployment_id, instance_id, and a host-only host parameter are required")
			return
		}
		appID, ok := appLookup(slug)
		if !ok || appID == "" {
			writeMCPInstanceProbeProblem(w, http.StatusNotFound, "app_not_found", "no app is routed for this slug")
			return
		}
		target, found, err := resolveTarget(r.Context(), appID, deploymentID, instanceID)
		if err != nil {
			if logger != nil {
				logger.Warn("resolve MCP instance health target", "app_id", logsanitize.Field(appID), "deployment_id", logsanitize.Field(deploymentID), "instance_id", logsanitize.Field(instanceID), "err", logsanitize.FieldAny(err))
			}
			writeMCPInstanceProbeProblem(w, http.StatusServiceUnavailable, "target_lookup_failed", "MCP instance target could not be verified")
			return
		}
		if !found || target.AppID != appID || target.DeploymentID != deploymentID || target.InstanceID != instanceID || target.NodeID == "" {
			writeMCPInstanceProbeProblem(w, http.StatusNotFound, "instance_not_found", "instance is not a running replica of the requested deployment")
			return
		}

		guestURL := (&url.URL{Scheme: "http", Host: host, Path: "/healthz"}).String()
		guestCtx, cancel := context.WithCancel(r.Context())
		defer cancel()
		guestRequest, err := http.NewRequestWithContext(guestCtx, http.MethodGet, guestURL, nil)
		if err != nil {
			writeMCPInstanceProbeProblem(w, http.StatusBadRequest, "invalid_host", "host could not be used for the health request")
			return
		}
		guestRequest.Host = host
		guestRequest.Header.Set("Accept", "application/json")
		guestRequest.Header.Set("Cache-Control", "no-cache, no-store")
		// The forwarder sends vmmd the instance named by x-faas-instance, not
		// target.InstanceID. Without the platform identity headers vmmd
		// refused every probe ("instance is required"), the probe answered
		// 502, and no MCP deployment passed qualification on production-us
		// (Hunt 14 H14-3). The deployment response check stamps them the
		// same way.
		api.PlatformIdentity{AppID: target.AppID, DeploymentID: target.DeploymentID, InstanceID: target.InstanceID, NodeID: target.NodeID}.ApplyGuestHeaders(guestRequest.Header)
		guestRequest.Header.Set("x-faas-wake-id", target.WakeID)
		guestResponse := &boundedMCPProbeResponseWriter{header: make(http.Header), cancel: cancel, maxBytes: mcpInstanceProbeMaxBodyBytes}
		forward(target).ServeHTTP(guestResponse, guestRequest)
		if guestResponse.status == 0 {
			guestResponse.status = http.StatusBadGateway
		}

		response := api.MCPInstanceHealthProbeResponse{
			AppID:              appID,
			DeploymentID:       target.DeploymentID,
			InstanceID:         target.InstanceID,
			HTTPStatus:         guestResponse.status,
			HealthBody:         string(guestResponse.body),
			HealthBodyTooLarge: guestResponse.tooLarge,
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}
}

type boundedMCPProbeResponseWriter struct {
	header   http.Header
	status   int
	body     []byte
	tooLarge bool
	cancel   context.CancelFunc
	maxBytes int
}

func (w *boundedMCPProbeResponseWriter) Header() http.Header { return w.header }

func (w *boundedMCPProbeResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *boundedMCPProbeResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	maxBytes := w.maxBytes
	if maxBytes <= 0 {
		maxBytes = mcpInstanceProbeMaxBodyBytes
	}
	remaining := maxBytes - len(w.body)
	if remaining > 0 {
		if len(body) > remaining {
			w.body = append(w.body, body[:remaining]...)
			w.tooLarge = true
			if w.cancel != nil {
				w.cancel()
			}
		} else {
			w.body = append(w.body, body...)
		}
	} else if len(body) > 0 {
		w.tooLarge = true
		if w.cancel != nil {
			w.cancel()
		}
	}
	return len(body), nil
}

func writeMCPInstanceProbeProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	api.WriteProblem(w, api.NewProblem(status, code, "MCP instance probe failed", detail))
}
