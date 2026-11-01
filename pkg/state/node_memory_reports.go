package state

// adr: 20261101091815 — node-wide measured memory, shared between schedulers.

import (
	"context"
	"fmt"
	"time"
)

// NodeMemoryReport is one compute node's memory as its own scheduler measured
// it (ADR-909): MeasuredMB is the measured admission charge of every instance
// on the node, UsedMB vmmd's summed cgroup memory.current (nil without a fresh
// vmmd report), FrozenInstances the node's frozen VMs, and MeasuredAt when the
// measurement was taken.
type NodeMemoryReport struct {
	NodeID          string
	MeasuredMB      int
	UsedMB          *int
	FrozenInstances int
	MeasuredAt      time.Time
}

// NodeMemoryReportStore shares node memory between schedulers (ADR
// 20261101091815). Only the scheduler that receives a node's vmmd stream
// writes its row; every scheduler reads them.
type NodeMemoryReportStore interface {
	// UpsertNodeMemoryReport records a node's report unless a newer one is
	// already stored.
	UpsertNodeMemoryReport(ctx context.Context, r NodeMemoryReport) error
	// NodeMemoryReports lists the reports measured after since.
	NodeMemoryReports(ctx context.Context, since time.Time) ([]NodeMemoryReport, error)
}

// UpsertNodeMemoryReport implements NodeMemoryReportStore.
func (s *PgStore) UpsertNodeMemoryReport(ctx context.Context, r NodeMemoryReport) error {
	if _, err := s.pool.Exec(ctx, `
		insert into node_memory_reports (node_id, measured_mb, used_mb, frozen_instances, measured_at, reported_at)
		values ($1, $2, $3, $4, $5, now())
		on conflict (node_id) do update
		   set measured_mb = excluded.measured_mb, used_mb = excluded.used_mb,
		       frozen_instances = excluded.frozen_instances, measured_at = excluded.measured_at,
		       reported_at = now()
		 where node_memory_reports.measured_at <= excluded.measured_at
	`, r.NodeID, r.MeasuredMB, r.UsedMB, r.FrozenInstances, r.MeasuredAt); err != nil {
		return fmt.Errorf("state: upsert node %s memory report: %w", r.NodeID, err)
	}
	return nil
}

// NodeMemoryReports implements NodeMemoryReportStore.
func (s *PgStore) NodeMemoryReports(ctx context.Context, since time.Time) ([]NodeMemoryReport, error) {
	rows, err := s.pool.Query(ctx, `
		select node_id::text, measured_mb, used_mb, frozen_instances, measured_at
		  from node_memory_reports
		 where measured_at > $1`, since)
	if err != nil {
		return nil, fmt.Errorf("state: list node memory reports: %w", err)
	}
	defer rows.Close()
	var out []NodeMemoryReport
	for rows.Next() {
		var r NodeMemoryReport
		if err := rows.Scan(&r.NodeID, &r.MeasuredMB, &r.UsedMB, &r.FrozenInstances, &r.MeasuredAt); err != nil {
			return nil, fmt.Errorf("state: scan node memory report: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpsertNodeMemoryReport mirrors PgStore.UpsertNodeMemoryReport.
func (m *MemStore) UpsertNodeMemoryReport(_ context.Context, r NodeMemoryReport) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.computeNodes[r.NodeID]; !ok {
		return fmt.Errorf("state: upsert node %s memory report: %w", r.NodeID, ErrNotFound)
	}
	if m.nodeMemoryReports == nil {
		m.nodeMemoryReports = map[string]NodeMemoryReport{}
	}
	if old, ok := m.nodeMemoryReports[r.NodeID]; ok && old.MeasuredAt.After(r.MeasuredAt) {
		return nil
	}
	if r.UsedMB != nil {
		used := *r.UsedMB
		r.UsedMB = &used
	}
	m.nodeMemoryReports[r.NodeID] = r
	return nil
}

// NodeMemoryReports mirrors PgStore.NodeMemoryReports.
func (m *MemStore) NodeMemoryReports(_ context.Context, since time.Time) ([]NodeMemoryReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []NodeMemoryReport
	for _, r := range m.nodeMemoryReports {
		if r.MeasuredAt.After(since) {
			out = append(out, r)
		}
	}
	return out, nil
}
