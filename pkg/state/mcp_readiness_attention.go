package state

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Reminder dispatch needs a current matching evaluator observation, even after
// a restart or a monitoring outage. Ordinary incident facts remain deliverable.
const MCPReadinessReminderObservationMaxAge = 2 * time.Minute

type MCPReadinessAttentionStore interface {
	ListMCPReadinessAttention(context.Context, string, string, string, string, int, bool, time.Time) (api.MCPReadinessAttentionPage, error)
	ActMCPReadinessAttention(context.Context, string, string, string, api.MCPReadinessAttentionAction, time.Time) (api.MCPReadinessAttentionReceipt, error)
	ListMCPReadinessAttentionHistory(context.Context, string, string, string, string, int) (api.MCPReadinessAttentionHistory, error)
}
type readinessAttentionRecord struct {
	PagerDutyTracking bool                                              `json:"pagerduty_tracking,omitempty"`
	PagerDutyStates   map[string]readinessPagerDutyAcknowledgementState `json:"pagerduty_states,omitempty"`
	LocalDecisionAt   *time.Time                                        `json:"local_decision_at,omitempty"`
	PolicySource      string                                            `json:"policy_source,omitempty"`
	DefaultsVersion   *int64                                            `json:"defaults_version,omitempty"`
	Version           int64                                             `json:"version"`
	Acknowledgement   *api.MCPReadinessAcknowledgement                  `json:"acknowledgement,omitempty"`
	Policy            api.MCPReadinessReminderPolicy                    `json:"policy"`
	Count             int                                               `json:"count"`
	NextAt            *time.Time                                        `json:"next_at,omitempty"`
	LastObservedAt    *time.Time                                        `json:"last_observed_at,omitempty"`
	LastUnhealthyAt   *time.Time                                        `json:"last_unhealthy_at,omitempty"`
	LastAction        *api.MCPReadinessAttentionReceipt                 `json:"last_action,omitempty"`
}
type readinessAttentionActionRecord struct {
	AccountID, AppID, IncidentID string
	Request                      api.MCPReadinessAttentionAction
	Receipt                      api.MCPReadinessAttentionReceipt
}

