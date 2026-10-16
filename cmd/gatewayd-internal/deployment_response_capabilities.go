package main

import (
	"encoding/json"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

func (c deploymentResponseChecker) configured() bool {
	return c.store != nil && c.picker != nil && c.ensure != nil && c.forward != nil
}

// Capabilities is exposed only on the trusted control listener. Checking the
// installed forwarding dependencies does not read a target or wake a guest.
func (c deploymentResponseChecker) Capabilities(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		c.problem(w, http.StatusMethodNotAllowed, "invalid_request", "use GET to read deployment response-check capabilities")
		return
	}
	if r.URL.RawQuery != "" {
		c.problem(w, http.StatusBadRequest, "invalid_request", "deployment response-check capabilities take no parameters")
		return
	}
	result := api.DeploymentResponseCheckCapabilities{ProtocolVersion: api.DeploymentResponseCheckProtocolVersion, Available: c.configured()}
	if !result.Available {
		result.UnavailableReason = api.DeploymentResponseCheckGatewayUnconfigured
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
