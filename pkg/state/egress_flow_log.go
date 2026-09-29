package state

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"time"
)

// EgressFlowRecord is one destination address and TCP port a tenant guest
// opened a new flow to (ADR-369).
type EgressFlowRecord struct {
	ObservedAt time.Time
	NodeName   string
	AccountID  string
	AppID      string
	InstanceID string
	RemoteIP   netip.Addr
	RemotePort uint16
}

// EgressFlowFilter selects egress flow log rows. A zero Remote matches any
// address; an address-only lookup is a single-host prefix. From is
// inclusive, To exclusive.
type EgressFlowFilter struct {
	Remote    netip.Prefix
	AccountID string
	From, To  time.Time
	Limit     int
}

// EgressFlowLogStore persists and queries the egress flow log. PgStore and
// MemStore implement it; callers type-assert.
type EgressFlowLogStore interface {
	InsertEgressFlows(ctx context.Context, records []EgressFlowRecord) error
	// ListEgressFlows returns matching rows, newest first.
	ListEgressFlows(ctx context.Context, filter EgressFlowFilter) ([]EgressFlowRecord, error)
	// DeleteEgressFlowsBefore deletes up to limit rows observed before
	// cutoff and returns how many it deleted.
	DeleteEgressFlowsBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error)
}

var (
	_ EgressFlowLogStore = (*PgStore)(nil)
	_ EgressFlowLogStore = (*MemStore)(nil)
)

// InsertEgressFlows implements EgressFlowLogStore with one statement.
func (s *PgStore) InsertEgressFlows(ctx context.Context, records []EgressFlowRecord) error {
	if len(records) == 0 {
		return nil
	}
	n := len(records)
	at, node, acct, app, inst, ip, port := make([]time.Time, n), make([]string, n), make([]string, n),
		make([]string, n), make([]string, n), make([]string, n), make([]int32, n)
	for i, r := range records {
		at[i], node[i], acct[i], app[i], inst[i] = r.ObservedAt.UTC(), r.NodeName, r.AccountID, r.AppID, r.InstanceID
		ip[i], port[i] = r.RemoteIP.String(), int32(r.RemotePort)
	}
	if _, err := s.pool.Exec(ctx, `
		insert into egress_flow_log (observed_at, node_name, account_id, app_id, instance_id, remote_ip, remote_port)
		select * from unnest($1::timestamptz[], $2::text[], $3::text[], $4::text[], $5::text[], $6::inet[], $7::int[])`,
		at, node, acct, app, inst, ip, port); err != nil {
		return fmt.Errorf("state: insert egress flows: %w", mapErr(err))
	}
	return nil
}

// ListEgressFlows implements EgressFlowLogStore.
func (s *PgStore) ListEgressFlows(ctx context.Context, f EgressFlowFilter) ([]EgressFlowRecord, error) {
	var remote *string
	if f.Remote.IsValid() {
		v := f.Remote.Masked().String()
		remote = &v
	}
	rows, err := s.pool.Query(ctx, `
		select observed_at, node_name, account_id, app_id, instance_id, host(remote_ip), remote_port
		  from egress_flow_log
		 where ($1::cidr is null or remote_ip <<= $1::cidr)
		   and ($2 = '' or account_id = $2)
		   and observed_at >= $3 and observed_at < $4
		 order by observed_at desc, id desc
		 limit $5`, remote, f.AccountID, f.From.UTC(), f.To.UTC(), f.Limit)
	if err != nil {
		return nil, fmt.Errorf("state: list egress flows: %w", mapErr(err))
	}
	defer rows.Close()
	var out []EgressFlowRecord
	for rows.Next() {
		var r EgressFlowRecord
		var ip string
		var port int32
		if err := rows.Scan(&r.ObservedAt, &r.NodeName, &r.AccountID, &r.AppID, &r.InstanceID, &ip, &port); err != nil {
			return nil, fmt.Errorf("state: scan egress flow: %w", err)
		}
		r.RemoteIP, _ = netip.ParseAddr(ip)
		r.RemotePort = uint16(port)
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteEgressFlowsBefore implements EgressFlowLogStore.
func (s *PgStore) DeleteEgressFlowsBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		delete from egress_flow_log
		 where id in (select id from egress_flow_log where observed_at < $1 order by observed_at limit $2)`,
		cutoff.UTC(), limit)
	if err != nil {
		return 0, fmt.Errorf("state: delete egress flows: %w", mapErr(err))
	}
	return tag.RowsAffected(), nil
}

// InsertEgressFlows implements EgressFlowLogStore.
func (m *MemStore) InsertEgressFlows(_ context.Context, records []EgressFlowRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range records {
		r.ObservedAt = r.ObservedAt.UTC()
		m.egressFlows = append(m.egressFlows, r)
	}
	return nil
}

// ListEgressFlows implements EgressFlowLogStore.
func (m *MemStore) ListEgressFlows(_ context.Context, f EgressFlowFilter) ([]EgressFlowRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []EgressFlowRecord
	for i := len(m.egressFlows) - 1; i >= 0; i-- {
		r := m.egressFlows[i]
		if f.Remote.IsValid() && !f.Remote.Masked().Contains(r.RemoteIP) {
			continue
		}
		if f.AccountID != "" && r.AccountID != f.AccountID {
			continue
		}
		if r.ObservedAt.Before(f.From) || !r.ObservedAt.Before(f.To) {
			continue
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ObservedAt.After(out[j].ObservedAt) })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// DeleteEgressFlowsBefore implements EgressFlowLogStore.
func (m *MemStore) DeleteEgressFlowsBefore(_ context.Context, cutoff time.Time, limit int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.egressFlows[:0]
	var deleted int64
	for _, r := range m.egressFlows {
		if r.ObservedAt.Before(cutoff) && deleted < int64(limit) {
			deleted++
			continue
		}
		kept = append(kept, r)
	}
	m.egressFlows = kept
	return deleted, nil
}
