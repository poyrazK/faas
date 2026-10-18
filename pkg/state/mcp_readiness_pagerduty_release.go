package state

import (
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Two checked destinations bound ownership and ordering without retaining raw events.
type readinessPagerDutyAcknowledgementState struct {
	Hold           api.MCPReadinessPagerDutyAcknowledgementHold `json:"hold"`
	Fingerprint    string                                       `json:"fingerprint"`
	Active         bool                                         `json:"active"`
	Ambiguous      bool                                         `json:"ambiguous,omitempty"`
	LastOccurredAt time.Time                                    `json:"last_occurred_at"`
}

func readinessPagerDutyAcknowledgementHolds(r readinessAttentionRecord) []api.MCPReadinessPagerDutyAcknowledgementHold {
	var out []api.MCPReadinessPagerDutyAcknowledgementHold
	for _, d := range []string{"primary", "fallback"} {
		if s, ok := r.PagerDutyStates[d]; ok && s.Active {
			out = append(out, s.Hold)
		}
	}
	return out
}
func readinessPagerDutyLocalDecision(r readinessAttentionRecord) *time.Time {
	if r.LocalDecisionAt != nil {
		return r.LocalDecisionAt
	}
	if r.LastAction != nil && r.LastAction.Source != "pagerduty" {
		return &r.LastAction.CreatedAt
	}
	return nil
}
func readinessPagerDutySameHold(s readinessPagerDutyAcknowledgementState, c MCPReadinessPagerDutyAcknowledgementConfig, e MCPReadinessPagerDutyAcknowledgementInput) bool {
	return s.Hold.ProviderIncidentID == e.ProviderIncidentID && s.Hold.ServiceID == e.ServiceID && s.Hold.Region == e.Region && s.Fingerprint == c.Fingerprint
}
func readinessPagerDutyNewHold(c MCPReadinessPagerDutyAcknowledgementConfig, e MCPReadinessPagerDutyAcknowledgementInput, now time.Time) readinessPagerDutyAcknowledgementState {
	return readinessPagerDutyAcknowledgementState{Active: true, Fingerprint: c.Fingerprint, LastOccurredAt: e.OccurredAt, Hold: api.MCPReadinessPagerDutyAcknowledgementHold{Destination: readinessPagerDutyAcknowledgementDestination(c), ProviderIncidentID: e.ProviderIncidentID, ProviderEventID: e.EventID, ServiceID: e.ServiceID, Region: e.Region, OccurredAt: e.OccurredAt, ReceivedAt: now, PolicyVersion: c.Version, ReleaseSyncEnabled: c.SyncUnacknowledgements}}
}
func readinessPagerDutyProviderAcknowledgement(i readinessIncident, s readinessPagerDutyAcknowledgementState) *api.MCPReadinessAcknowledgement {
	at := s.Hold.OccurredAt
	return &api.MCPReadinessAcknowledgement{RequestID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("gregale:pagerduty-ack:"+i.RuleID+":"+s.Hold.ProviderEventID)).String(), Source: "pagerduty", ProviderEventID: s.Hold.ProviderEventID, ProviderIncidentID: s.Hold.ProviderIncidentID, ProviderOccurredAt: &at, CreatedBy: i.AccountID, CreatedAt: s.Hold.ReceivedAt, Reason: "Acknowledged in PagerDuty"}
}
func readinessPagerDutyDecisionReceipt(i readinessIncident, r readinessAttentionRecord, c MCPReadinessPagerDutyAcknowledgementConfig, e MCPReadinessPagerDutyAcknowledgementInput, now time.Time) api.MCPReadinessPagerDutyAcknowledgementEvent {
	name := "gregale:pagerduty-ack:" + i.RuleID + ":" + e.EventID
	action := "acknowledge"
	if e.Action == "release" {
		name += ":release:" + readinessPagerDutyAcknowledgementDestination(c)
		action = "release"
	}
	return api.MCPReadinessPagerDutyAcknowledgementEvent{Action: action, Destination: readinessPagerDutyAcknowledgementDestination(c), Region: c.Region, ServiceID: c.ServiceID, ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)).String(), EventID: e.EventID, IncidentID: i.ID, ProviderIncidentID: e.ProviderIncidentID, OccurredAt: e.OccurredAt, ReceivedAt: now, PolicyVersion: c.Version, AttentionVersion: r.Version, Outcome: "stale"}
}

