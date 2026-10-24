package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// internalMCPInstanceProtocolProbeHandler forwards one allowlisted read-only
// MCP JSON-RPC request to a platform-selected running replica. It does not
// accept arbitrary guest methods, hosts, or route patterns.
func internalMCPInstanceProtocolProbeHandler(
	appLookup gateway.ResolveSlugFn,
	resolveTarget mcpInstanceProbeTargetResolver,
	forward func(gateway.Target) http.Handler,
	logger *slog.Logger,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if logger != nil {
			logger.Debug("internal MCP instance protocol probe", "path", logsanitize.Field(r.URL.Path))
		}
		if r.Method != http.MethodPost {
			writeMCPInstanceProbeProblem(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is supported on this endpoint")
			return
		}
		const prefix = "/v1/internal/apps/"
		const suffix = "/mcp-probe"
		rest := strings.TrimPrefix(r.URL.Path, prefix)
		if !strings.HasSuffix(rest, suffix) {
			writeMCPInstanceProbeProblem(w, http.StatusNotFound, "not_found", "path must match /v1/internal/apps/<slug>/mcp-probe")
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
		if deploymentID == "" || instanceID == "" {
			writeMCPInstanceProbeProblem(w, http.StatusBadRequest, "invalid_request", "deployment_id and instance_id are required")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, api.MCPProbeMaxRequestBytes+1))
		if err != nil || len(body) > api.MCPProbeMaxRequestBytes {
			writeMCPInstanceProbeProblem(w, http.StatusRequestEntityTooLarge, "request_too_large", "MCP protocol probe request exceeds its size limit")
			return
		}
		var probe api.MCPInstanceProtocolProbeRequest
		if json.Unmarshal(body, &probe) != nil || !api.ValidMCPProbeHost(probe.Host) || !api.ValidMCPProbeEndpointPath(probe.Endpoint) || !api.ValidMCPProbeProtocolVersion(probe.ProtocolVersion) {
			writeMCPInstanceProbeProblem(w, http.StatusBadRequest, "invalid_request", "host, endpoint, or protocol_version is invalid")
			return
		}
		method, valid := api.MCPProbeRPCMethod(probe.RPCRequest)
		if !valid || !api.MCPProbeRPCMatchesVersion(probe.RPCRequest, probe.ProtocolVersion) {
			writeMCPInstanceProbeProblem(w, http.StatusBadRequest, "invalid_rpc_request", "rpc_request must match protocol_version and use an allowlisted read-only method")
			return
		}
		token := r.Header.Get("MCP-Probe-Authorization")
		if !api.ValidMCPProbeToken(token) {
			writeMCPInstanceProbeProblem(w, http.StatusBadRequest, "invalid_token", "MCP probe bearer token contains invalid header characters or exceeds its limit")
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
				logger.Warn("resolve MCP instance protocol target", "app_id", logsanitize.Field(appID), "deployment_id", logsanitize.Field(deploymentID), "instance_id", logsanitize.Field(instanceID), "err", logsanitize.FieldAny(err))
			}
			writeMCPInstanceProbeProblem(w, http.StatusServiceUnavailable, "target_lookup_failed", "MCP instance target could not be verified")
			return
		}
		if !found || target.AppID != appID || target.DeploymentID != deploymentID || target.InstanceID != instanceID || target.NodeID == "" {
			writeMCPInstanceProbeProblem(w, http.StatusNotFound, "instance_not_found", "instance is not a running replica of the requested deployment")
			return
		}

		guestURL := (&url.URL{Scheme: "http", Host: probe.Host, Path: probe.Endpoint}).String()
		guestCtx, cancel := context.WithCancel(r.Context())
		defer cancel()
		guestRequest, err := http.NewRequestWithContext(guestCtx, http.MethodPost, guestURL, bytes.NewReader(probe.RPCRequest))
		if err != nil {
			writeMCPInstanceProbeProblem(w, http.StatusBadRequest, "invalid_endpoint", "endpoint could not be used for the MCP request")
			return
		}
		guestRequest.Host = probe.Host
		guestRequest.Header.Set("Content-Type", "application/json")
		guestRequest.Header.Set("Accept", "application/json, text/event-stream")
		guestRequest.Header.Set("MCP-Protocol-Version", probe.ProtocolVersion)
		guestRequest.Header.Set("Mcp-Method", method)
		guestRequest.Header.Set("Cache-Control", "no-cache, no-store")
		if token != "" {
			guestRequest.Header.Set("Authorization", "Bearer "+token)
		}
		guestResponse := &boundedMCPProbeResponseWriter{header: make(http.Header), cancel: cancel, maxBytes: api.MCPProbeMaxResponseBytes}
		if serveBoundedProbe(forward(target), guestResponse, guestRequest) || guestResponse.status == 0 {
			guestResponse.status = http.StatusBadGateway
		}
		protocolVersion := boundedMCPResponseHeader(guestResponse.header.Get("MCP-Protocol-Version"), 64)
		sessionID := boundedMCPResponseHeader(guestResponse.header.Get("MCP-Session-Id"), 1024)
		sessionIDPresent := len(guestResponse.header.Values("MCP-Session-Id")) > 0
		contentType := boundedMCPResponseHeader(guestResponse.header.Get("Content-Type"), 128)
		result := api.MCPInstanceProtocolProbeResponse{
			AppID: appID, DeploymentID: target.DeploymentID, InstanceID: target.InstanceID,
			HTTPStatus: guestResponse.status, ContentType: contentType,
			ProtocolVersion: protocolVersion, SessionID: sessionID, SessionIDPresent: sessionIDPresent,
			Body: string(guestResponse.body), BodyTooLarge: guestResponse.tooLarge,
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(result)
	}
}

func boundedMCPResponseHeader(value string, limit int) string {
	if len(value) > limit {
		return ""
	}
	return value
}
