package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	mcpInstanceProtocolProbeTimeout = 10 * time.Second
	mcpInstanceProtocolProbeMaxWire = 4 << 20
)

// postMCPInstanceProtocolProbe sends one allowlisted read-only MCP request to
// an exact running replica. Workload tokens use a separate header from the
// Gregale API credential and are forwarded only for this request.
func (s *server) postMCPInstanceProtocolProbe(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	deploymentUUID, err := uuid.Parse(r.PathValue("deployment"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("deployment must be a valid UUID"))
		return
	}
	instanceUUID, err := uuid.Parse(r.PathValue("instance"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("instance must be a valid UUID"))
		return
	}
	deploymentID, instanceID := deploymentUUID.String(), instanceUUID.String()
	deployment, err := s.store.DeploymentByID(r.Context(), deploymentID)
	if errors.Is(err, state.ErrNotFound) || err == nil && (deployment.AppID != app.ID || deployment.Status != state.DeploySnapshotting && deployment.Status != state.DeployLive) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Deployment not found", "deployment is not an active or verifying deployment for this app"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not verify the requested deployment"))
		return
	}
	instance, err := s.store.InstanceByID(r.Context(), instanceID)
	if errors.Is(err, state.ErrNotFound) || err == nil && (instance.AppID != app.ID || instance.DeploymentID != deploymentID || instance.State != string(state.StateRunning)) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Instance not found", "instance is not a running replica of the requested deployment"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not verify the requested instance"))
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, api.MCPProbeMaxRequestBytes+1))
	if err != nil || len(body) > api.MCPProbeMaxRequestBytes {
		api.WriteProblem(w, api.ErrValidation("MCP protocol probe request exceeds its size limit"))
		return
	}
	var probe api.MCPInstanceProtocolProbeRequest
	if json.Unmarshal(body, &probe) != nil || !api.ValidMCPProbeHost(probe.Host) || !api.ValidMCPProbeEndpointPath(probe.Endpoint) || !api.ValidMCPProbeProtocolVersion(probe.ProtocolVersion) {
		api.WriteProblem(w, api.ErrValidation("host, endpoint, or protocol_version is invalid"))
		return
	}
	method, valid := api.MCPProbeRPCMethod(probe.RPCRequest)
	if !valid || !api.MCPProbeRPCMatchesVersion(probe.RPCRequest, probe.ProtocolVersion) {
		api.WriteProblem(w, api.ErrValidation("rpc_request must match protocol_version and use an allowlisted read-only MCP method"))
		return
	}
	token := r.Header.Get("MCP-Probe-Authorization")
	if !api.ValidMCPProbeToken(token) {
		api.WriteProblem(w, api.ErrValidation("MCP probe bearer token contains invalid header characters or exceeds its limit"))
		return
	}
	controlURL := s.gatewayControlURL(r.Context())
	if controlURL == "" {
		api.WriteProblem(w, api.ErrCapacity("gateway instance probe is unavailable"))
		return
	}
	endpoint, err := url.JoinPath(controlURL, "v1", "internal", "apps", app.Slug, "mcp-probe")
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("gateway instance probe is unavailable"))
		return
	}
	query := url.Values{}
	query.Set("deployment_id", deploymentID)
	query.Set("instance_id", instanceID)
	internalBody, err := json.Marshal(probe)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("MCP protocol probe request is invalid"))
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint+"?"+query.Encode(), strings.NewReader(string(internalBody)))
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("gateway instance probe is unavailable"))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("MCP-Probe-Authorization", token)
	}
	probeCtx, cancel := context.WithTimeout(r.Context(), mcpInstanceProtocolProbeTimeout)
	defer cancel()
	req = req.WithContext(probeCtx)
	resp, err := (&http.Client{Timeout: mcpInstanceProtocolProbeTimeout}).Do(req)
	if err != nil {
		if s.log != nil {
			s.log.Warn("apid to gatewayd MCP protocol probe failed", "app_id", app.ID, "deployment_id", deploymentID, "instance_id", instanceID, "method", method, "err", err)
		}
		api.WriteProblem(w, api.ErrCapacity("gateway could not reach the requested MCP instance"))
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Instance changed", "instance stopped or changed deployment before the probe completed"))
		return
	}
	if resp.StatusCode != http.StatusOK {
		api.WriteProblem(w, api.ErrCapacity("gateway could not verify the requested MCP instance"))
		return
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, mcpInstanceProtocolProbeMaxWire+1))
	if err != nil || len(responseBody) > mcpInstanceProtocolProbeMaxWire {
		api.WriteProblem(w, api.ErrCapacity("gateway MCP protocol probe response exceeded its size limit"))
		return
	}
	var result api.MCPInstanceProtocolProbeResponse
	if json.Unmarshal(responseBody, &result) != nil || result.AppID != app.ID || result.DeploymentID != deploymentID || result.InstanceID != instanceID || result.HTTPStatus < 100 || result.HTTPStatus > 599 || len(result.Body) > api.MCPProbeMaxResponseBytes || len(result.ContentType) > 128 || len(result.ProtocolVersion) > 64 || len(result.SessionID) > 1024 {
		api.WriteProblem(w, api.ErrCapacity("gateway MCP protocol probe identity or response was invalid"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}
