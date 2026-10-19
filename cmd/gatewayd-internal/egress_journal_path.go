package main

import "github.com/onebox-faas/faas/pkg/gateway/egressgrpc"

const gatewayEgressPendingPathEnv = "FAAS_GATEWAY_EGRESS_PENDING_PATH"

// gatewayEgressPendingPath keeps a dependency-injected path ahead of the
// process configuration and retains the production journal location by default.
func gatewayEgressPendingPath(explicit string, getenv func(string) string) string {
	if explicit != "" {
		return explicit
	}
	if configured := getenv(gatewayEgressPendingPathEnv); configured != "" {
		return configured
	}
	return egressgrpc.DefaultPendingPath
}
