package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

type mcpHealthKey struct{}

func (e *Evaluator) withMCPConnectionHealth(ctx context.Context, rule state.AlertRule) (context.Context, *api.MCPConnectionHealthSnapshot) {
	var health *api.MCPConnectionHealthSnapshot
	if source, ok := e.store.(state.MCPConnectionHealthStore); ok {
		read, cancel := context.WithTimeout(ctx, api.MCPPolicyConvergenceReadTimeout)
		result, err := source.MCPConnectionHealth(read, rule.AccountID, rule.AppID, 0, e.now())
		cancel()
		if err == nil {
			health = &result
		}
	}
	return context.WithValue(ctx, mcpHealthKey{}, health), health
}
func (e *Evaluator) observeMCPConnectionHealth(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	health, _ := ctx.Value(mcpHealthKey{}).(*api.MCPConnectionHealthSnapshot)
	if health == nil || !health.Complete || !api.AlertRuleActionAllowedForMetric(string(rule.Metric), string(rule.Action)) {
		return 0, false, skipDegraded
	}
	value := float64(health.Counts[api.MCPConnectionHealthKind(string(rule.Metric))])
	return value, compareFloat(value, rule.Comparison, rule.Threshold), ""
}
func (e *Evaluator) latestMCPHealthDelivery(ctx context.Context, rule state.AlertRule) (state.AlertDelivery, map[string]any, bool) {
	deliveries, err := e.store.ListAlertDeliveriesForRule(ctx, rule.ID, 1, false)
	if err != nil || len(deliveries) != 1 {
		return state.AlertDelivery{}, nil, false
	}
	delivery := deliveries[0]
	var payload map[string]any
	if json.Unmarshal(delivery.Payload, &payload) != nil || payload["metric"] != string(rule.Metric) || payload["mcp_connection_health"] == nil {
		return delivery, nil, false
	}
	return delivery, payload, true
}
func (e *Evaluator) sameMCPHealthIncident(ctx context.Context, rule state.AlertRule, health *api.MCPConnectionHealthSnapshot) bool {
	if rule.State != state.AlertStateFiring && rule.State != state.AlertStateDegraded {
		return false
	}
	delivery, payload, ok := e.latestMCPHealthDelivery(ctx, rule)
	return ok && delivery.Status == state.AlertDeliveryDelivered && payload["event"] == "alert.fired" && payload["incident_id"] == health.Fingerprints[api.MCPConnectionHealthKind(string(rule.Metric))]
}
func (e *Evaluator) recoverMCPConnectionHealth(ctx context.Context, rule state.AlertRule, observed float64, health *api.MCPConnectionHealthSnapshot, now time.Time, stats *Stats) {
	delivery, previous, ok := e.latestMCPHealthDelivery(ctx, rule)
	if !ok {
		return
	}
	// Retry a failed recovery with a fresh transport row and the same incident ID.
	recovering := previous["event"] == "alert.resolved"
	if recovering && delivery.Status == state.AlertDeliveryDelivered {
		return
	}
	if !recovering && (previous["event"] != "alert.fired" || delivery.Status != state.AlertDeliveryDelivered) {
		return
	}
	if recovering && now.Sub(delivery.FiredAt) < time.Duration(rule.CooldownMinutes)*time.Minute {
		return
	}
	_, payload, err := buildPayload(rule, observed, investigationPaths{mcpHealth: health}, nil)
	if err != nil {
		return
	}
	payload["event"] = "alert.resolved"
	payload["incident_id"] = previous["incident_id"]
	bytes, err := json.Marshal(payload)
	if err != nil {
		return
	}
	recordedAt := now
	if !recordedAt.After(delivery.FiredAt) {
		recordedAt = delivery.FiredAt.Add(time.Microsecond)
	}
	key := fmt.Sprintf("%s:mcp-recovered:%s:%d", rule.ID, previous["incident_id"], delivery.FiredAt.UnixNano())
	recorded, err := e.store.RecordAlertDelivery(ctx, state.AlertDelivery{ID: uuid.NewString(), RuleID: rule.ID, AccountID: rule.AccountID, AppID: rule.AppID, IdempotencyKey: key, Payload: bytes, Status: state.AlertDeliveryPending, ObservedValue: observed, FiredAt: recordedAt})
	if err != nil {
		return
	}
	if !recovering && e.audit != nil {
		e.audit.Emit(ctx, "alert.resolved", &rule.AccountID, map[string]any{"rule_id": rule.ID, "rule": rule.Name, "incident_id": previous["incident_id"]})
	}
	e.deliverAlert(ctx, rule, recorded.ID, observed, payload, now, stats)
}
