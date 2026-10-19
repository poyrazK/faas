package state

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const MCPReadinessDeliveryLease = 2 * time.Minute

type MCPReadinessObservation struct {
	Rule          AlertRule
	At            time.Time
	Breached      bool
	Coverage      string
	Payload       json.RawMessage
	ObservedValue float64
}
type MCPReadinessTransition struct{ EventID, IncidentID, Event string }
type MCPReadinessDeliveryClaim struct {
	Provider       string
	EventID, Token string
	Delivery       AlertDelivery
	Rule           AlertRule
	LeaseUntil     time.Time
}
type MCPReadinessOutboxStore interface {
	RecordMCPReadinessObservation(context.Context, MCPReadinessObservation) (MCPReadinessTransition, error)
	ClaimMCPReadinessDelivery(context.Context, string, time.Time) (MCPReadinessDeliveryClaim, bool, error)
	CompleteMCPReadinessDelivery(context.Context, string, string, time.Time) error
}
type readinessIncident struct {
	Routing                                       readinessIncidentRouting
	ID, RuleID, AccountID, AppID, Scope, Coverage string
	OpenedAt                                      time.Time
	MaintenancePending                            bool
	ResolvedAt                                    *time.Time
}
type readinessIncidentEventRecord struct {
	Destinations                          map[string]readinessDestinationDelivery
	Routing                               api.MCPReadinessDeliveryRouting
	ID, IncidentID, RuleID, Kind          string
	Version                               int64
	ReminderNumber                        int
	LastOperatorAction                    *api.MCPReadinessNotificationReceipt
	Sequence                              int64
	At                                    time.Time
	Payload                               json.RawMessage
	Observed                              float64
	Status                                string
	Attempts                              int
	AvailableAt                           time.Time
	LeaseToken                            string
	LeaseUntil                            time.Time
	DeliveryID                            string
	LastAttemptAt                         *time.Time
	LastStatusCode                        int
	MaintenanceWindowIDs                  []string
	SuppressionReason, NotificationReason string
}

func sameReadinessObservationRule(current, expected AlertRule) bool {
	return current.Enabled && string(current.Metric) == api.MetricMCPReadinessUnhealthy && current.Action == AlertActionWebhook && current.AccountID == expected.AccountID && current.AppID == expected.AppID && current.MCPReadinessScope == expected.MCPReadinessScope && current.Comparison == expected.Comparison && current.Threshold == expected.Threshold
}
func validateReadinessObservation(o MCPReadinessObservation) error {
	if o.At.IsZero() || o.Rule.AppID == "" || !api.IsFiniteFloat(o.ObservedValue) || len(o.Coverage) != 64 {
		return ErrInvalidArgument
	}
	if _, err := hex.DecodeString(o.Coverage); err != nil {
		return ErrInvalidArgument
	}
	var p map[string]any
	if json.Unmarshal(o.Payload, &p) != nil || p["metric"] != api.MetricMCPReadinessUnhealthy || p["mcp_readiness_coverage_fingerprint"] != o.Coverage {
		return ErrInvalidArgument
	}
	return nil
}
func decideReadinessIncident(rule AlertRule, o MCPReadinessObservation, active *readinessIncident) (*readinessIncidentEventRecord, *readinessIncident, AlertState, error) {
	to := AlertStateOk
	if o.Breached {
		to = AlertStateFiring
		if active != nil {
			return nil, active, to, nil
		}
	} else {
		if active == nil {
			return nil, nil, to, nil
		}
		if active.Coverage != o.Coverage {
			return nil, active, AlertStateUnknown, nil
		}
	}
	kind := "alert.resolved"
	if o.Breached {
		kind = "alert.fired"
		active = &readinessIncident{ID: uuid.NewString(), RuleID: rule.ID, AccountID: rule.AccountID, AppID: rule.AppID, Scope: rule.MCPReadinessScope, Coverage: o.Coverage, OpenedAt: o.At}
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(active.ID+":"+kind)).String()
	var payload map[string]any
	if err := json.Unmarshal(o.Payload, &payload); err != nil {
		return nil, nil, to, ErrInvalidArgument
	}
	payload["incident_id"] = active.ID
	payload["event_id"] = id
	payload["event"] = kind
	payload["occurred_at"] = o.At.UTC().Format(time.RFC3339Nano)
	payload["incident_started_at"] = active.OpenedAt.UTC().Format(time.RFC3339Nano)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, to, err
	}
	if !o.Breached {
		at := o.At
		active.ResolvedAt = &at
	}
	return &readinessIncidentEventRecord{ID: id, IncidentID: active.ID, RuleID: rule.ID, Kind: kind, At: o.At, Payload: body, Observed: o.ObservedValue, Status: "pending", AvailableAt: o.At}, active, to, nil
}
func incidentRecordView(event readinessIncidentEventRecord, incident readinessIncident, now time.Time) api.MCPReadinessIncidentEvent {
	view := readinessIncidentEvent(AlertDelivery{ID: event.ID, RuleID: event.RuleID, Payload: event.Payload, FiredAt: event.At, Status: AlertDeliveryStatus(event.Status)})
	view.DeliveryVersion = event.Version
	view.DestinationDeliveries = readinessDestinationViews(event, now)
	view.ReminderNumber = event.ReminderNumber
	if event.Routing.LastDestination != "" {
		route := event.Routing
		view.DeliveryRouting = &route
	}
	view.LastOperatorAction = cloneReadiness(event.LastOperatorAction)
	view.OccurredAt = event.At
	view.AttemptedAt = event.LastAttemptAt
	view.AttemptCount = event.Attempts
	view.IncidentStatus = "open"
	if incident.ResolvedAt != nil {
		view.IncidentStatus = "resolved"
	}
	view.LastDeliveryID = event.DeliveryID
	view.LastStatusCode = event.LastStatusCode
	view.MaintenanceWindowIDs = append([]string(nil), event.MaintenanceWindowIDs...)
	view.SuppressionReason = event.SuppressionReason
	view.NotificationReason = event.NotificationReason
	if !readinessNotificationTerminal(event.Status) {
		at := event.AvailableAt
		view.NextAttemptAt = &at
	}
	if event.LeaseToken != "" && now.Before(event.LeaseUntil) {
		view.DeliveryStatus = "delivering"
		at := event.LeaseUntil
		view.NextAttemptAt = &at
	}
	return view
}
