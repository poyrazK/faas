package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/vmmdhop"
	"github.com/onebox-faas/faas/pkg/wire"
)

// forwardHopEnv makes gatewayd-internal offer the light forward hop to each
// vmmd for ForwardHTTPStream (ADR-20261121202814). Off by default.
const forwardHopEnv = "FAAS_GATEWAY_VMMD_FORWARD_HOP"

type computeNodeByID interface {
	ComputeNodeByID(ctx context.Context, id string) (state.ComputeNode, error)
}

// forwardHopFactory builds a node's hop client with the TLS config its gRPC
// dial uses: the gateway's client certificate and the vmmd leaf pinned to
// the node's name (wire.ExpectPeerCN, ADR-20261108124106). A node without a
// TLS target (the local data socket) or without a name stays on gRPC.
func forwardHopFactory(store computeNodeByID, vmmdTLS *tls.Config, local *localVmmd, reg prometheus.Registerer, log *slog.Logger) gateway.ForwardHopFactory {
	calls := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_vmmd_forward_hop_total",
		Help: "ForwardHTTPStream calls by how they reached vmmd (ADR-20261121202814): hop, fallback (vmmd does not offer it), dial_error.",
	}, []string{"result"})
	if reg != nil {
		if err := reg.Register(calls); err != nil {
			var already prometheus.AlreadyRegisteredError
			if errors.As(err, &already) {
				if existing, ok := already.ExistingCollector.(*prometheus.CounterVec); ok {
					calls = existing
				}
			}
		}
	}
	return func(ctx context.Context, nodeID, target string) *vmmdhop.Client {
		address, ok := forwardHopAddress(target)
		if !ok {
			return nil
		}
		base := local.tlsFor(target, vmmdTLS)
		if base == nil {
			return nil
		}
		node, err := store.ComputeNodeByID(ctx, nodeID)
		if err != nil || node.Name == "" {
			return nil
		}
		cfg := wire.ExpectPeerCN(base, node.Name)
		if cfg.ServerName == "" {
			// As gRPC's TLS credentials do: verify the target's host.
			host, _, _ := net.SplitHostPort(address)
			cfg.ServerName = host
		}
		c, err := vmmdhop.NewClient(address, cfg)
		if err != nil {
			log.Warn("gateway: forward hop client not built; node stays on gRPC", "node", nodeID, "err", err)
			return nil
		}
		c.Result = func(result string) { calls.WithLabelValues(result).Inc() }
		return c
	}
}

// forwardHopAddress is the host:port of a TCP or DNS vmmd target, parsed as
// the gRPC dial parses it. Unix targets have no TLS and keep gRPC.
func forwardHopAddress(target string) (string, bool) {
	normalized, err := wire.NormalizeLegacyTarget(target)
	if err != nil {
		return "", false
	}
	t, err := wire.ParseTarget(normalized)
	if err != nil || (t.Scheme != wire.SchemeTCP && t.Scheme != wire.SchemeDNS) {
		return "", false
	}
	address := strings.TrimLeft(t.Address, "/")
	if _, port, err := net.SplitHostPort(address); err != nil || port == "" || port == "0" {
		return "", false
	}
	return address, true
}
