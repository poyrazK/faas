package main

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// staticEgressInventoryRefresh bounds how long an operator's inventory change
// waits when its notification is missed.
const staticEgressInventoryRefresh = 5 * time.Second

// staticEgressInventoryKeyPrefix keys inventory grants in the manager's
// app-keyed alias set. Inventory grants are per account, not per app; the
// manager matches grants by (account, IP) and never reads the key otherwise.
const staticEgressInventoryKeyPrefix = "inventory:"

// staticEgressGrantSet is the node's static egress grants: the legacy
// operator TOML bundle plus this node's rows of the operator inventory
// (ADR-20261128090000). Each source replaces only its own part; the manager
// always receives the union, so neither source can strip the other's aliases.
type staticEgressGrantSet struct {
	mu        sync.Mutex
	target    staticEgressIPTarget
	bundle    []StaticEgressIPEntry
	inventory []StaticEgressIPEntry
}

func newStaticEgressGrantSet(target staticEgressIPTarget) *staticEgressGrantSet {
	return &staticEgressGrantSet{target: target}
}

// SetStaticEgressIPAliases replaces the TOML bundle part.
func (g *staticEgressGrantSet) SetStaticEgressIPAliases(entries []StaticEgressIPEntry) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.bundle = slices.Clone(entries)
	g.pushLocked()
}

// setInventory replaces the inventory part.
func (g *staticEgressGrantSet) setInventory(grants []state.StaticEgressIPGrant) {
	entries := make([]StaticEgressIPEntry, 0, len(grants))
	for _, grant := range grants {
		entries = append(entries, StaticEgressIPEntry{AccountID: grant.AccountID, AppID: staticEgressInventoryKeyPrefix + grant.IP.String(), IP: grant.IP})
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.inventory = entries
	g.pushLocked()
}

func (g *staticEgressGrantSet) pushLocked() {
	union := make([]StaticEgressIPEntry, 0, len(g.bundle)+len(g.inventory))
	union = append(union, g.bundle...)
	union = append(union, g.inventory...)
	g.target.SetStaticEgressIPAliases(union)
}

// ReconcileStaticEgressPolicies lets the bundle publication repair the
// manager's policies through the set, as it would directly.
func (g *staticEgressGrantSet) ReconcileStaticEgressPolicies(ctx context.Context) error {
	if repair, ok := g.target.(interface{ ReconcileStaticEgressPolicies(context.Context) error }); ok {
		return repair.ReconcileStaticEgressPolicies(ctx)
	}
	return nil
}

// staticEgressInventorySource reads this node's assigned inventory rows.
type staticEgressInventorySource interface {
	AssignedStaticEgressAddressesForNode(ctx context.Context, nodeID string) ([]state.StaticEgressIPGrant, error)
}

// watchStaticEgressInventory keeps the set's inventory part current: on start,
// on every wake (an operator change), and every staticEgressInventoryRefresh.
// A failed read keeps the last good grants, so a database outage never strips
// a live alias. After a change it repairs the manager's policies so a newly
// granted pin is enforced, and a revoked one blocked, without a wake.
func watchStaticEgressInventory(ctx context.Context, set *staticEgressGrantSet, source staticEgressInventorySource, nodeID string, wake <-chan struct{}, log *slog.Logger) {
	if source == nil || nodeID == "" {
		return
	}
	ticker := time.NewTicker(staticEgressInventoryRefresh)
	defer ticker.Stop()
	var last []state.StaticEgressIPGrant
	loaded := false
	for {
		readCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		grants, err := source.AssignedStaticEgressAddressesForNode(readCtx, nodeID)
		cancel()
		switch {
		case err != nil:
			if ctx.Err() == nil {
				log.Warn("vmmd: read static egress inventory; keeping the last grants", "node_id", nodeID, "err", err)
			}
		case !loaded || !sameStaticEgressGrants(last, grants):
			set.setInventory(grants)
			last, loaded = grants, true
			repairCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			if err := set.ReconcileStaticEgressPolicies(repairCtx); err != nil {
				log.Warn("vmmd: repair static egress after inventory change", "err", err)
			}
			cancel()
			log.Info("vmmd: static egress inventory applied", "node_id", nodeID, "addresses", len(grants))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-wake:
		}
	}
}

func sameStaticEgressGrants(a, b []state.StaticEgressIPGrant) bool {
	return slices.EqualFunc(a, b, func(x, y state.StaticEgressIPGrant) bool { return x.AccountID == y.AccountID && x.IP == y.IP })
}

var (
	_ staticEgressIPTarget = (*staticEgressGrantSet)(nil)
	_ staticEgressIPTarget = (*fcvm.Manager)(nil)
)
