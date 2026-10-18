package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type MCPReadinessMaintenanceStore interface {
	ListMCPReadinessMaintenance(context.Context, string, string, string, string, int) (api.MCPReadinessMaintenancePage, error)
	CreateMCPReadinessMaintenance(context.Context, string, string, api.CreateMCPReadinessMaintenanceRequest, time.Time) (api.MCPReadinessMaintenanceWindow, error)
	CancelMCPReadinessMaintenance(context.Context, string, string, string, string, time.Time) (api.MCPReadinessMaintenanceWindow, error)
}
type readinessMaintenanceRecord struct {
	AccountID, AppID string
	Window           api.MCPReadinessMaintenanceWindow
}

func maintenanceWindowActive(w api.MCPReadinessMaintenanceWindow, at time.Time) bool {
	return !at.Before(w.StartsAt) && at.Before(w.EndsAt) && (w.CancelledAt == nil || at.Before(*w.CancelledAt))
}

// All observed environments must be covered. A staging window must never mute a
// mixed-environment rule's production failure. Empty/unknown coverage fails open.
func readinessMaintenanceIDs(rule AlertRule, payload json.RawMessage, windows []api.MCPReadinessMaintenanceWindow, at time.Time) []string {
	scopes := []string{rule.MCPReadinessScope}
	if rule.MCPReadinessScope == "" {
		var p struct {
			Scopes []string `json:"mcp_readiness_scopes"`
		}
		if json.Unmarshal(payload, &p) != nil {
			return nil
		}
		scopes = p.Scopes
	}
	if len(scopes) == 0 {
		return nil
	}
	ids := map[string]bool{}
	for _, scope := range scopes {
		if api.ValidateScope(scope) != nil {
			return nil
		}
		covered := false
		for _, w := range windows {
			if w.Scope == scope && maintenanceWindowActive(w, at) {
				covered = true
				ids[w.ID] = true
			}
		}
		if !covered {
			return nil
		}
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
func suppressReadinessEvent(e *readinessIncidentEventRecord, ids []string, reason string) {
	e.Version++
	e.Status = "suppressed"
	e.MaintenanceWindowIDs = append([]string(nil), ids...)
	e.SuppressionReason = reason
	e.LeaseToken = ""
	e.LeaseUntil = time.Time{}
}
func readinessMaintenanceTransition(rule AlertRule, o MCPReadinessObservation, active *readinessIncident, events []readinessIncidentEventRecord, windows []api.MCPReadinessMaintenanceWindow) (*readinessIncidentEventRecord, *readinessIncident, AlertState, error) {
	ids := readinessMaintenanceIDs(rule, o.Payload, windows, o.At)
	pending := active != nil && active.MaintenancePending
	event, incident, to, err := decideReadinessIncident(rule, o, active)
	if err != nil || incident == nil {
		return event, incident, to, err
	}
	if len(ids) > 0 {
		incident.MaintenancePending = true
		if event != nil {
			suppressReadinessEvent(event, ids, "maintenance")
		}
		return event, incident, to, nil
	}
	if !pending {
		return event, incident, to, nil
	}
	// Recovery is never manufactured from stale/missing coverage. Leave pending
	// until a complete matching observation can confirm breach or recovery.
	if to == AlertStateUnknown {
		return event, incident, to, nil
	}
	incident.MaintenancePending = false
	if o.Breached && event == nil {
		id := uuid.NewString()
		var p map[string]any
		if json.Unmarshal(o.Payload, &p) != nil {
			return nil, nil, to, ErrInvalidArgument
		}
		p["event"] = "alert.fired"
		p["incident_id"] = incident.ID
		p["event_id"] = id
		p["occurred_at"] = o.At.UTC().Format(time.RFC3339Nano)
		p["incident_started_at"] = incident.OpenedAt.UTC().Format(time.RFC3339Nano)
		p["notification_reason"] = "maintenance_ended"
		body, _ := json.Marshal(p)
		event = &readinessIncidentEventRecord{ID: id, IncidentID: incident.ID, RuleID: rule.ID, Kind: "alert.fired", At: o.At, Payload: body, Observed: o.ObservedValue, Status: "pending", AvailableAt: o.At, NotificationReason: "maintenance_ended"}
	} else if event != nil && !o.Breached {
		attempted := false
		var priorIDs []string
		for _, e := range events {
			if e.IncidentID == incident.ID {
				if e.Kind == "alert.fired" && e.Attempts > 0 {
					attempted = true
				}
				priorIDs = append(priorIDs, e.MaintenanceWindowIDs...)
			}
		}
		if !attempted {
			suppressReadinessEvent(event, priorIDs, "maintenance_recovered")
		}
	}
	return event, incident, to, nil
}
func maintenanceListArgs(scope, before string, limit int) (time.Time, string, error) {
	return validateReadinessIncidentList(api.MCPConnectionScope(scope), before, limit)
}

// If the evaluator was down throughout a window, the first fresh observation
// after restart still reconciles it exactly once via the observation cursor.
func readinessMaintenanceSince(rule AlertRule, payload json.RawMessage, windows []api.MCPReadinessMaintenanceWindow, after, at time.Time) bool {
	for _, w := range windows {
		since := w.StartsAt
		if w.CreatedAt.After(since) {
			since = w.CreatedAt
		}
		if since.After(after) && !since.After(at) && len(readinessMaintenanceIDs(rule, payload, windows, since)) > 0 {
			return true
		}
	}
	return false
}

func readinessQueuedMaintenanceIDs(rule AlertRule, e readinessIncidentEventRecord, windows []api.MCPReadinessMaintenanceWindow, now time.Time) []string {
	// A delayed claim can predate events committed by a newer observation.
	if e.At.After(now) {
		return nil
	}
	if ids := readinessMaintenanceIDs(rule, e.Payload, windows, now); len(ids) > 0 {
		return ids
	}
	if ids := readinessMaintenanceIDs(rule, e.Payload, windows, e.At); len(ids) > 0 {
		return ids
	}
	for _, w := range windows {
		if !w.StartsAt.Before(e.At) && !w.StartsAt.After(now) {
			if ids := readinessMaintenanceIDs(rule, e.Payload, windows, w.StartsAt); len(ids) > 0 {
				return ids
			}
		}
	}
	return nil
}

// A newer unmuted continuation already represents this open incident. An old
// lease expiring afterward must not rearm maintenance reconciliation. A later
// observation independently detects any window crossed since its cursor.
func readinessSuppressionNeedsReconciliation(rule AlertRule, event readinessIncidentEventRecord, events []readinessIncidentEventRecord, windows []api.MCPReadinessMaintenanceWindow, at time.Time) bool {
	for _, later := range events {
		if later.IncidentID == event.IncidentID && later.Sequence > event.Sequence && later.Kind == "alert.fired" && later.Status != "suppressed" && len(readinessMaintenanceIDs(rule, later.Payload, windows, at)) == 0 {
			return false
		}
	}
	return true
}
