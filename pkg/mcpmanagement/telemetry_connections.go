package mcpmanagement

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// connectionStats is a remote bridge's authorization state, read at scrape
// time. Like the rest of Telemetry it carries no account or grant labels.
type connectionStats struct {
	entries        map[string]int
	limit          int
	connections    int
	servers        int
	stateAvailable bool
}

var (
	oauthEntriesDesc = prometheus.NewDesc("gregale_management_mcp_oauth_entries", "Authorization entries held, by fixed kind (clients, requests, codes, grants).", []string{"kind"}, nil)
	oauthLimitDesc   = prometheus.NewDesc("gregale_management_mcp_oauth_entry_limit", "Bound on each kind of authorization entry; at the bound new registrations and sign-ins are refused.", nil, nil)
	connectionsDesc  = prometheus.NewDesc("gregale_management_mcp_connections", "Approved, unexpired agent connections.", nil, nil)
	serversDesc      = prometheus.NewDesc("gregale_management_mcp_connection_servers", "Connections whose MCP server is built; a server idle for ten minutes is dropped and rebuilt on use.", nil, nil)
	stateDesc        = prometheus.NewDesc("gregale_management_mcp_state_available", "1 while the bridge can read and commit its authorization and plan state.", nil, nil)
)

// connectionCollector exports nothing until a remote bridge attaches.
type connectionCollector struct{ t *Telemetry }

func (c connectionCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{oauthEntriesDesc, oauthLimitDesc, connectionsDesc, serversDesc, stateDesc} {
		ch <- d
	}
}

func (c connectionCollector) Collect(ch chan<- prometheus.Metric) {
	read := c.t.connections.Load()
	if read == nil {
		return
	}
	s := (*read)()
	for _, kind := range []string{"clients", "requests", "codes", "grants"} {
		ch <- prometheus.MustNewConstMetric(oauthEntriesDesc, prometheus.GaugeValue, float64(s.entries[kind]), kind)
	}
	available := 0.0
	if s.stateAvailable {
		available = 1
	}
	ch <- prometheus.MustNewConstMetric(oauthLimitDesc, prometheus.GaugeValue, float64(s.limit))
	ch <- prometheus.MustNewConstMetric(connectionsDesc, prometheus.GaugeValue, float64(s.connections))
	ch <- prometheus.MustNewConstMetric(serversDesc, prometheus.GaugeValue, float64(s.servers))
	ch <- prometheus.MustNewConstMetric(stateDesc, prometheus.GaugeValue, available)
}

// attachConnections makes t export r's authorization state.
func (t *Telemetry) attachConnections(r *remoteServer) {
	if t == nil {
		return
	}
	read := r.connectionStats
	t.connections.Store(&read)
}

func (r *remoteServer) connectionStats() connectionStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	s := connectionStats{
		entries: map[string]int{"clients": len(r.clients), "requests": len(r.requests), "codes": len(r.codes), "grants": len(r.grants)},
		limit:   r.capacity(), stateAvailable: r.opts.StateStore.available(),
	}
	for _, g := range r.grants {
		if g.approved && !g.revoked && now.Before(g.expires) {
			s.connections++
		}
		if g.server != nil {
			s.servers++
		}
	}
	return s
}
