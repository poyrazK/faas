// adr: 169
package main

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

const serviceProxyIdentityTTL = 5 * time.Second

// serviceProxyCallerResolver maps the source address seen on the tenant
// bridge to a live instance. HostIP is the post-MASQUERADE identity of a
// guest, so the map remains trustworthy even though every guest shares the
// same 10.0.0.0/30 network inside its namespace.
type serviceProxyCallerResolver struct {
	list   func(context.Context) ([]state.Instance, error)
	nodeID string
	now    func() time.Time
	ttl    time.Duration

	mu      sync.Mutex
	expires time.Time
	byIP    map[string]string
}

func newServiceProxyCallerResolver(list func(context.Context) ([]state.Instance, error), nodeID string) gateway.ServiceProxyCallerResolver {
	r := &serviceProxyCallerResolver{
		list:   list,
		nodeID: strings.TrimSpace(nodeID),
		now:    time.Now,
		ttl:    serviceProxyIdentityTTL,
		byIP:   make(map[string]string),
	}
	return r.Resolve
}

func (r *serviceProxyCallerResolver) Resolve(ctx context.Context, remoteAddr string) (string, error) {
	host := strings.TrimSpace(remoteAddr)
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return "", nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if now.Before(r.expires) {
		return r.byIP[ip.String()], nil
	}
	if r.list == nil {
		return "", fmt.Errorf("%w: instance lister is not wired", gateway.ErrServiceProxyUnavailable)
	}
	instances, err := r.list(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: list instances: %v", gateway.ErrServiceProxyUnavailable, err)
	}
	byIP := make(map[string]string, len(instances))
	ambiguous := make(map[string]struct{})
	for _, instance := range instances {
		if instance.AppID == "" || r.nodeID != "" && instance.NodeID != r.nodeID {
			continue
		}
		address, err := netip.ParseAddr(strings.TrimSpace(instance.HostIP))
		if err != nil {
			continue
		}
		key := address.String()
		if _, ok := ambiguous[key]; ok {
			continue
		}
		if previous, ok := byIP[key]; ok && previous != instance.AppID {
			delete(byIP, key)
			ambiguous[key] = struct{}{}
			continue
		}
		byIP[key] = instance.AppID
	}
	r.byIP = byIP
	r.expires = now.Add(r.ttl)
	return r.byIP[ip.String()], nil
}
