package main

// adr: 20261110081523 — the compute edge reaches its own gatewayd-internal
// over binary HTTP.

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/onebox-faas/faas/pkg/gateway"
)

const (
	// edgeInternalHopEnv picks the hop to this node's gatewayd-internal:
	// http1, the default, or binary.
	edgeInternalHopEnv = "FAAS_EDGE_INTERNAL_HOP"
	// internalHopSocketEnv is gatewayd-internal's binary HTTP socket.
	internalHopSocketEnv     = "FAAS_INTERNAL_HOP_SOCKET"
	defaultInternalHopSocket = "/run/faas/gatewayd-internal-hop.sock"
)

// configureBinaryHop sends a compute edge's requests to its own node's
// gatewayd-internal in binary HTTP when FAAS_EDGE_INTERNAL_HOP=binary.
// Requests to other nodes, upgrades and every request while the hop socket
// cannot be dialled stay on HTTP/1.1. It needs per-node keep-alive
// (FAAS_INTERNAL_KEEPALIVE_PER_NODE), whose transport chooses the node
// before the connection; without it the switch is ignored with a warning.
func configureBinaryHop(proxy *gateway.InternalReverseProxy, lfd *localFirstDialer, m *edgeMetrics, getenv func(string) string, log *slog.Logger) error {
	switch v := strings.TrimSpace(getenv(edgeInternalHopEnv)); v {
	case "", "http1":
		return nil
	case "binary":
	default:
		return fmt.Errorf("gatewayd-public: %s=%q, want http1 or binary", edgeInternalHopEnv, v)
	}
	if lfd == nil {
		return fmt.Errorf("gatewayd-public: %s=binary is for a compute edge (%s=%s)", edgeInternalHopEnv, publicEdgeModeEnv, edgeModeCompute)
	}
	if !proxy.WithBinaryHop(m.observeHopFallback) {
		log.Warn("gatewayd-public: " + edgeInternalHopEnv + "=binary ignored: it needs " + internalKeepAlivePerNodeEnv + "=1")
		return nil
	}
	path := strings.TrimSpace(getenv(internalHopSocketEnv))
	if path == "" {
		path = defaultInternalHopSocket
	}
	lfd.withBinaryHop(gateway.NewUnixSocketDialer(path))
	log.Info("gatewayd-public: binary hop to this node's gatewayd-internal (ADR-20261110081523)", "socket", path)
	return nil
}
