package main

import (
	"context"
	"net"
	"net/http"
	"net/netip"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type qualificationNetworkCallerStore interface {
	EnvironmentQualificationNetworkNode(context.Context, string, string) (string, error)
	EnvironmentQualificationNetworkCaller(context.Context, string, string) (bool, error)
}

// Ordinary service requests must reject private qualification network slots.
// DNS alias lookup remains a discoverability check; the dedicated qualification
// HTTP route binds every request to its actual node, address, graph and lease.
func ordinaryQualificationCallerGuard(ctx context.Context, store qualificationNetworkCallerStore, nodeName, remote string) error {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.Is4() {
		return gateway.ErrServiceProxyDenied
	}
	nodeID, err := store.EnvironmentQualificationNetworkNode(ctx, nodeName, ip.String())
	if err != nil {
		return err
	}
	if nodeID == "" {
		return nil // Ordinary identity resolution will reject an unknown slot.
	}
	held, err := store.EnvironmentQualificationNetworkCaller(ctx, nodeID, ip.String())
	if err != nil {
		return err
	}
	if held {
		return gateway.ErrServiceProxyDenied
	}
	return nil
}

func newEnvironmentQualificationServiceProxy(store *state.PgStore, nodeName string, forward func(gateway.Target) http.Handler, next http.Handler) http.Handler {
	return gateway.NewEnvironmentQualificationServiceProxy(gateway.EnvironmentQualificationServiceProxyConfig{
		Store: store, Forward: forward, Next: next,
		ResolveNode: func(ctx context.Context, hostIP string) (string, error) {
			if nodeName == "" {
				// A legacy unnamed listener can deny held callers through its
				// ordinary guard, but cannot authorize private graph execution.
				return "", nil
			}
			return store.EnvironmentQualificationNetworkNode(ctx, nodeName, hostIP)
		},
	})
}
