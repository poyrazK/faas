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
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
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
	c.Protocol = publicEdgeProtocol(getenv)
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
	member          state.RuntimeUpgradePublicEdgeMember
	store           state.RuntimeUpgradePublicEdgeGuardStore
	log             *slog.Logger
	activity        *ingress.ActivityTracker
	activityStore   state.RuntimeUpgradePublicEdgeActivityStore
	withdrawalStore state.RuntimeUpgradePublicEdgeWithdrawalStore
	identity        http.Handler
}

func publicEdgeProtocol(getenv func(string) string) string {
	if getenv("FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_IDENTITY") == "1" {
		return "adr616/public-identity-v1"
	}
	if getenv("FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_WITHDRAWAL") == "1" {
		return "adr615/withdrawal-v1"
	}
	if getenv("FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_ACTIVITY") == "1" {
		return "adr614/generation-v1"
	}
	return "adr612/guard-v1"
}

func prepareRuntimePublicEdgeObserver(proxy *gateway.InternalReverseProxy, store state.RuntimeUpgradePublicEdgeGuardStore, config runtimePublicEdgeConfig, getenv func(string) string, log *slog.Logger) (*runtimePublicEdgeObserver, error) {
	if getenv("FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_IDENTITY") == "1" && getenv("FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_WITHDRAWAL") != "1" {
		return nil, fmt.Errorf("public edge identity requires public edge withdrawal")
	}
	if getenv("FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_WITHDRAWAL") == "1" && getenv("FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_ACTIVITY") != "1" {
		return nil, fmt.Errorf("public edge withdrawal requires public ingress activity")
	}
	if getenv("FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_CONFIRMATION") != "1" {
		if getenv("FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_ACTIVITY") == "1" {
			return nil, fmt.Errorf("public ingress activity requires public edge confirmation")
		}
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
	// Bind the fingerprint to the mode actually installed below, even if a
	// private caller supplied an incomplete startup configuration descriptor.
	config.Protocol = publicEdgeProtocol(getenv)
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode public edge startup config: %w", err)
	}
	digest := sha256.Sum256(encoded)
	member := state.RuntimeUpgradePublicEdgeMember{SlotID: slot, SessionID: session, ConfigSHA256: hex.EncodeToString(digest[:])}
	o := &runtimePublicEdgeObserver{member: member, store: store, log: log}
	if err := o.configureActivity(proxy, getenv); err != nil {
		return nil, err
	}
	if getenv("FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_WITHDRAWAL") == "1" {
		var ok bool
		o.withdrawalStore, ok = store.(state.RuntimeUpgradePublicEdgeWithdrawalStore)
		if !ok {
			return nil, fmt.Errorf("public edge withdrawal requires PostgreSQL withdrawal store")
		}
	}
	if err := o.configureIdentity(getenv); err != nil {
		return nil, err
	}
	log.Info("private public edge guard awaits inventory review", "public_edge_slot_id", slot, "public_edge_session_id", session, "config_sha256", member.ConfigSHA256)
	return o, nil
}

func (o *runtimePublicEdgeObserver) configureIdentity(getenv func(string) string) error {
	if getenv("FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_IDENTITY") != "1" {
		return nil
	}
	id := ingress.PublicEdgeIdentity{SlotID: o.member.SlotID, SessionID: o.member.SessionID, ConfigSHA256: o.member.ConfigSHA256}
	h, err := ingress.NewPublicIdentityHandler(getenv("FAAS_RUNTIME_UPGRADE_INGRESS_TOKEN"), id)
	if err != nil {
		return err
	}
	o.identity = h
	return nil
}

func (o *runtimePublicEdgeObserver) identityHandler() http.Handler {
	if o == nil {
		return nil
	}
	return o.identity
}

func (o *runtimePublicEdgeObserver) configureActivity(proxy *gateway.InternalReverseProxy, getenv func(string) string) error {
	if getenv("FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_ACTIVITY") != "1" {
		return nil
	}
	store, ok := o.store.(state.RuntimeUpgradePublicEdgeActivityStore)
	if !ok {
		return fmt.Errorf("public ingress activity requires PostgreSQL generation store")
	}
	o.activity, o.activityStore = ingress.NewActivityTracker(), store
	guard := proxy.Dialer.(runtimeIngressProxy).Guard
	tracked, err := guard.WithAdmissions(func() (ingress.Authorize, func(), error) {
		a, err := o.activity.Begin()
		if err != nil {
			return nil, nil, err
		}
		authorize := func(ctx context.Context, identity ingress.Identity) error {
			b, err := store.AuthorizeRuntimeUpgradePublicEdgeIngress(ctx, o.member, identity.SlotID, identity.SessionID)
			if err != nil {
				return err
			}
			if b.SlotID != identity.SlotID || b.SessionID != identity.SessionID || b.GatewayRevision != b.GatewayRosterRevision || b.ValidForSeconds < 1 || b.ValidForSeconds > int(api.RuntimeUpgradeGatewayHeartbeatMaxAge/time.Second) {
				return state.ErrConflict
			}
			return a.Bind(ingress.Generation{PublicRevision: b.PublicRevision, GatewayRevision: b.GatewayRevision})
		}
		return authorize, a.Finish, nil
	})
	if err != nil {
		return err
	}
	protected := runtimeIngressProxy{tracked}
	proxy.Dialer, proxy.Transport = protected, protected
	return nil
}

func (o *runtimePublicEdgeObserver) recordActivity(ctx context.Context) error {
	if o.activity == nil {
		return nil
	}
	return o.activityStore.RecordRuntimeUpgradePublicEdgeActivity(ctx, o.member, func(g state.RuntimeUpgradeIngressGeneration) state.RuntimeUpgradePublicEdgeActivity {
		a := o.activity.Snapshot(ingress.Generation{PublicRevision: g.PublicRevision, GatewayRevision: g.GatewayRevision})
		return state.RuntimeUpgradePublicEdgeActivity{Version: a.Version, Known: a.Known, Pending: a.Pending, Current: a.Current, Previous: a.Previous}
	})
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
		err := o.repair(poll)
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

// Withdrawal precedes current facts: removed sessions cannot publish guard
// facts, but must keep repairing their irreversible admission fence and seal.
func (o *runtimePublicEdgeObserver) repair(ctx context.Context) error {
	if o.withdrawalStore != nil {
		withdrawn, err := o.withdrawalStore.RepairRuntimeUpgradePublicEdgeWithdrawal(ctx, o.member, func(id string) (state.RuntimeUpgradePublicEdgeWithdrawalSnapshot, error) {
			a, err := o.activity.Withdraw(id)
			return state.RuntimeUpgradePublicEdgeWithdrawalSnapshot{ID: a.ID, FenceID: a.FenceID, Version: a.Version, Closed: a.Closed, Known: a.Known, Active: a.Active}, err
		})
		if err != nil || withdrawn {
			return err
		}
	}
	if err := o.store.RecordRuntimeUpgradePublicEdgeGuard(ctx, o.member); err != nil {
		return err
	}
	return o.recordActivity(ctx)
}
