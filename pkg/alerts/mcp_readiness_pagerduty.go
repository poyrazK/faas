package alerts

import (
	"context"
	"encoding/json"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/pagerduty"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func (e *Evaluator) deliverMCPReadinessPagerDuty(ctx context.Context, claim state.MCPReadinessDeliveryClaim, stats *Stats) {
	source, ok := e.store.(state.MCPReadinessProviderOutboxStore)
	if !ok {
		return
	}
	result := state.MCPReadinessProviderResult{Failure: "secret_unavailable"}
	if key, ok := e.pagerDutyKey(claim.Rule.WebhookSecretSealed); ok {
		var payload struct {
			IncidentID string    `json:"incident_id"`
			EventID    string    `json:"event_id"`
			Event      string    `json:"event"`
			At         time.Time `json:"occurred_at"`
			Reason     string    `json:"notification_reason"`
			Number     int       `json:"reminder_number"`
		}
		result.Failure = "invalid_event"
		if json.Unmarshal(claim.Delivery.Payload, &payload) == nil {
			dispatchCtx, cancel := context.WithTimeout(ctx, state.MCPReadinessDeliveryLease/2)
			r := e.pagerDuty.Send(dispatchCtx, key, claim.Rule.WebhookURL, pagerduty.Event{IncidentID: payload.IncidentID, EventID: payload.EventID, Kind: payload.Event, OccurredAt: payload.At, RuleName: claim.Rule.Name, RuleID: claim.Rule.ID, AppID: claim.Rule.AppID, NotificationReason: payload.Reason, ReminderNumber: payload.Number}, e.now)
			cancel()
			result = state.MCPReadinessProviderResult{Accepted: r.Accepted, StatusCode: r.StatusCode, Failure: r.Failure, RetryAfter: r.RetryAfter}
		}
	}
	if err := source.CompleteMCPReadinessProviderDelivery(ctx, claim.EventID, claim.Token, e.now(), result); err != nil {
		e.log.Warn("alerts: complete PagerDuty delivery", "rule", claim.Rule.ID, "err", err)
		return
	}
	outcome, kind := AlertOutcomeFailed, "alert.failed"
	if result.Accepted {
		stats.Delivered++
		outcome, kind = AlertOutcomeDelivered, "alert.delivered"
	} else {
		stats.Failed++
	}
	if e.ops != nil {
		e.ops.AlertDeliveryAttemptsTotal(outcome)()
	}
	if e.audit != nil {
		e.audit.Emit(ctx, kind, &claim.Rule.AccountID, map[string]any{"rule_id": claim.Rule.ID, "event_id": claim.EventID, "delivery_id": claim.Delivery.ID, "provider": "pagerduty", "accepted": result.Accepted, "status_code": result.StatusCode})
	}
}
func (e *Evaluator) pagerDutyKey(sealed []byte) (string, bool) {
	var identities []*age.X25519Identity
	if e.identities != nil {
		identities = e.identities()
	}
	if len(identities) == 0 && e.identity != nil {
		if v := e.identity(); v != nil {
			identities = []*age.X25519Identity{v}
		}
	}
	if len(identities) == 0 {
		return "", false
	}
	ns, raw, err := secretbox.OpenBytesMulti(identities, sealed)
	if err != nil || ns != api.MCPReadinessPagerDutySecretNamespace || !api.ValidMCPReadinessPagerDutyKey(string(raw)) {
		return "", false
	}
	return string(raw), true
}
