package responsecheck

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
)

func (s Service) ServeCapabilities(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" {
		api.WriteProblem(w, api.ErrValidation("deployment response-check capabilities take no parameters"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, s.Capabilities(r.Context()))
}

// Capabilities reads only the trusted control listener. It never resolves an
// app, admits capacity, wakes an instance, or sends credentials to the gateway.
func (s Service) Capabilities(ctx context.Context) api.DeploymentResponseCheckCapabilities {
	result := api.DeploymentResponseCheckCapabilities{ProtocolVersion: api.DeploymentResponseCheckProtocolVersion, UnavailableReason: api.DeploymentResponseCheckGatewayUnconfigured}
	if s.GatewayURL == "" {
		return result
	}
	result.UnavailableReason = api.DeploymentResponseCheckGatewayUnavailable
	endpoint, err := url.JoinPath(s.GatewayURL, api.DeploymentResponseCheckCapabilitiesControlPath)
	if err != nil {
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, api.DeploymentResponseCheckCapabilityTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return result
	}
	client := &http.Client{Timeout: api.DeploymentResponseCheckCapabilityTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return result
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed || response.StatusCode == http.StatusNotImplemented {
		result.UnavailableReason = api.DeploymentResponseCheckGatewayUnsupported
		return result
	}
	if response.StatusCode != http.StatusOK {
		return result
	}
	wire, err := io.ReadAll(io.LimitReader(response.Body, api.DeploymentResponseCheckMaxWireBytes+1))
	var gateway api.DeploymentResponseCheckCapabilities
	if err != nil || len(wire) > api.DeploymentResponseCheckMaxWireBytes || json.Unmarshal(wire, &gateway) != nil {
		return result
	}
	if gateway.ProtocolVersion != api.DeploymentResponseCheckProtocolVersion {
		result.UnavailableReason = api.DeploymentResponseCheckGatewayUnsupported
	} else if gateway.SupportsCurrentProtocol() {
		result.Available, result.UnavailableReason = true, ""
	} else if !gateway.Available && gateway.UnavailableReason == api.DeploymentResponseCheckGatewayUnconfigured {
		result.UnavailableReason = api.DeploymentResponseCheckGatewayUnconfigured
	}
	return result
}
