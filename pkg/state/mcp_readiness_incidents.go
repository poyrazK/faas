package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type MCPReadinessIncidentStore interface {
	ListMCPReadinessIncidents(context.Context, string, string, string, string, int) (api.MCPReadinessIncidentTimeline, error)
}

func readinessIncidentEvent(d AlertDelivery) api.MCPReadinessIncidentEvent {
	var p struct {
		IncidentID string                          `json:"incident_id"`
		EventID    string                          `json:"event_id"`
		Event      string                          `json:"event"`
		Metric     string                          `json:"metric"`
		Scope      string                          `json:"mcp_readiness_scope"`
		Scopes     []string                        `json:"mcp_readiness_scopes"`
		OccurredAt time.Time                       `json:"occurred_at"`
		Subjects   []api.MCPReadinessMonitorStatus `json:"mcp_readiness"`
	}
	if json.Unmarshal(d.Payload, &p) != nil || p.Metric != api.MetricMCPReadinessUnhealthy || p.IncidentID == "" || p.EventID == "" || (p.Event != "alert.fired" && p.Event != "alert.resolved") {
		return api.MCPReadinessIncidentEvent{}
	}
	for i := range p.Subjects {
		p.Subjects[i].History = nil
	}
	if len(p.Subjects) > 50 {
		p.Subjects = p.Subjects[:50]
	}
	return api.MCPReadinessIncidentEvent{ID: d.ID, IncidentID: p.IncidentID, EventID: p.EventID, Event: p.Event, RuleID: d.RuleID, ReadinessScope: p.Scope, Scopes: p.Scopes, OccurredAt: p.OccurredAt, AttemptedAt: &d.FiredAt, DeliveryStatus: string(d.Status), Subjects: p.Subjects}
}
func validateReadinessIncidentList(scope, before string, limit int) (time.Time, string, error) {
	if limit < 1 || limit > 50 || scope != "" && api.ValidateScope(scope) != nil {
		return time.Time{}, "", ErrInvalidArgument
	}
	at, id, err := api.ParseMCPReadinessIncidentCursor(before)
	if err != nil {
		return time.Time{}, "", ErrInvalidArgument
	}
	return at, id, nil
}
func (m *MemStore) ListMCPReadinessIncidents(_ context.Context, account, app, scope, before string, limit int) (api.MCPReadinessIncidentTimeline, error) {
	at, id, err := validateReadinessIncidentList(scope, before, limit)
	if err != nil {
		return api.MCPReadinessIncidentTimeline{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.readinessAppLocked(account, app) {
		return api.MCPReadinessIncidentTimeline{}, ErrNotFound
	}
	events := []readinessIncidentEventRecord{}
	for _, e := range m.readinessIncidentEvents {
		incident := m.readinessIncidents[e.IncidentID]
		if incident.AccountID != account || incident.AppID != app || !at.IsZero() && (e.At.After(at) || e.At.Equal(at) && e.ID >= id) {
			continue
		}
		match := scope == ""
		for _, s := range readinessIncidentEvent(AlertDelivery{ID: e.ID, Payload: e.Payload}).Scopes {
			if s == scope {
				match = true
			}
		}
		if match {
			events = append(events, e)
		}
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].At.Equal(events[j].At) {
			return events[i].ID > events[j].ID
		}
		return events[i].At.After(events[j].At)
	})
	out := api.MCPReadinessIncidentTimeline{Events: []api.MCPReadinessIncidentEvent{}}
	more := len(events) > limit
	if more {
		events = events[:limit]
	}
	for _, e := range events {
		out.Events = append(out.Events, incidentRecordView(e, m.readinessIncidents[e.IncidentID], time.Now().UTC()))
	}
	if more {
		last := events[len(events)-1]
		out.NextBefore = api.MCPReadinessIncidentCursor(last.At, last.ID)
	}
	return out, nil
}
func (s *PgStore) ListMCPReadinessIncidents(ctx context.Context, account, app, scope, before string, limit int) (api.MCPReadinessIncidentTimeline, error) {
	at, id, err := validateReadinessIncidentList(scope, before, limit)
	if err != nil {
		return api.MCPReadinessIncidentTimeline{}, err
	}
	owned, err := s.AppByID(ctx, app)
	if err != nil {
		return api.MCPReadinessIncidentTimeline{}, err
	}
	if owned.AccountID != account {
		return api.MCPReadinessIncidentTimeline{}, ErrNotFound
	}
	var cursorAt, cursorID any
	if !at.IsZero() {
		cursorAt = at
		cursorID = id
	}
	rows, err := s.pool.Query(ctx, `SELECT `+readinessEventSelect+`,(SELECT resolved_at FROM mcp_readiness_incidents i WHERE i.id=e.incident_id) FROM mcp_readiness_incident_events e WHERE incident_id IN(SELECT id FROM mcp_readiness_incidents WHERE account_id=$1 AND app_id=$2) AND ($3::text='' OR (payload->'mcp_readiness_scopes') ? $3) AND ($4::timestamptz IS NULL OR (occurred_at,id)<($4::timestamptz,$5::uuid)) ORDER BY occurred_at DESC,id DESC LIMIT $6`, account, app, scope, cursorAt, cursorID, limit+1)
	if err != nil {
		return api.MCPReadinessIncidentTimeline{}, err
	}
	defer rows.Close()
	out := api.MCPReadinessIncidentTimeline{Events: []api.MCPReadinessIncidentEvent{}}
	var last readinessIncidentEventRecord
	for rows.Next() {
		var resolved *time.Time
		e, err := scanReadinessEvent(rows, &resolved)
		if err != nil {
			return out, err
		}
		if len(out.Events) == limit {
			out.NextBefore = api.MCPReadinessIncidentCursor(last.At, last.ID)
			break
		}
		out.Events = append(out.Events, incidentRecordView(e, readinessIncident{ResolvedAt: resolved}, time.Now().UTC()))
		last = e
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	return out, nil
}
