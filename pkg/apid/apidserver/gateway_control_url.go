package apidserver

import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// gatewayControlFromRegistryEnv makes apid reach gatewayd-internal's private
// control listener on an active compute node instead of the loopback
// FAAS_GATEWAYD_CONTROL_URL (ADR-20261022004500). A split control plane runs
// no gatewayd-internal, so the loopback default refused every MCP replica
// probe and qualification there (Hunt 11 H11-31, Hunt 12 H12-9).
const gatewayControlFromRegistryEnv = "FAAS_GATEWAYD_CONTROL_FROM_REGISTRY"

// WithGatewaydControlFromRegistry turns on per-request compute-node
// resolution of the gateway control URL.
func (s *server) WithGatewaydControlFromRegistry(on bool) *server {
	s.gatewaydControlFromRegistry = on
	return s
}

// gatewayControlURL is the base URL of a gatewayd-internal control listener
// for one request: the configured URL on a single box, or, with
// FAAS_GATEWAYD_CONTROL_FROM_REGISTRY, the private control port of an active
// compute node taken in turn. Any node serves these routes: the MCP probes
// and response checks forward to the target instance's own node, and the
// streaming-cap rules are the same everywhere. "" means unavailable.
func (s *server) gatewayControlURL(ctx context.Context) string {
	if !s.gatewaydControlFromRegistry {
		return s.gatewaydControlURL
	}
	if s.store == nil {
		return ""
	}
	nodes, err := s.store.ListComputeNodes(ctx, false)
	if err != nil {
		if s.log != nil {
			s.log.Warn("gateway control: list compute nodes", "err", err)
		}
		return ""
	}
	target := daemonMetricsTarget(api.GatewaydPrivateControlPort)
	var hosts []string
	for _, node := range nodes {
		if !node.Active || node.GatewayTargetURL == nil {
			continue
		}
		if hostPort, ok := target(*node.GatewayTargetURL); ok {
			hosts = append(hosts, hostPort)
		}
	}
	if len(hosts) == 0 {
		return ""
	}
	sort.Strings(hosts)
	next := s.gatewayControlNext.Add(1) - 1
	return "http://" + hosts[next%uint64(len(hosts))]
}
