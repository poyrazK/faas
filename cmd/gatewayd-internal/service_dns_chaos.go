package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type serviceDNSChaosStore interface {
	ScenarioTestDNSChaosByHostIP(ctx context.Context, nodeName, hostIP, targetWorkload string) (state.ScenarioTestDNSChaosPolicy, error)
}

// newServiceDNSChaosResolver reads source identity and the same-run policy
// together. The store matches the live IP and rejects ambiguous or recycled
// source addresses before returning any chaos plan. Results, found or not,
// are kept for api.ServiceDNSChaosCacheTTL per caller address and name, so
// guest DNS does not read the control plane on every question (bug hunt
// H33); store errors are not kept.
func newServiceDNSChaosResolver(store serviceDNSChaosStore, nodeName string) gateway.ServiceDiscoveryDNSChaosResolver {
	cache := &dnsChaosCache{entries: map[dnsChaosKey]dnsChaosEntry{}, now: time.Now}
	return func(ctx context.Context, remoteAddr, targetWorkload string) (gateway.ServiceDiscoveryDNSChaosPolicy, error) {
		if store == nil {
			return gateway.ServiceDiscoveryDNSChaosPolicy{}, nil
		}
		hostIP := strings.TrimSpace(remoteAddr)
		if host, _, err := net.SplitHostPort(hostIP); err == nil {
			hostIP = host
		}
		key := dnsChaosKey{hostIP: hostIP, target: targetWorkload}
		if policy, ok := cache.get(key); ok {
			return policy, nil
		}
		policy, err := store.ScenarioTestDNSChaosByHostIP(ctx, nodeName, hostIP, targetWorkload)
		if errors.Is(err, state.ErrInvalidArgument) {
			// A name that cannot be a workload, or a non-IPv4 source, has no
			// policy; the store refuses it before reading anything.
			return gateway.ServiceDiscoveryDNSChaosPolicy{}, nil
		}
		if err != nil {
			return gateway.ServiceDiscoveryDNSChaosPolicy{}, fmt.Errorf("load verified scenario DNS chaos policy: %w", err)
		}
		result := gateway.ServiceDiscoveryDNSChaosPolicy{RunID: policy.RunID, CallerAppID: policy.CallerAppID, Lease: policy.Lease}
		cache.put(key, result)
		return result, nil
	}
}

type dnsChaosKey struct{ hostIP, target string }

type dnsChaosEntry struct {
	policy  gateway.ServiceDiscoveryDNSChaosPolicy
	expires time.Time
}

// dnsChaosCache keeps recent chaos lookups, bounded by
// api.ServiceDNSChaosCacheMax.
type dnsChaosCache struct {
	mu      sync.Mutex
	entries map[dnsChaosKey]dnsChaosEntry
	now     func() time.Time
}

func (c *dnsChaosCache) get(key dnsChaosKey) (gateway.ServiceDiscoveryDNSChaosPolicy, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || !c.now().Before(e.expires) {
		return gateway.ServiceDiscoveryDNSChaosPolicy{}, false
	}
	return e.policy, true
}

func (c *dnsChaosCache) put(key dnsChaosKey, policy gateway.ServiceDiscoveryDNSChaosPolicy) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if len(c.entries) >= api.ServiceDNSChaosCacheMax {
		for k, e := range c.entries {
			if !now.Before(e.expires) {
				delete(c.entries, k)
			}
		}
		for k := range c.entries {
			if len(c.entries) < api.ServiceDNSChaosCacheMax*9/10 {
				break
			}
			delete(c.entries, k)
		}
	}
	c.entries[key] = dnsChaosEntry{policy: policy, expires: now.Add(api.ServiceDNSChaosCacheTTL)}
}
