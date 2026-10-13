package main

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// newServiceDNSChaosResolver reads source identity and the same-run policy
// together. The store matches the live IP and rejects ambiguous or recycled
// source addresses before returning any chaos plan.
func newServiceDNSChaosResolver(store state.Store, nodeName string) gateway.ServiceDiscoveryDNSChaosResolver {
	return func(ctx context.Context, remoteAddr, targetWorkload string) (gateway.ServiceDiscoveryDNSChaosPolicy, error) {
		if store == nil {
			return gateway.ServiceDiscoveryDNSChaosPolicy{}, nil
		}
		hostIP := strings.TrimSpace(remoteAddr)
		if host, _, err := net.SplitHostPort(hostIP); err == nil {
			hostIP = host
		}
		policy, err := store.ScenarioTestDNSChaosByHostIP(ctx, nodeName, hostIP, targetWorkload)
		if err != nil {
			return gateway.ServiceDiscoveryDNSChaosPolicy{}, fmt.Errorf("load verified scenario DNS chaos policy: %w", err)
		}
		return gateway.ServiceDiscoveryDNSChaosPolicy{RunID: policy.RunID, CallerAppID: policy.CallerAppID, Lease: policy.Lease}, nil
	}
}
