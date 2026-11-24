package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
)

// Internal egress destinations (ADR-20261114100000 amendment 2026-11-23).
// The operator lists public destinations that are still Gregale's own; vmmd
// seeds them into every namespace it prepares so the guest's traffic to them
// is counted as internal (faas_internal_tx) and left out of billed egress.
const (
	internalEgressResolveEvery   = 5 * time.Minute
	internalEgressResolveTimeout = 5 * time.Second
	// internalEgressStartBudget bounds the first, synchronous resolution, so
	// a slow resolver delays vmmd's start by at most this much.
	internalEgressStartBudget = 2 * time.Second
	// internalEgressAddressTTL keeps an address internal for a day after DNS
	// last returned it, so a regional endpoint rotating its addresses does
	// not turn a long-lived database session into billed egress.
	internalEgressAddressTTL = 24 * time.Hour
)

func splitCommaList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// parseInternalEgressCIDRs accepts IPv4 prefixes and bare IPv4 addresses.
func parseInternalEgressCIDRs(values []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(values))
	for _, v := range values {
		if p, err := netip.ParsePrefix(v); err == nil && p.Addr().Is4() {
			out = append(out, p.Masked())
			continue
		}
		if a, err := netip.ParseAddr(v); err == nil && a.Is4() {
			out = append(out, netip.PrefixFrom(a, 32))
			continue
		}
		return nil, fmt.Errorf("vmmd: internal egress destination %q is not an IPv4 address or prefix", v)
	}
	return out, nil
}

type internalEgressResolver struct {
	static   []netip.Prefix
	hosts    []string
	lookup   func(ctx context.Context, host string) ([]netip.Addr, error)
	now      func() time.Time
	apply    func([]netip.Prefix)
	log      *slog.Logger
	mu       sync.Mutex
	lastSeen map[netip.Addr]time.Time
}

func newInternalEgressResolver(cidrs []netip.Prefix, hosts []string, log *slog.Logger) *internalEgressResolver {
	resolver := net.DefaultResolver
	return &internalEgressResolver{
		static: cidrs,
		hosts:  hosts,
		lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return resolver.LookupNetIP(ctx, "ip4", host)
		},
		now:      time.Now,
		apply:    netns.SetInternalEgressExtraDestinations,
		log:      log,
		lastSeen: make(map[netip.Addr]time.Time),
	}
}

// refresh resolves every host once and applies the static prefixes plus
// every address seen within the TTL. A failed lookup keeps what it saw
// before, so a DNS outage never shrinks the internal set.
func (r *internalEgressResolver) refresh(ctx context.Context) {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, host := range r.hosts {
		lctx, cancel := context.WithTimeout(ctx, internalEgressResolveTimeout)
		addrs, err := r.lookup(lctx, host)
		cancel()
		if err != nil {
			if r.log != nil {
				r.log.Warn("vmmd: resolve internal egress host", "host", host, "err", err)
			}
			continue
		}
		for _, a := range addrs {
			if a = a.Unmap(); a.Is4() {
				r.lastSeen[a] = now
			}
		}
	}
	prefixes := append([]netip.Prefix(nil), r.static...)
	for a, seen := range r.lastSeen {
		if now.Sub(seen) > internalEgressAddressTTL {
			delete(r.lastSeen, a)
			continue
		}
		prefixes = append(prefixes, netip.PrefixFrom(a, 32))
	}
	sort.Slice(prefixes, func(i, j int) bool { return prefixes[i].String() < prefixes[j].String() })
	r.apply(prefixes)
}

func (r *internalEgressResolver) run(ctx context.Context) {
	if len(r.hosts) == 0 {
		return
	}
	t := time.NewTicker(internalEgressResolveEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.refresh(ctx)
		}
	}
}

// seedInternalEgressDestinations applies the static prefixes and a first
// resolution of the hosts before any namespace is prepared, bounded by
// internalEgressStartBudget, then keeps resolving in the background. A host
// the first resolution missed is added by the next refresh; a namespace keeps
// the set it was prepared with until its next wake, so a long-running
// instance does not see rotated addresses until then.
func seedInternalEgressDestinations(ctx context.Context, cfg ComputeNodeConfig, log *slog.Logger) error {
	cidrs, err := parseInternalEgressCIDRs(cfg.InternalEgressCIDRs)
	if err != nil {
		return err
	}
	r := newInternalEgressResolver(cidrs, cfg.InternalEgressHosts, log)
	startCtx, cancel := context.WithTimeout(ctx, internalEgressStartBudget)
	r.refresh(startCtx)
	cancel()
	go r.run(ctx)
	if log != nil && (len(cidrs) > 0 || len(cfg.InternalEgressHosts) > 0) {
		log.Info("vmmd: internal egress destinations seeded (ADR-20261114100000)", "cidrs", len(cidrs), "hosts", cfg.InternalEgressHosts)
	}
	return nil
}