func readinessAcknowledged(r readinessAttentionRecord, now time.Time) bool {
	return r.Acknowledgement != nil && (r.Acknowledgement.ExpiresAt == nil || now.Before(*r.Acknowledgement.ExpiresAt))
}
func readinessAttentionView(i readinessIncident, rule AlertRule, r readinessAttentionRecord, now time.Time) api.MCPReadinessIncidentAttention {
	v := api.MCPReadinessIncidentAttention{ReminderPolicySource: r.PolicySource, ReminderDefaultsVersion: r.DefaultsVersion, IncidentID: i.ID, RuleID: i.RuleID, RuleName: rule.Name, RuleEnabled: rule.Enabled, ReadinessScope: i.Scope, OpenedAt: i.OpenedAt, ResolvedAt: i.ResolvedAt, Version: r.Version, State: "unacknowledged", Acknowledgement: r.Acknowledgement, ReminderPolicy: r.Policy, RemindersCreated: r.Count, NextReminderAt: r.NextAt, LastObservedAt: r.LastObservedAt, LastUnhealthyAt: r.LastUnhealthyAt, LastAction: r.LastAction}
	v.ProviderAcknowledgements = readinessPagerDutyAcknowledgementHolds(r)
	if v.ReminderPolicySource == "" {
		v.ReminderPolicySource = "legacy"
	}
	switch {
	case i.ResolvedAt != nil:
		v.State = "resolved"
	case readinessAcknowledged(r, now):
		v.State = "acknowledged"
	case r.Acknowledgement != nil:
		v.State = "expired"
	}
	if i.ResolvedAt != nil || !rule.Enabled || readinessAcknowledged(r, now) || r.Policy.MaxReminders == 0 || r.Count >= r.Policy.MaxReminders {
		v.NextReminderAt = nil
	}
	return cloneReadiness(v)
}
func canonicalReadinessAttentionAction(r api.MCPReadinessAttentionAction) (api.MCPReadinessAttentionAction, error) {
	if api.ValidateMCPReadinessAttentionAction(r) != nil {
		return r, ErrInvalidArgument
	}
	r.RequestID = uuid.MustParse(r.RequestID).String()
	r = cloneReadiness(r)
	if r.ExpiresAt != nil {
		at := r.ExpiresAt.UTC()
		r.ExpiresAt = &at
	}
	return r, nil
}
func sameReadinessAttentionAction(a, b api.MCPReadinessAttentionAction) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func decideReadinessAttentionAction(i readinessIncident, r readinessAttentionRecord, request api.MCPReadinessAttentionAction, now time.Time) (readinessAttentionRecord, api.MCPReadinessAttentionReceipt, error) {
	if i.ResolvedAt != nil || now.Before(i.OpenedAt) || r.Version != *request.ExpectedVersion || r.Version == math.MaxInt64 {
		return r, api.MCPReadinessAttentionReceipt{}, ErrConflict
	}
	if request.ExpiresAt != nil && (!request.ExpiresAt.After(now) || request.ExpiresAt.Sub(now) > 30*24*time.Hour) {
		return r, api.MCPReadinessAttentionReceipt{}, ErrInvalidArgument
	}
	receipt := api.MCPReadinessAttentionReceipt{RequestID: request.RequestID, IncidentID: i.ID, Action: request.Action, Reason: request.Reason, CreatedBy: i.AccountID, CreatedAt: now, Version: r.Version + 1, ExpiresAt: request.ExpiresAt, ReminderPolicy: request.ReminderPolicy}
	switch request.Action {
	case "acknowledge":
		r.Acknowledgement = &api.MCPReadinessAcknowledgement{RequestID: request.RequestID, CreatedBy: i.AccountID, CreatedAt: now, Reason: request.Reason, ExpiresAt: request.ExpiresAt}
	case "release":
		if r.Acknowledgement == nil {
			return r, receipt, ErrConflict
		}
		r.Acknowledgement = nil
		if r.Policy.MaxReminders > 0 {
			at := now.Add(time.Duration(r.Policy.IntervalSeconds) * time.Second)
			r.NextAt = &at
		}
	case "configure_reminders":
		r.PolicySource = "incident_override"
		r.Policy = *request.ReminderPolicy
		r.NextAt = nil
		if r.Policy.MaxReminders > 0 {
			at := now.Add(time.Duration(r.Policy.IntervalSeconds) * time.Second)
			r.NextAt = &at
		}
	}
	if request.Action == "acknowledge" || request.Action == "release" {
		r.PagerDutyStates = nil
		r.PagerDutyTracking = false
	}
	r.LocalDecisionAt = &now
	r.Version++
	r.LastAction = &receipt
	return r, receipt, nil
}
func recordReadinessAttentionObservation(r readinessAttentionRecord, i readinessIncident, o MCPReadinessObservation, normal *readinessIncidentEventRecord) readinessAttentionRecord {
	at := o.At
	r.LastObservedAt = &at
	r.LastUnhealthyAt = nil
	if o.Breached && i.ResolvedAt == nil && i.Coverage == o.Coverage {
		r.LastUnhealthyAt = &at
	}
	if normal != nil && normal.Kind == "alert.fired" && r.Policy.MaxReminders > 0 {
		next := at.Add(time.Duration(r.Policy.IntervalSeconds) * time.Second)
		if r.NextAt == nil || r.NextAt.Before(next) {
			r.NextAt = &next
		}
	}
	return r
}
func readinessReminderSuppression(i readinessIncident, r readinessAttentionRecord, e readinessIncidentEventRecord, now time.Time) string {
	if e.NotificationReason != "reminder" || e.At.After(now) || readinessNotificationTerminal(e.Status) || e.LeaseToken != "" && now.Before(e.LeaseUntil) {
		return ""
	}
	switch {
	case i.ResolvedAt != nil:
		return "incident_resolved"
	case readinessAcknowledged(r, now):
		return "acknowledged"
	case r.Policy.MaxReminders == 0:
		return "reminders_disabled"
	case e.ReminderNumber > r.Policy.MaxReminders || r.LastObservedAt == nil || r.LastUnhealthyAt == nil || !r.LastObservedAt.Equal(*r.LastUnhealthyAt) || now.Before(*r.LastUnhealthyAt) || now.Sub(*r.LastUnhealthyAt) > MCPReadinessReminderObservationMaxAge:
		return "reminder_obsolete"
	}
	return ""
}
func decideReadinessReminder(rule AlertRule, i readinessIncident, r readinessAttentionRecord, o MCPReadinessObservation, normal *readinessIncidentEventRecord, pending bool, windows []api.MCPReadinessMaintenanceWindow) (readinessAttentionRecord, *readinessIncidentEventRecord, error) {
	if normal != nil || pending || !o.Breached || i.ResolvedAt != nil || i.Coverage != o.Coverage || readinessAcknowledged(r, o.At) || r.Policy.MaxReminders == 0 || r.Count >= r.Policy.MaxReminders || r.Count >= api.MCPReadinessReminderMaxCount || r.Version == math.MaxInt64 || r.NextAt == nil || o.At.Before(*r.NextAt) || len(readinessMaintenanceIDs(rule, o.Payload, windows, o.At)) > 0 {
		return r, nil, nil
	}
	number := r.Count + 1
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(i.ID+":reminder:"+strconv.Itoa(number))).String()
	var p map[string]any
	if json.Unmarshal(o.Payload, &p) != nil {
		return r, nil, ErrInvalidArgument
	}
	p["incident_id"] = i.ID
	p["event_id"] = id
	p["event"] = "alert.fired"
	p["occurred_at"] = o.At.UTC().Format(time.RFC3339Nano)
	p["incident_started_at"] = i.OpenedAt.UTC().Format(time.RFC3339Nano)
	p["notification_reason"] = "reminder"
	p["reminder_number"] = number
	body, err := json.Marshal(p)
	if err != nil {
		return r, nil, err
	}
	e := &readinessIncidentEventRecord{ID: id, IncidentID: i.ID, RuleID: rule.ID, Kind: "alert.fired", At: o.At, Payload: body, Observed: o.ObservedValue, Status: "pending", AvailableAt: o.At, NotificationReason: "reminder", ReminderNumber: number}
	r.Count++
	r.Version++
	at := o.At.Add(time.Duration(r.Policy.IntervalSeconds) * time.Second)
	r.NextAt = &at
	return r, e, nil
}
