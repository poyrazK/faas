package main

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"

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

// directServing is edgedns.TLSProbe.
type directServing interface {
	Serves(ctx context.Context, addr netip.Addr, host string) bool
}

// nodeSource builds one tick's view of the compute nodes: the registry says
// which are drained, the manifest gives each its public address, a probe of
// each edge's /readyz says which are ready, and a handshake with each public
// address for every DNS-only name says which serve those names
// (ADR-20261102081618 amendment 2026-11-05).
type nodeSource struct {
	registry computeNodes
	probe    readiness
	public   map[string]netip.Addr
	edgePort string
	// direct and directHosts are set when a managed name is DNS-only.
	direct      directServing
	directHosts []string
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
		if s.direct != nil && len(s.directHosts) > 0 && nodes[i].Public.IsValid() {
			wg.Add(1)
			go func() {
				defer wg.Done()
				nodes[i].Direct = s.servesAll(ctx, nodes[i].Public)
			}()
		}
	}
	wg.Wait()
	return nodes, nil
}

// servesAll reports whether addr completes a handshake for every DNS-only
// name, probing them at once so a tick stays within one probe timeout.
func (s nodeSource) servesAll(ctx context.Context, addr netip.Addr) bool {
	var failed atomic.Bool
	var wg sync.WaitGroup
	for _, host := range s.directHosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !s.direct.Serves(ctx, addr, host) {
				failed.Store(true)
			}
		}()
	}
	wg.Wait()
	return !failed.Load()
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
