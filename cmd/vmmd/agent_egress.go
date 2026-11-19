package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/agentproxy"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// agentEgressRouteTable is the agent tunnel's routing table, the
// agent_egress_client role's faas_agent_egress_route_table.
const agentEgressRouteTable = 51821

// agentEgressConfigFromEnv reads the agent tunnel the vmmd egress drop-in
// declares (ADR-20261117185346): FAAS_AGENT_EGRESS_IFACE and
// FAAS_AGENT_EGRESS_SLICE, this node's slice of the agent tunnel.
func agentEgressConfigFromEnv(getenv func(string) string) (fcvm.AgentEgressConfig, error) {
	iface := strings.TrimSpace(getenv("FAAS_AGENT_EGRESS_IFACE"))
	rawSlice := strings.TrimSpace(getenv("FAAS_AGENT_EGRESS_SLICE"))
	if iface == "" || rawSlice == "" {
		return fcvm.AgentEgressConfig{}, errors.New("vmmd: FAAS_AGENT_EGRESS_GATEWAY=1 needs FAAS_AGENT_EGRESS_IFACE and FAAS_AGENT_EGRESS_SLICE; declare egress.agent_gateway and converge the node")
	}
	if err := validatePublicIface(iface); err != nil {
		return fcvm.AgentEgressConfig{}, fmt.Errorf("vmmd: FAAS_AGENT_EGRESS_IFACE %q invalid: %w", iface, err)
	}
	slice, err := netip.ParsePrefix(rawSlice)
	if err != nil {
		return fcvm.AgentEgressConfig{}, fmt.Errorf("vmmd: FAAS_AGENT_EGRESS_SLICE %q invalid: %w", rawSlice, err)
	}
	cfg := fcvm.AgentEgressConfig{Iface: iface, Slice: slice, Table: agentEgressRouteTable,
		HandshakeMaxAge: time.Duration(api.AgentEgressHandshakeMaxAgeSec) * time.Second}
	// ADR-20261117185346 §4: with the gateway's proxy on, every agent VM is
	// registered with it before boot and renewed at a third of its TTL.
	if raw := strings.TrimSpace(getenv("FAAS_AGENT_EGRESS_PROXY_URL")); raw != "" {
		client, err := agentproxy.NewClient(raw, &http.Client{Timeout: 10 * time.Second})
		if err != nil {
			return fcvm.AgentEgressConfig{}, fmt.Errorf("vmmd: FAAS_AGENT_EGRESS_PROXY_URL: %w", err)
		}
		cfg.Proxy = client
		cfg.ProxyRenewEvery = time.Duration(api.AgentEgressRegistrationTTLSec) * time.Second / 3
	}
	return cfg, nil
}
