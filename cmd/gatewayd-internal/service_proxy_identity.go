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

const (
	serviceProxyIdentityTTL = 5 * time.Second
	// Keep this value aligned with pkg/netns.ServiceProxyPort. The daemon
	// owns the listener and cannot import the netns renderer package because
	// that package is vmmd-owned by the repository's dependency policy.
	serviceProxyPort      = 10080
	serviceProxyHTTPSPort = 443
)

// serviceProxyCallerResolver maps the source address seen on the tenant
// bridge to a live instance. HostIP is the post-MASQUERADE identity of a
// guest, so the map remains trustworthy even though every guest shares the
// same 10.0.0.0/30 network inside its namespace.
type serviceProxyCallerResolver struct {
	list func(context.Context) ([]state.Instance, error)
	// A fresh indexed lookup is required for the guest listener: VM network
	// slots (and therefore HostIPs) can be reused immediately on teardown.
	// A cached IP -> deployment mapping could attribute the next guest's
	// release-graph call to the previous tenant for up to one cache TTL.
	lookup func(context.Context, string, string) ([]state.Instance, error)
	nodeID string
	now    func() time.Time
	ttl    time.Duration

	mu      sync.Mutex
	expires time.Time
	byIP    map[string]serviceProxyInstanceIdentity
}

type serviceProxyInstanceIdentity struct {
	appID        string
	deploymentID string
}

func newServiceProxyCallerResolver(list func(context.Context) ([]state.Instance, error), nodeID string) gateway.ServiceProxyCallerResolver {
	r := newServiceProxyCallerIdentityResolver(list, nodeID)
	return r.Resolve
}

func newServiceProxyCallerIdentityResolver(list func(context.Context) ([]state.Instance, error), nodeID string) *serviceProxyCallerResolver {
	return &serviceProxyCallerResolver{
		list:   list,
		nodeID: strings.TrimSpace(nodeID),
		now:    time.Now,
		ttl:    serviceProxyIdentityTTL,
		byIP:   make(map[string]serviceProxyInstanceIdentity),
	}
}

func (r *serviceProxyCallerResolver) Resolve(ctx context.Context, remoteAddr string) (string, error) {
	identity, err := r.resolveIdentity(ctx, remoteAddr)
	return identity.appID, err
}

func (r *serviceProxyCallerResolver) ResolveIdentity(ctx context.Context, remoteAddr string) (string, string, error) {
	identity, err := r.resolveIdentity(ctx, remoteAddr)
	return identity.appID, identity.deploymentID, err
}

func (r *serviceProxyCallerResolver) resolveIdentity(ctx context.Context, remoteAddr string) (serviceProxyInstanceIdentity, error) {
	host := strings.TrimSpace(remoteAddr)
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return serviceProxyInstanceIdentity{}, nil
	}
	if r.lookup != nil {
		instances, err := r.lookup(ctx, r.nodeID, ip.String())
		if err != nil {
			return serviceProxyInstanceIdentity{}, fmt.Errorf("%w: lookup caller instance: %w", gateway.ErrServiceProxyUnavailable, err)
		}
		return identityForHostIP(instances, r.nodeID, ip.String()), nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if now.Before(r.expires) {
		return r.byIP[ip.String()], nil
	}
	if r.list == nil {
		return serviceProxyInstanceIdentity{}, fmt.Errorf("%w: instance lister is not wired", gateway.ErrServiceProxyUnavailable)
	}
	instances, err := r.list(ctx)
	if err != nil {
		return serviceProxyInstanceIdentity{}, fmt.Errorf("%w: list instances: %w", gateway.ErrServiceProxyUnavailable, err)
	}
	byIP := make(map[string]serviceProxyInstanceIdentity, len(instances))
	ambiguous := make(map[string]struct{})
	for _, instance := range instances {
		if instance.AppID == "" || r.nodeID != "" && instance.NodeID != r.nodeID {
			continue
		}
		if instance.State != "" && instance.State != string(state.StateRunning) && instance.State != string(state.StateDraining) {
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
		if previous, ok := byIP[key]; ok {
			if previous.appID != instance.AppID {
				delete(byIP, key)
				ambiguous[key] = struct{}{}
				continue
			}
			if previous.deploymentID != instance.DeploymentID {
				previous.deploymentID = "" // app identity remains usable; graph identity does not
				byIP[key] = previous
			}
			continue
		}
		byIP[key] = serviceProxyInstanceIdentity{appID: instance.AppID, deploymentID: instance.DeploymentID}
	}
	r.byIP = byIP
	r.expires = now.Add(r.ttl)
	return r.byIP[ip.String()], nil
}

func identityForHostIP(instances []state.Instance, nodeID, hostIP string) serviceProxyInstanceIdentity {
	var identity serviceProxyInstanceIdentity
	for _, instance := range instances {
		if instance.AppID == "" || nodeID != "" && instance.NodeID != nodeID ||
			(instance.State != "" && instance.State != string(state.StateRunning) && instance.State != string(state.StateDraining)) ||
			strings.TrimSpace(instance.HostIP) != hostIP {
			continue
		}
		if identity.appID == "" {
			identity = serviceProxyInstanceIdentity{appID: instance.AppID, deploymentID: instance.DeploymentID}
			continue
		}
		if identity.appID != instance.AppID {
			return serviceProxyInstanceIdentity{}
		}
		if identity.deploymentID != instance.DeploymentID {
			identity.deploymentID = ""
		}
	}
	return identity
}
