package api

import "time"

// AlertDeliveryResponse is the public surface for one
// alert_deliveries row as seen by the operator pane at
// GET /v1/apps/{slug}/alerts/{id}/deliveries (ADR-123 PR-D).
//
// Closed-set vocabularies: Status is the closed set
// {pending, delivered, failed} — mirrors state.AlertDeliveryStatus.
// IsTest is the PR-D discriminator; the production-default read
// (include_test=false) hides rows with IsTest=true, the operator
// read (?include_test=true) surfaces them.
//
// LastError is truncated server-side via dashboard.FormatAlertError
// (same precedent as the dashboard's recent-deliveries pane at
// handlers_dashboard.go:756-763) so the response is bounded and
// log-injection-safe — the dashboard helper rejects CR/LF and
// clamps to a fixed length.
type AlertDeliveryResponse struct {
	ID             string    `json:"id"`
	RuleID         string    `json:"rule_id"`
	AccountID      string    `json:"account_id"`
	AppID          string    `json:"app_id,omitempty"`
	IdempotencyKey string    `json:"idempotency_key"`
	Status         string    `json:"status"`
	AttemptCount   int       `json:"attempt_count"`
	LastStatusCode int       `json:"last_status_code,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	ObservedValue  float64   `json:"observed_value"`
	FiredAt        time.Time `json:"fired_at"`
	DeliveredAt    time.Time `json:"delivered_at,omitempty"`
	IsTest         bool      `json:"is_test"`
}
