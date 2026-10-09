package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
	"github.com/onebox-faas/faas/pkg/state"
)

// This protects one configured public edge's actual HTTP/upgrade connections.
// It neither declares nor certifies that the entire public fleet enables it.
func configureRuntimeIngressProxy(proxy *gateway.InternalReverseProxy, store state.RuntimeUpgradeIngressBindingStore, getenv func(string) string) error {
	if getenv("FAAS_RUNTIME_UPGRADE_INGRESS_CONFIRMATION") != "1" {
		return nil
	}
	if store == nil {
		return state.ErrInvalidArgument
	}
	guard, err := ingress.New(proxy.Dialer, proxy.Transport, getenv("FAAS_RUNTIME_UPGRADE_INGRESS_TOKEN"), func(ctx context.Context, identity ingress.Identity) error {
		binding, err := store.AuthorizeRuntimeUpgradeGatewayIngress(ctx, identity.SlotID, identity.SessionID)
		if err != nil {
			return err
		}
		if binding.SlotID != identity.SlotID || binding.SessionID != identity.SessionID || binding.GatewayRosterRevision == "" || binding.ValidForSeconds < 1 || binding.ValidForSeconds > int(api.RuntimeUpgradeGatewayHeartbeatMaxAge/time.Second) {
			return state.ErrConflict
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("configure private ingress binding: %w", err)
	}
	protected := runtimeIngressProxy{guard}
	proxy.Dialer, proxy.Transport = protected, protected
	return nil
}

type runtimeIngressProxy struct{ *ingress.Guard }

func (p runtimeIngressProxy) DialContext(ctx context.Context, target string) (net.Conn, error) {
	conn, err := p.Guard.DialContext(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", gateway.ErrNoComputeCapacity, err)
	}
	return conn, nil
}

func (p runtimeIngressProxy) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := p.Guard.RoundTrip(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", gateway.ErrNoComputeCapacity, err)
	}
	return resp, nil
}
