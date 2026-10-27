package realtime

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Realtime fair use (ADR-1323). Realtime is free; an account's concurrent
// connections are capped by its plan across the fleet, and one account may
// hold at most api.RealtimeAccountNodeSharePercent of a node. Each node counts
// its own connections per account and learns the other nodes' counts from
// apid every ConnectionSyncInterval. Without a fresh sync a node enforces its
// local count only, so an apid outage never refuses connections that would
// otherwise fit on the node.

// ConnectionSyncer reports this node's per-account connection counts and
// returns the other nodes' fresh counts.
type ConnectionSyncer interface {
	SyncRealtimeConnections(context.Context, []state.RealtimeAccountConnections) ([]state.RealtimeAccountConnections, error)
}

// accountConnections is one account's connections on this node and, from the
// last sync, on the other nodes.
type accountConnections struct {
	local  atomic.Int64
	remote atomic.Int64
	// resume counts the account's resume, inbox and SSE subscriptions on
	// this node (reserveResume).
	resume atomic.Int64
	// outbound is the bytes queued for, or being written to, the account's
	// connections on this node (reserveNodeOutbound).
	outbound atomic.Int64
}

// connectionRefusal explains a refused reservation. A zero value means the
// node or endpoint limit applied, which predates fair use and keeps its plain
// response.
type connectionRefusal struct {
	scope           string
	limit, observed int64
}

func (r connectionRefusal) problem() *api.Problem {
	return api.ErrRealtimeConnectionLimit(r.scope, r.limit, r.observed)
}

func (m *Manager) accountFor(id string) *accountConnections {
	value, _ := m.accounts.LoadOrStore(id, new(accountConnections))
	return value.(*accountConnections)
}

// remoteConnections is the other nodes' count for the account, or zero when
// the last sync is too old to trust.
func (m *Manager) remoteConnections(account *accountConnections) int64 {
	synced := m.connectionSyncedAt.Load()
	if synced == 0 || time.Since(time.Unix(0, synced)) > api.RealtimeConnectionSyncStaleAfter {
		return 0
	}
	return account.remote.Load()
}

// reserveAccount admits one more connection for the endpoint's account, or
// explains why not. Accounts are always counted so syncs report them. Every
// account is held to its share of this node's connections, with or without
// fair use: otherwise one endpoint (an auth_mode=none endpoint any client can
// open) took every slot and the node answered 429 to all other tenants. Only
// an endpoint registered with an account limit enforces that limit across
// nodes, and only with Config.FairUse.
func (m *Manager) reserveAccount(state *endpointState) (connectionRefusal, bool) {
	account := state.account
	limit := int64(state.config.Load().AccountMaxConnections)
	if !m.cfg.FairUse {
		limit = 0
	}
	share := int64(m.cfg.MaxConnections) * api.RealtimeAccountNodeSharePercent / 100
	for {
		local := account.local.Load()
		if share > 0 && local >= share {
			return connectionRefusal{scope: api.RealtimeConnectionLimitAccountNode, limit: share, observed: local}, false
		}
		if limit > 0 {
			if observed := local + m.remoteConnections(account); observed >= limit {
				return connectionRefusal{scope: api.RealtimeConnectionLimitAccount, limit: limit, observed: observed}, false
			}
		}
		if account.local.CompareAndSwap(local, local+1) {
			return connectionRefusal{}, true
		}
	}
}

func (m *Manager) startConnectionSync() {
	if m.cfg.ConnectionSync == nil {
		return
	}
	go m.runConnectionSync(m.ctx) //nolint:contextcheck // the Manager owns the sync loop's lifecycle; Close cancels it.
}

func (m *Manager) runConnectionSync(ctx context.Context) {
	ticker := time.NewTicker(m.cfg.ConnectionSyncInterval)
	defer ticker.Stop()
	for {
		m.syncConnections(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Manager) syncConnections(ctx context.Context) {
	counts := []state.RealtimeAccountConnections{}
	m.accounts.Range(func(key, value any) bool {
		if n := value.(*accountConnections).local.Load(); n > 0 && len(counts) < api.RealtimeConnectionSyncMaxAccounts {
			counts = append(counts, state.RealtimeAccountConnections{AccountID: key.(string), Connections: n})
		}
		return true
	})
	syncCtx, cancel := context.WithTimeout(ctx, m.cfg.ConnectionSyncInterval)
	remote, err := m.cfg.ConnectionSync.SyncRealtimeConnections(syncCtx, counts)
	cancel()
	if err != nil {
		m.connectionSyncFailures.Add(1)
		if m.cfg.ConnectionSyncFailure != nil {
			m.cfg.ConnectionSyncFailure(err)
		}
		return
	}
	others := make(map[string]int64, len(remote))
	for _, r := range remote {
		others[r.AccountID] = r.Connections
		m.accountFor(r.AccountID)
	}
	m.accounts.Range(func(key, value any) bool {
		value.(*accountConnections).remote.Store(others[key.(string)])
		return true
	})
	m.connectionSyncedAt.Store(time.Now().UnixNano())
	m.connectionSyncs.Add(1)
}
