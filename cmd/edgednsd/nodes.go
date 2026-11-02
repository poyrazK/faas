package main

import (
	"context"
	"net"
	"net/netip"
	"sync"

	"github.com/onebox-faas/faas/pkg/edgedns"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// computeNodes is the registry read edgednsd needs.
type computeNodes interface {
	ListComputeNodes(ctx context.Context, includeInactive bool) ([]state.ComputeNode, error)
}

type readiness interface {
	Ready(ctx context.Context, url string) bool
}

// nodeSource builds one tick's view of the compute nodes: the registry says
// which are drained, the manifest gives each its public address, and a probe
// of each edge's /readyz says which are ready.
type nodeSource struct {
	registry computeNodes
	probe    readiness
	public   map[string]netip.Addr
	edgePort string
}

func (s nodeSource) nodes(ctx context.Context) ([]edgedns.Node, error) {
	list, err := s.registry.ListComputeNodes(ctx, true)
	if err != nil {
		return nil, err
	}
	nodes := make([]edgedns.Node, len(list))
	var wg sync.WaitGroup
	for i, n := range list {
		nodes[i] = edgedns.Node{Name: n.Name, Public: s.public[n.Name], Active: n.Active}
		host := privateHost(n.TargetURL)
		if host == "" {
			continue // no private address to probe: the node stays unready
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			nodes[i].Ready = s.probe.Ready(ctx, "http://"+net.JoinHostPort(host, s.edgePort)+"/readyz")
		}()
	}
	wg.Wait()
	return nodes, nil
}

// privateHost returns the host of a node's vmmd target, the node's private
// address; "" for a unix target, which has none.
func privateHost(target string) string {
	t, err := wire.ParseTarget(target)
	if err != nil || t.Scheme == wire.SchemeUnix {
		return ""
	}
	host, _, err := net.SplitHostPort(t.Address)
	if err != nil {
		return ""
	}
	return host
}
