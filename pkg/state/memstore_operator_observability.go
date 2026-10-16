package state

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) OperatorAuditTraceCoverage(_ context.Context, since time.Time) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	total, covered := 0, 0
	for _, row := range m.auditLog {
		if !row.ReceivedAt.Before(since) {
			total++
			if auditLogHasTrace(row) {
				covered++
			}
		}
	}
	if total == 0 {
		return 1, nil
	}
	return float64(covered) / float64(total), nil
}

func (m *MemStore) OperatorOverviewActivity(_ context.Context, auditSince, failureSince time.Time) (OperatorOverviewActivity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := OperatorOverviewActivity{OrgsTotal: len(m.orgs), Failures: []api.ObsOverviewFailureKind{}}
	for _, row := range m.auditLog {
		if !row.ReceivedAt.Before(auditSince) {
			out.AuditEvents24h++
		}
	}
	counts := map[string]int{}
	for _, row := range m.events {
		if !row.At.Before(failureSince) && (strings.HasSuffix(row.Kind, ".failed") || strings.HasSuffix(row.Kind, ".failure") || strings.HasSuffix(row.Kind, ".error")) {
			counts[row.Kind]++
		}
	}
	for kind, count := range counts {
		out.Failures = append(out.Failures, api.ObsOverviewFailureKind{Kind: kind, Count: count})
	}
	sort.Slice(out.Failures, func(i, j int) bool {
		if out.Failures[i].Count == out.Failures[j].Count {
			return out.Failures[i].Kind < out.Failures[j].Kind
		}
		return out.Failures[i].Count > out.Failures[j].Count
	})
	return out, nil
}

func (m *MemStore) OperatorTenantCounts(_ context.Context, accountID string) (int, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	apps, live := 0, 0
	owned := map[string]bool{}
	for id, app := range m.apps {
		if app.AccountID == accountID && app.Status != AppDeleted {
			apps++
			owned[id] = true
		}
	}
	for _, d := range m.deployments {
		if owned[d.AppID] && d.Status == DeployLive && d.DeletedAt == nil {
			live++
		}
	}
	return apps, live, nil
}

func (m *MemStore) ListOperatorIncidents(_ context.Context, f OperatorIncidentFilter) ([]api.ObsIncident, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []api.ObsIncident{}
	add := func(row api.ObsIncident) {
		if f.Kind != "" && row.Type != f.Kind || f.Severity != "" && row.Severity != f.Severity {
			return
		}
		if f.BeforeID != "" && (row.ObservedAt.After(f.BeforeAt) || row.ObservedAt.Equal(f.BeforeAt) && row.ID >= f.BeforeID) {
			return
		}
		out = append(out, row)
	}
	for _, d := range m.deployments {
		app, ok := m.apps[d.AppID]
		if !ok || app.AccountID == "" || app.Status == AppDeleted || d.DeletedAt != nil {
			continue
		}
		switch d.Status {
		case DeployPending, DeployBuilding, DeployImaging, DeploySnapshotting, DeployFailed:
		default:
			continue
		}
		if d.Status == DeployFailed && d.CreatedAt.Before(f.Since) {
			continue
		}
		severity, summary := "warning", "deployment "+string(d.Status)
		if d.Status == DeployFailed {
			severity = "error"
			if d.ErrorCode != "" {
				summary += " (" + d.ErrorCode + ")"
			}
		}
		add(api.ObsIncident{ID: "deployment/" + d.ID, Type: "deployment", Severity: severity, Status: string(d.Status), Summary: summary, AccountID: app.AccountID, AppID: app.ID, ResourceID: d.ID, ResourceName: app.Slug, ObservedAt: d.CreatedAt.UTC()})
	}
	for _, r := range m.jobRuns {
		job, ok := m.jobs[r.JobID]
		if !ok || job.AccountID != r.AccountID {
			continue
		}
		active := r.AggregateStatus == "queued" || r.AggregateStatus == "running"
		if !active && r.AggregateStatus != "failed" && r.AggregateStatus != "dead_letter" {
			continue
		}
		at := r.CreatedAt
		if r.FinishedAt != nil {
			at = *r.FinishedAt
		}
		if !active && at.Before(f.Since) {
			continue
		}
		severity, summary := "warning", "job run "+r.AggregateStatus
		if !active {
			severity = "error"
		} else if !r.CreatedAt.After(f.StuckBefore) {
			severity = "error"
			summary = "job run appears stuck"
		}
		add(api.ObsIncident{ID: "job_run/" + r.ID, Type: "job_run", Severity: severity, Status: r.AggregateStatus, Summary: summary, AccountID: r.AccountID, ResourceID: r.ID, ResourceName: job.Name, ObservedAt: at.UTC()})
	}
	for _, n := range m.computeNodes {
		lifecycle := string(n.Lifecycle)
		if lifecycle == "" {
			if n.Active {
				lifecycle = "active"
			} else {
				lifecycle = "unavailable"
			}
		}
		stale := n.LastHeartbeatAt.IsZero() || n.LastHeartbeatAt.Before(f.StaleBefore)
		if n.Active && lifecycle == "active" && !stale {
			continue
		}
		severity, status, summary := "warning", lifecycle, "compute node "+lifecycle
		if !n.Active {
			severity = "error"
		}
		if stale {
			status = "stale"
			summary = "compute node heartbeat stale"
		}
		at := n.LastHeartbeatAt
		if n.DrainInitiatedAt != nil {
			at = *n.DrainInitiatedAt
		}
		if at.IsZero() {
			at = n.CreatedAt
		}
		add(api.ObsIncident{ID: "compute_node/" + n.ID, Type: "compute_node", Severity: severity, Status: status, Summary: summary, ResourceID: n.ID, ResourceName: n.Name, ObservedAt: at.UTC()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ObservedAt.Equal(out[j].ObservedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].ObservedAt.After(out[j].ObservedAt)
	})
	if f.Limit <= 0 {
		return []api.ObsIncident{}, nil
	}
	if len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}