func decideReadinessPagerDutyAcknowledgement(i readinessIncident, r readinessAttentionRecord, c MCPReadinessPagerDutyAcknowledgementConfig, e MCPReadinessPagerDutyAcknowledgementInput, now time.Time) (readinessAttentionRecord, api.MCPReadinessPagerDutyAcknowledgementEvent, *readinessAttentionActionRecord, error) {
	if !r.PagerDutyTracking && e.Action == "" {
		updated, receipt, action, err := decideReadinessPagerDutyLegacyAcknowledgement(i, r, c, e, now)
		if err == nil && action != nil && c.SyncUnacknowledgements {
			updated.PagerDutyTracking = true
			updated.LocalDecisionAt = readinessPagerDutyLocalDecision(r)
			updated.PagerDutyStates = map[string]readinessPagerDutyAcknowledgementState{readinessPagerDutyAcknowledgementDestination(c): readinessPagerDutyNewHold(c, e, now)}
		}
		return updated, receipt, action, err
	}
	receipt := readinessPagerDutyDecisionReceipt(i, r, c, e, now)
	if i.ResolvedAt != nil {
		receipt.Outcome = "resolved"
		return r, receipt, nil, nil
	}
	local := readinessPagerDutyLocalDecision(r)
	if e.OccurredAt.Before(i.OpenedAt) || local != nil && !e.OccurredAt.After(*local) {
		return r, receipt, nil, nil
	}
	// Unknown legacy/manual ownership is never adopted by a release callback.
	if readinessAcknowledged(r, now) && (!r.PagerDutyTracking || r.Acknowledgement.Source != "pagerduty") {
		receipt.Outcome = "retained"
		return r, receipt, nil, nil
	}
	if e.Action == "" && r.Acknowledgement != nil && r.Acknowledgement.ExpiresAt != nil && !e.OccurredAt.After(*r.Acknowledgement.ExpiresAt) {
		return r, receipt, nil, nil
	}
	destination := readinessPagerDutyAcknowledgementDestination(c)
	previous, exists := r.PagerDutyStates[destination]
	if exists && (e.OccurredAt.Before(previous.LastOccurredAt) || e.OccurredAt.Equal(previous.LastOccurredAt) && (e.Action == "release" || previous.Active && readinessPagerDutySameHold(previous, c, e))) {
		return r, receipt, nil, nil
	}
	updated := cloneReadiness(r)
	if updated.PagerDutyStates == nil {
		updated.PagerDutyStates = map[string]readinessPagerDutyAcknowledgementState{}
	}
	updated.PagerDutyTracking = true
	updated.LocalDecisionAt = local
	if e.Action == "release" {
		if previous.Active && (!readinessPagerDutySameHold(previous, c, e) || !previous.Hold.ReleaseSyncEnabled || previous.Ambiguous) {
			receipt.Outcome = "retained"
			return r, receipt, nil, nil
		}
		previous.LastOccurredAt = e.OccurredAt
		if !exists {
			previous = readinessPagerDutyNewHold(c, e, now)
		}
		previous.Active = false
		updated.PagerDutyStates[destination] = previous
	} else {
		next := readinessPagerDutyNewHold(c, e, now)
		if previous.Active && (previous.Ambiguous || !readinessPagerDutySameHold(previous, c, e)) {
			next = previous
			next.LastOccurredAt = e.OccurredAt
			next.Ambiguous = true
			next.Hold.ReleaseSyncEnabled = false
		}
		updated.PagerDutyStates[destination] = next
	}
	return finishReadinessPagerDutyDecision(i, r, updated, receipt, e, now)
}

func finishReadinessPagerDutyDecision(i readinessIncident, old, r readinessAttentionRecord, receipt api.MCPReadinessPagerDutyAcknowledgementEvent, e MCPReadinessPagerDutyAcknowledgementInput, now time.Time) (readinessAttentionRecord, api.MCPReadinessPagerDutyAcknowledgementEvent, *readinessAttentionActionRecord, error) {
	if old.Version == math.MaxInt64 {
		return old, receipt, nil, ErrConflict
	}
	r.Version++
	receipt.AttentionVersion = r.Version
	holds := readinessPagerDutyAcknowledgementHolds(r)
	wasAcknowledged := readinessAcknowledged(old, now)
	if len(holds) > 0 {
		if !wasAcknowledged || e.Action == "release" {
			r.Acknowledgement = readinessPagerDutyProviderAcknowledgement(i, r.PagerDutyStates[holds[0].Destination])
		}
		receipt.Outcome = "already_acknowledged"
		if e.Action == "release" {
			receipt.Outcome = "retained"
		} else if !wasAcknowledged {
			receipt.Outcome = "acknowledged"
		}
	} else {
		receipt.Outcome = "not_acknowledged"
		if wasAcknowledged {
			r.Acknowledgement = nil
			receipt.Outcome = "released"
			r.NextAt = nil
			if r.Policy.MaxReminders > 0 && r.Count < r.Policy.MaxReminders {
				at := now.Add(time.Duration(r.Policy.IntervalSeconds) * time.Second)
				r.NextAt = &at
			}
		}
	}
	if receipt.Outcome != "acknowledged" && receipt.Outcome != "released" {
		return r, receipt, nil, nil
	}
	reason := "Acknowledged in PagerDuty"
	if e.Action == "release" {
		reason = "Acknowledgement released in PagerDuty"
	}
	request := api.MCPReadinessAttentionAction{RequestID: receipt.ID, ExpectedVersion: &old.Version, Action: receipt.Action, Reason: reason}
	action := api.MCPReadinessAttentionReceipt{Source: "pagerduty", ProviderEventID: e.EventID, ProviderIncidentID: e.ProviderIncidentID, ProviderOccurredAt: &receipt.OccurredAt, RequestID: receipt.ID, IncidentID: i.ID, Action: receipt.Action, Reason: reason, CreatedBy: i.AccountID, CreatedAt: now, Version: r.Version}
	r.LastAction = &action
	return r, receipt, &readinessAttentionActionRecord{AccountID: i.AccountID, AppID: i.AppID, IncidentID: i.ID, Request: request, Receipt: action}, nil
}
