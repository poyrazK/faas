package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) OperatorIncidentEvidence(_ context.Context, targetID string, since time.Time, limit int) ([]api.ObsIncidentEvidenceEntry, error) {
	limit = operatorIncidentEvidenceLimit(limit)
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []api.ObsIncidentEvidenceEntry{}
	for _, row := range m.events {
		if row.At.Before(since) || !operatorEventTargets(row, targetID) {
			continue
		}
		entry := api.ObsIncidentEvidenceEntry{ID: fmt.Sprint(row.ID), Source: "event", Kind: row.Kind, At: row.At.UTC()}
		if row.TraceID != nil {
			entry.TraceID = *row.TraceID
		}
		out = append(out, entry)
	}
	for _, row := range m.deploymentAudit {
		if row.DeploymentID.String() == targetID && !row.At.Before(since) {
			out = append(out, api.ObsIncidentEvidenceEntry{ID: fmt.Sprint(row.ID), Source: "deployment_audit", Kind: string(row.Kind), At: row.At.UTC()})
		}
	}
	for _, row := range m.operatorIntents {
		if row.TargetID != targetID || row.RequestedAt.Before(since) {
			continue
		}
		entry := api.ObsIncidentEvidenceEntry{ID: row.ID, Source: "operator_intent", Kind: string(row.Kind), Status: string(row.Status), At: row.RequestedAt.UTC()}
		if row.TraceID != nil {
			entry.TraceID = *row.TraceID
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At)
		}
		if out[i].Source != out[j].Source {
			return out[i].Source > out[j].Source
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func operatorEventTargets(row Event, targetID string) bool {
	if row.Subject != nil && row.Subject.String() == targetID {
		return true
	}
	if !strings.HasPrefix(row.Kind, "operator.action.") {
		return false
	}
	var refs map[string]json.RawMessage
	if json.Unmarshal(row.Data, &refs) != nil {
		return false
	}
	for _, key := range []string{"target_id", "run_id", "deployment_id"} {
		value := refs[key]
		if len(value) == 0 || string(value) == "null" {
			continue
		}
		var reference string
		if json.Unmarshal(value, &reference) != nil {
			return false
		}
		if reference != "" {
			return reference == targetID
		}
	}
	return false
}

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
