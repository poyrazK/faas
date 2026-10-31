package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	mcpInstanceHealthProbeTimeout = 10 * time.Second
	mcpInstanceHealthProbeMaxWire = 512 << 10
	mcpInstanceHealthMaxBodyBytes = 64 << 10
)

// getMCPInstanceHealth probes the fixed /healthz path on one exact running
// deployment replica. The authenticated app-read boundary and durable
// ownership checks run here; gatewayd then stamps the selected target identity
// into the response envelope after forwarding directly to that instance.
func (s *server) getMCPInstanceHealth(w http.ResponseWriter, r *http.Request, acct state.Account) {
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
	host := r.URL.Query().Get("host")
	if !api.ValidMCPProbeHost(host) {
		api.WriteProblem(w, api.ErrValidation("host must be a hostname and optional port without a URL path"))
		return
	}
	controlURL := s.gatewayControlURL(r.Context())
	if controlURL == "" {
		api.WriteProblem(w, api.ErrCapacity("gateway instance probe is unavailable"))
		return
	}
	endpoint, err := url.JoinPath(controlURL, "v1", "internal", "apps", app.Slug, "mcp-health")
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("gateway instance probe is unavailable"))
		return
	}
	query := url.Values{}
	query.Set("deployment_id", deploymentID)
	query.Set("instance_id", instanceID)
	query.Set("host", host)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("gateway instance probe is unavailable"))
		return
	}
	probeCtx, cancel := context.WithTimeout(r.Context(), mcpInstanceHealthProbeTimeout)
	defer cancel()
	req = req.WithContext(probeCtx)
	resp, err := (&http.Client{Timeout: mcpInstanceHealthProbeTimeout}).Do(req)
	if err != nil {
		if s.log != nil {
			s.log.Warn("apid to gatewayd MCP instance probe failed", "app_id", app.ID, "deployment_id", deploymentID, "instance_id", instanceID, "err", err)
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, mcpInstanceHealthProbeMaxWire+1))
	if err != nil || len(body) > mcpInstanceHealthProbeMaxWire {
		api.WriteProblem(w, api.ErrCapacity("gateway MCP probe response exceeded its size limit"))
		return
	}
	var result api.MCPInstanceHealthProbeResponse
	if err := json.Unmarshal(body, &result); err != nil {
		api.WriteProblem(w, api.ErrCapacity("gateway returned an invalid MCP probe response"))
		return
	}
	if result.AppID != app.ID || result.DeploymentID != deploymentID || result.InstanceID != instanceID || result.HTTPStatus < 100 || result.HTTPStatus > 599 || len(result.HealthBody) > mcpInstanceHealthMaxBodyBytes {
		api.WriteProblem(w, api.ErrCapacity("gateway MCP probe identity or response was invalid"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}
