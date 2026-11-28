package sched

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"

	"github.com/onebox-faas/faas/pkg/state"
)

// ErrStaticEgressNodeOnly refuses to move a pinned instance to a node the
// provider does not route its static egress address to.
var ErrStaticEgressNodeOnly = errors.New("sched: a pinned static egress address is routed to another node")

// staticEgressNodes returns the nodes holding the static egress address a
// wake spec pins (ADR-20261128090000). pinned is false when nothing is pinned,
// the store keeps no inventory, or only a legacy per-node TOML grant covers
// the address: those keep their unconstrained placement.
func staticEgressNodes(ctx context.Context, store state.Store, accountID, ip string) (bool, []string, error) {
	if ip == "" {
		return false, nil, nil
	}
	inventory, ok := store.(state.StaticEgressAddressStore)
	if !ok {
		return false, nil, nil
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false, nil, fmt.Errorf("sched: static egress address %q: %w", ip, err)
	}
	nodes, err := inventory.StaticEgressAddressNodeIDs(ctx, accountID, addr)
	if err != nil {
		return false, nil, fmt.Errorf("sched: static egress address node: %w", err)
	}
	return len(nodes) > 0, nodes, nil
}

// withStaticEgressPlacement gates r to the nodes holding the pinned address.
func (e *Engine) withStaticEgressPlacement(ctx context.Context, r Request, accountID, ip string) (Request, error) {
	pinned, nodes, err := staticEgressNodes(ctx, e.store, accountID, ip)
	if err != nil {
		return r, err
	}
	r.StaticEgressPinned, r.StaticEgressNodeIDs = pinned, nodes
	return r, nil
}

// staticEgressAllowsDestination reports whether a pinned instance may move to
// node; an unpinned one always may.
func staticEgressAllowsDestination(ctx context.Context, store state.Store, accountID, ip, node string) (bool, error) {
	pinned, nodes, err := staticEgressNodes(ctx, store, accountID, ip)
	if err != nil || !pinned {
		return err == nil, err
	}
	return slices.Contains(nodes, node), nil
}
