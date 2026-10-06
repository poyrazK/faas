package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/gatewayconfirmation"
	"github.com/onebox-faas/faas/pkg/state"
)

// Capture selected startup configuration, rather than hashing unused fallback
// env vars or credentials. The pool's changing endpoints remain guarded by
// ADR-612 on each actual connection; this hash is not an endpoint inventory.
type runtimePublicEdgeConfig struct {
	Protocol, ListenAddress, NodeName, UpstreamMode, UpstreamTarget string
	H2C                                                             bool
	TrustedIngressCIDRs                                             []string
}

func publicEdgeConfig(mode internalUpstreamMode, h2c bool, listen string, trusted []netip.Prefix, getenv func(string) string) runtimePublicEdgeConfig {
	c := runtimePublicEdgeConfig{Protocol: "adr612/guard-v1", ListenAddress: listen, NodeName: strings.TrimSpace(getenv("FAAS_NODE_NAME")), H2C: h2c, TrustedIngressCIDRs: make([]string, 0, len(trusted))}
	switch mode {
	case internalUpstreamDatabase:
		c.UpstreamMode, c.UpstreamTarget = "database", "compute_gateway_pool"
	case internalUpstreamStatic:
		c.UpstreamMode = "tcp"
		// Startup already validated this URL; only its host is dialed.
		if target, err := url.Parse(strings.TrimSpace(getenv("FAAS_INTERNAL_TARGET"))); err == nil {
			c.UpstreamTarget = target.Host
		}
	default:
		c.UpstreamMode, c.UpstreamTarget = "unix", getenv("FAAS_INTERNAL_SOCKET")
		if c.UpstreamTarget == "" {
			c.UpstreamTarget = defaultInternalSocket
		}
	}
	for _, prefix := range trusted {
		c.TrustedIngressCIDRs = append(c.TrustedIngressCIDRs, prefix.Masked().String())
	}
	slices.Sort(c.TrustedIngressCIDRs)
	c.TrustedIngressCIDRs = slices.Compact(c.TrustedIngressCIDRs)
	return c
}

type runtimePublicEdgeObserver struct {
	member state.RuntimeUpgradePublicEdgeMember
	store  state.RuntimeUpgradePublicEdgeGuardStore
	log    *slog.Logger
}

func prepareRuntimePublicEdgeObserver(proxy *gateway.InternalReverseProxy, store state.RuntimeUpgradePublicEdgeGuardStore, config runtimePublicEdgeConfig, getenv func(string) string, log *slog.Logger) (*runtimePublicEdgeObserver, error) {
	if getenv("FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_CONFIRMATION") != "1" {
		return nil, nil
	}
	dialer, dialOK := proxy.Dialer.(runtimeIngressProxy)
	transport, transportOK := proxy.Transport.(runtimeIngressProxy)
	if !dialOK || !transportOK || dialer.Guard == nil || dialer.Guard != transport.Guard || store == nil {
		return nil, fmt.Errorf("public edge confirmation requires installed connection guard and PostgreSQL store")
	}
	slot, session := getenv("FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_SLOT_ID"), uuid.NewString()
	if err := gatewayconfirmation.ValidateIdentity(slot, session); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode public edge startup config: %w", err)
	}
	digest := sha256.Sum256(encoded)
	member := state.RuntimeUpgradePublicEdgeMember{SlotID: slot, SessionID: session, ConfigSHA256: hex.EncodeToString(digest[:])}
	log.Info("private public edge guard awaits inventory review", "public_edge_slot_id", slot, "public_edge_session_id", session, "config_sha256", member.ConfigSHA256)
	return &runtimePublicEdgeObserver{member: member, store: store, log: log}, nil
}

// Attach starts facts only when Serve has acquired the real listener. Stop on
// listener/run cancellation and on Shutdown, before long-lived traffic drains.
func (o *runtimePublicEdgeObserver) attach(ctx context.Context, server *http.Server) func() {
	if o == nil {
		return func() {}
	}
	live, cancel := context.WithCancel(ctx)
	server.RegisterOnShutdown(cancel)
	server.BaseContext = func(net.Listener) context.Context { go o.run(live); return ctx }
	return cancel
}

func (o *runtimePublicEdgeObserver) run(ctx context.Context) {
	ticker := time.NewTicker(api.RuntimeUpgradeGatewayRepairInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		poll, cancel := context.WithTimeout(ctx, api.RuntimeUpgradeGatewayRepairTimeout)
		err := o.store.RecordRuntimeUpgradePublicEdgeGuard(poll, o.member)
		cancel()
		if err != nil && ctx.Err() == nil {
			o.log.Warn("private public edge guard pending review or retry")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
