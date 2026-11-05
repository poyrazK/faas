package main

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/role"
)

// privateControlListenEnv names the compute node's private control listener
// (ADR-20261022004500), for example 0.0.0.0:9097. Empty disables it.
const privateControlListenEnv = "FAAS_GATEWAY_PRIVATE_CONTROL_LISTEN"

// privateControlAddr returns the private control listener address, or ""
// when it is off. Only a compute-only node serves it: on a single box apid
// reaches the loopback control listener directly.
func privateControlAddr(boxRole role.Role, getenv func(string) string) string {
	if boxRole != role.RoleComputeOnly {
		return ""
	}
	return strings.TrimSpace(getenv(privateControlListenEnv))
}

// newPrivateControlMux serves only the control routes apid calls across
// hosts: the per-app dispatcher under /v1/internal/apps/ (routes, streaming
// cap, MCP replica health and protocol probes, response checks), the
// response-check capabilities path, and the realtimed control proxy under
// /v1/internal/realtime/. apid prefers it here: the data listener serves it
// too, for apid releases that predate the private route, and only the
// edge's refusal of that path on app hosts keeps it from customers. The loopback
// listener's /metrics, pprof, quota, services and edge-rule reset stay
// loopback-only. A nil handler leaves its path unrouted (404).
func newPrivateControlMux(apps, responseCheckCapabilities, realtimeControl http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	if apps != nil {
		mux.Handle("/v1/internal/apps/", apps)
	}
	if realtimeControl != nil {
		mux.Handle("/v1/internal/realtime/", realtimeControl)
	}
	if responseCheckCapabilities != nil {
		mux.Handle(api.DeploymentResponseCheckCapabilitiesControlPath, responseCheckCapabilities)
	}
	return mux
}
