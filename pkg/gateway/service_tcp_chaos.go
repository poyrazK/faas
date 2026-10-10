package gateway

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
)

// ServiceTCPChaosResolver revalidates live run membership and reads the lease.
// A removed run, revoked caller or unavailable policy must return an error.
type ServiceTCPChaosResolver func(context.Context, string, string) (chaos.Lease, error)

const serviceTCPChaosRefresh = 250 * time.Millisecond

type serviceTCPChaosKey struct {
	caller, target string
}

type serviceTCPChaosGroup struct {
	ctx    context.Context
	cancel context.CancelFunc
	conns  map[*chaos.TCPConn]struct{}
}

// Policy polling is shared by all pooled connections on one workload route.
// No database lookups happen on production sessions or for each payload chunk.
type serviceTCPChaosController struct {
	mu      sync.Mutex
	groups  map[serviceTCPChaosKey]*serviceTCPChaosGroup
	resolve ServiceTCPChaosResolver
	observe ServiceProxyChaosObserver
	ordinal atomic.Uint64
}

func (p *ServiceTCPProxy) chaosConn(ctx context.Context, conn net.Conn, caller string, target ServiceTCPTarget, port int) (net.Conn, func(), error) {
	if target.ScenarioTestRunID == "" {
		return conn, func() {}, nil
	}
	if p.chaos.resolve == nil {
		return nil, nil, fmt.Errorf("scenario TCP chaos resolver is unavailable")
	}
	key := serviceTCPChaosKey{caller: caller, target: target.AppID}
	lease, err := p.chaos.lookup(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	ordinal := p.chaos.ordinal.Add(1)
	var observedMu sync.Mutex
	observed := make(map[string]struct{})
	onMatch := func(rule chaos.Rule, direction, generation string) {
		if p.chaos.observe == nil {
			return
		}
		ruleID := generation + "\x00" + chaos.RuleID(rule)
		observedMu.Lock()
		_, alreadyObserved := observed[ruleID]
		observed[ruleID] = struct{}{}
		observedMu.Unlock()
		if !alreadyObserved {
			p.chaos.observe(target.ScenarioTestRunID, caller, generation, rule)
		}
	}
	impaired := chaos.NewTCPConnWithMatchObserver(ctx, conn, port, ordinal, func(rule chaos.Rule, direction string) {
		p.cfg.Metrics.ObserveChaosInjection(rule.Kind, direction)
		p.cfg.Log.Info("scenario TCP fault injected", "run_id", target.ScenarioTestRunID,
			"target", target.ScenarioWorkload, "caller_app_id", caller,
			"connection_ordinal", ordinal, "port", port, "kind", rule.Kind,
			"direction", direction, "seed", rule.Seed)
	}, onMatch)
	if err := impaired.Update(lease); err != nil {
		_ = impaired.Close()
		return nil, nil, err
	}
	c := &p.chaos
	c.mu.Lock()
	group := c.groups[key]
	start := group == nil
	if start {
		if len(c.groups) >= api.ScenarioTCPChaosMaxRoutesPerNode {
			c.mu.Unlock()
			_ = impaired.Close()
			return nil, nil, fmt.Errorf("scenario TCP route limit reached")
		}
		// The route watcher belongs to all its sockets, not the first socket's
		// lifetime. The final cleanup below owns its cancellation.
		groupCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		group = &serviceTCPChaosGroup{ctx: groupCtx, cancel: cancel, conns: make(map[*chaos.TCPConn]struct{})}
		c.groups[key] = group
	}
	group.conns[impaired] = struct{}{}
	c.mu.Unlock()
	if start {
		go c.watch(key, group)
	}
	return impaired, func() {
		_ = impaired.Close()
		c.mu.Lock()
		delete(group.conns, impaired)
		if len(group.conns) == 0 {
			group.cancel()
			if c.groups[key] == group {
				delete(c.groups, key)
			}
		}
		c.mu.Unlock()
	}, nil
}

func (c *serviceTCPChaosController) lookup(ctx context.Context, key serviceTCPChaosKey) (chaos.Lease, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return c.resolve(lookupCtx, key.caller, key.target)
}

func (c *serviceTCPChaosController) watch(key serviceTCPChaosKey, group *serviceTCPChaosGroup) {
	ticker := time.NewTicker(serviceTCPChaosRefresh)
	defer ticker.Stop()
	for {
		select {
		case <-group.ctx.Done():
			return
		case <-ticker.C:
		}
		lease, err := c.lookup(group.ctx, key)
		c.mu.Lock()
		conns := make([]*chaos.TCPConn, 0, len(group.conns))
		for conn := range group.conns {
			conns = append(conns, conn)
		}
		if err != nil && c.groups[key] == group {
			delete(c.groups, key)
			group.cancel()
		}
		c.mu.Unlock()
		for _, conn := range conns {
			if err != nil {
				_ = conn.Close()
			} else if updateErr := conn.Update(lease); updateErr != nil {
				_ = conn.Close()
			}
		}
		if err != nil {
			return
		}
	}
}
