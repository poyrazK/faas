package api

import "time"

type EventSubscriptionDeliveryControl struct {
	RateLimit                     *EventSubscriptionRateLimit              `json:"rate_limit,omitempty"`
	CapacityCommandBudget         *EventSubscriptionCapacityCommandBudget  `json:"capacity_command_budget,omitempty"`
	CapacityCommandReceipt        *EventSubscriptionCapacityCommandReceipt `json:"capacity_command_receipt,omitempty"`
	MaxPendingDeliveries          int                                      `json:"max_pending_deliveries"`
	EffectiveMaxPendingDeliveries int                                      `json:"effective_max_pending_deliveries"`
	LiveDeliveries                int64                                    `json:"live_deliveries"`
	CircuitBreaker                *EventCircuitBreakerResponse             `json:"circuit_breaker,omitempty"`
	SubscriptionID                string                                   `json:"subscription_id"`
	AppID                         string                                   `json:"app_id"`
	Paused                        bool                                     `json:"paused"`
	RatePerSecond                 int                                      `json:"rate_per_second"`
	PendingRecipients             int64                                    `json:"pending_recipients"`
	ProcessingRecipients          int64                                    `json:"processing_recipients"`
	OldestPendingAt               *time.Time                               `json:"oldest_pending_at,omitempty"`
	OldestAgeSeconds              float64                                  `json:"oldest_age_seconds"`
	UpdatedAt                     *time.Time                               `json:"updated_at,omitempty"`
}

type EventCircuitBreakerPolicy struct {
	FailureThresholdPct float64 `json:"failure_threshold_pct"`
	MinSamples          int64   `json:"min_samples"`
	WindowSeconds       int64   `json:"window_seconds"`
	CooldownSeconds     int64   `json:"cooldown_seconds"`
	ProbeSuccesses      int     `json:"probe_successes"`
	RecoveryMaxRate     int     `json:"recovery_max_rate_per_second"`
	RecoverySeconds     int64   `json:"recovery_seconds"`
}

type EventCircuitBreakerResponse struct {
	SubscriptionID        string                     `json:"subscription_id"`
	Enabled               bool                       `json:"enabled"`
	Policy                *EventCircuitBreakerPolicy `json:"policy,omitempty"`
	State                 string                     `json:"state"`
	Reason                string                     `json:"reason,omitempty"`
	ChangedAt             *time.Time                 `json:"changed_at,omitempty"`
	CooldownUntil         *time.Time                 `json:"cooldown_until,omitempty"`
	ProbeInFlight         bool                       `json:"probe_in_flight"`
	SuccessfulProbes      int                        `json:"successful_probes"`
	RecoveryRatePerSecond int                        `json:"recovery_rate_per_second"`
	HistoryIncomplete     bool                       `json:"history_incomplete"`
	ManualPaused          bool                       `json:"manual_paused"`
}
