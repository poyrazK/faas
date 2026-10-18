package state

import (
	"context"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type MCPReadinessNotificationStore interface {
	ListMCPReadinessNotificationQueues(context.Context, string, string, string, string, int, time.Time) (api.MCPReadinessNotificationQueues, error)
	ActMCPReadinessNotification(context.Context, string, string, api.MCPReadinessNotificationAction, time.Time) (api.MCPReadinessNotificationReceipt, error)
}
type readinessNotificationActionRecord struct {
	AccountID string
	Request   api.MCPReadinessNotificationAction
	Receipt   api.MCPReadinessNotificationReceipt
}

func readinessNotificationTerminal(status string) bool {
	return status == "delivered" || status == "suppressed" || status == "abandoned"
}
func readinessNotificationListArgs(scope, after string, limit int) error {
	if limit < 1 || limit > 50 || scope != "" && api.ValidateScope(scope) != nil || after != "" && uuid.Validate(after) != nil {
		return ErrInvalidArgument
	}
	return nil
}
func canonicalReadinessNotificationAction(r api.MCPReadinessNotificationAction) (api.MCPReadinessNotificationAction, error) {
	if api.ValidateMCPReadinessNotificationAction(r) != nil {
		return r, ErrInvalidArgument
	}
	r.RequestID = uuid.MustParse(r.RequestID).String()
	r.EventID = uuid.MustParse(r.EventID).String()
	return cloneReadiness(r), nil
}
func sameReadinessNotificationAction(a, b api.MCPReadinessNotificationAction) bool {
	return a.Destination == b.Destination && a.RequestID == b.RequestID && a.EventID == b.EventID && a.Action == b.Action && a.Reason == b.Reason && *a.ExpectedVersion == *b.ExpectedVersion
}
func readinessNotificationQueue(rule AlertRule, e *readinessIncidentEventRecord, incident readinessIncident, count int64, oldest, now time.Time, windows []api.MCPReadinessMaintenanceWindow) api.MCPReadinessNotificationQueue {
	q := api.MCPReadinessNotificationQueue{RuleID: rule.ID, Name: rule.Name, ReadinessScope: rule.MCPReadinessScope, Enabled: rule.Enabled, OutstandingCount: count, State: "idle"}
	if e == nil {
		return q
	}
	q.BlockedCount = count - 1
	q.OldestAgeSeconds = math.Max(0, now.Sub(oldest).Seconds())
	head := incidentRecordView(*e, incident, now)
	q.Head = &head
	switch {
	case e.LeaseToken != "" && now.Before(e.LeaseUntil):
		q.State = "delivering"
	case !rule.Enabled:
		q.State = "paused"
	case len(readinessQueuedMaintenanceIDs(rule, *e, windows, now)) > 0:
		q.State = "maintenance"
	case now.Before(e.AvailableAt) || now.Before(e.At):
		q.State = "delayed"
	default:
		q.State = "ready"
	}
	if e.Status == "failed" {
		switch {
		case e.LastStatusCode >= 500:
			q.FailureKind = "http_5xx"
		case e.LastStatusCode >= 400:
			q.FailureKind = "http_4xx"
		case e.LastStatusCode != 0:
			q.FailureKind = "http_other"
		default:
			q.FailureKind = "transport"
		}
	} else if e.LeaseToken != "" && !now.Before(e.LeaseUntil) {
		q.FailureKind = "lease_expired"
	}
	q.CanAbandon = rule.Enabled && q.State != "delivering" && q.State != "maintenance" && !now.Before(e.At)
	q.CanRetry = q.CanAbandon && e.Attempts > 0 && (e.Status == "failed" || e.LeaseToken != "")
	return q
}
func decideReadinessNotificationAction(rule AlertRule, e readinessIncidentEventRecord, r api.MCPReadinessNotificationAction, now time.Time, windows []api.MCPReadinessMaintenanceWindow) (readinessIncidentEventRecord, api.MCPReadinessNotificationReceipt, error) {
	view := e
	if r.Destination != "" {
		d, ok := e.Destinations[r.Destination]
		if !ok || readinessDestinationTerminal(d.Status) {
			return e, api.MCPReadinessNotificationReceipt{}, ErrConflict
		}
		view = readinessDestinationEvent(e, d)
	}
	q := readinessNotificationQueue(rule, &view, readinessIncident{}, 1, e.At, now, windows)
	if !sameReadinessObservationRule(rule, rule) || readinessNotificationTerminal(e.Status) || e.Version != *r.ExpectedVersion || e.Version == math.MaxInt64 || r.Action == "retry" && !q.CanRetry || r.Action == "abandon" && !q.CanAbandon {
		return e, api.MCPReadinessNotificationReceipt{}, ErrConflict
	}
	e.Version++
	receipt := api.MCPReadinessNotificationReceipt{Destination: r.Destination, RequestID: r.RequestID, EventID: e.ID, Action: r.Action, Reason: r.Reason, CreatedAt: now, CreatedBy: rule.AccountID, DeliveryVersion: e.Version}
	for name, d := range e.Destinations {
		if r.Destination != "" && name != r.Destination || readinessDestinationTerminal(d.Status) {
			continue
		}
		d.LeaseToken, d.LeaseUntil = "", time.Time{}
		if r.Action == "abandon" {
			d.Status = "abandoned"
		} else {
			d.Status = "failed"
			d.AvailableAt = now
		}
		e.Destinations[name] = d
	}
	e.LeaseToken, e.LeaseUntil = "", time.Time{}
	if r.Action == "abandon" {
		e.Status = "abandoned"
	} else {
		e.Status = "failed"
		e.AvailableAt = now
	}
	projectReadinessDestinations(&e)
	e.LastOperatorAction = &receipt
	return e, receipt, nil
}
func readinessDestinationEvent(e readinessIncidentEventRecord, d readinessDestinationDelivery) readinessIncidentEventRecord {
	e.Status, e.Attempts, e.AvailableAt = d.Status, d.Attempts, d.AvailableAt
	e.LeaseToken, e.LeaseUntil = d.LeaseToken, d.LeaseUntil
	e.DeliveryID, e.LastAttemptAt, e.LastStatusCode = d.DeliveryID, d.LastAttemptAt, d.LastStatusCode
	return e
}
func readinessNotificationQueues(rule AlertRule, policy MCPReadinessFallbackConfig, events []readinessIncidentEventRecord, incidents map[string]readinessIncident, now time.Time, windows []api.MCPReadinessMaintenanceWindow) api.MCPReadinessNotificationQueue {
	var head *readinessIncidentEventRecord
	var oldest time.Time
	for _, e := range events {
		if head == nil || e.Sequence < head.Sequence {
			v := e
			head = &v
		}
		if oldest.IsZero() || e.At.Before(oldest) {
			oldest = e.At
		}
	}
	var incident readinessIncident
	if head != nil {
		incident = incidents[head.IncidentID]
	}
	q := readinessNotificationQueue(rule, head, incident, int64(len(events)), oldest, now, windows)
	plan := readinessDestinationPlan(rule, policy, events, incidents)
	for _, name := range []string{"primary", "fallback"} {
		v := api.MCPReadinessDestinationQueue{Destination: name, State: "idle"}
		for _, c := range plan {
			if c.Destination != name {
				continue
			}
			v.OutstandingCount++
			if !c.Head {
				continue
			}
			e := c.Event
			if !c.WaitingPrimary {
				e = readinessDestinationEvent(e, e.Destinations[name])
			}
			h := readinessNotificationQueue(rule, &e, incidents[e.IncidentID], 1, e.At, now, windows)
			target := c.Route.Primary
			if name == "fallback" {
				target = c.Route.Fallback
			}
			v.Provider = readinessProvider(target)
			v.Head, v.State, v.FailureKind, v.CanRetry, v.CanAbandon = h.Head, h.State, h.FailureKind, h.CanRetry, h.CanAbandon
			if c.ReceiverBlocked {
				v.State = "waiting_receiver"
				v.CanRetry = false
				v.CanAbandon = false
			}
			if c.WaitingPrimary {
				v.State = "waiting_primary"
				v.CanRetry = false
				v.CanAbandon = false
			}
		}
		if v.OutstandingCount > 0 {
			v.BlockedCount = v.OutstandingCount - 1
			q.Destinations = append(q.Destinations, v)
		}
	}
	return q
}
func readinessNotificationActionEvent(rule AlertRule, policy MCPReadinessFallbackConfig, events []readinessIncidentEventRecord, incidents map[string]readinessIncident, r api.MCPReadinessNotificationAction) (readinessIncidentEventRecord, error) {
	if r.Destination == "" {
		var head *readinessIncidentEventRecord
		for _, e := range events {
			if head == nil || e.Sequence < head.Sequence {
				v := e
				head = &v
			}
		}
		if head == nil || head.ID != r.EventID {
			return readinessIncidentEventRecord{}, ErrConflict
		}
		e := cloneReadiness(*head)
		prepareReadinessDestinations(&e, incidents[e.IncidentID].Routing)
		return e, nil
	}
	for _, c := range readinessDestinationPlan(rule, policy, events, incidents) {
		if c.Head && !c.WaitingPrimary && !c.ReceiverBlocked && c.Destination == r.Destination && c.Event.ID == r.EventID {
			return c.Event, nil
		}
	}
	return readinessIncidentEventRecord{}, ErrConflict
}

// Retiring an expired fence must not erase the possibility that the fired HTTP
// request reached primary before its worker disappeared.
func readinessActionPrimaryUncertain(e readinessIncidentEventRecord, r api.MCPReadinessNotificationAction, now time.Time) bool {
	return (r.Destination == "" || r.Destination == "primary") && readinessPrimaryUncertain(e, now)
}
func readinessPrimaryUncertain(e readinessIncidentEventRecord, now time.Time) bool {
	if e.Kind != "alert.fired" {
		return false
	}
	if d, ok := e.Destinations["primary"]; ok {
		return !readinessDestinationTerminal(d.Status) && d.LeaseToken != "" && !now.Before(d.LeaseUntil)
	}
	return e.LeaseToken != "" && !now.Before(e.LeaseUntil) && (e.Routing.LastDestination == "" || e.Routing.LastDestination == "primary")
}
