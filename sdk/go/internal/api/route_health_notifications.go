package api

import "time"

// RouteHealthTransitionWebhookPayload points to authenticated saved evidence.
// Route inventory, counts and request data do not leave through webhooks.
type RouteHealthTransitionWebhookPayload struct {
	Version                 int        `json:"version"`
	AppID                   string     `json:"app_id"`
	DeploymentID            string     `json:"deployment_id"`
	StableDeploymentID      string     `json:"stable_deployment_id"`
	DecisionID              string     `json:"decision_id"`
	BlockedDecisionID       string     `json:"blocked_decision_id,omitempty"`
	Status                  string     `json:"status"`
	HealthStatus            string     `json:"health_status"`
	Reason                  string     `json:"reason"`
	Source                  string     `json:"source"`
	CanaryStep              int        `json:"canary_step"`
	Revision                int64      `json:"revision"`
	ObservationAnchor       *time.Time `json:"observation_anchor,omitempty"`
	CheckedAt               time.Time  `json:"checked_at"`
	PreviousTrafficPercent  int        `json:"previous_traffic_percent"`
	RequestedTrafficPercent int        `json:"requested_traffic_percent"`
	HistoryPath             string     `json:"history_path"`
}
