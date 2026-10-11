package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"time"
)

type EventCircuitBreakerPolicy struct {
	FailureThresholdPct float64 `json:"failure_threshold_pct"`
	MinSamples          int64   `json:"min_samples"`
	WindowSeconds       int64   `json:"window_seconds"`
	CooldownSeconds     int64   `json:"cooldown_seconds"`
	ProbeSuccesses      int     `json:"probe_successes"`
	RecoveryMaxRate     int     `json:"recovery_max_rate_per_second"`
	RecoverySeconds     int64   `json:"recovery_seconds"`
}

// Preserve caller-supplied defaults for omitted fields, while rejecting explicit
// nulls and unknown settings rather than silently treating them as defaults.
func (p *EventCircuitBreakerPolicy) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("circuit breaker policy must be an object")
	}
	for name, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("%s cannot be null", name)
		}
	}
	type plain EventCircuitBreakerPolicy
	value := plain(*p)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*p = EventCircuitBreakerPolicy(value)
	return nil
}

func DefaultEventCircuitBreakerPolicy() EventCircuitBreakerPolicy {
	return EventCircuitBreakerPolicy{FailureThresholdPct: EventCircuitDefaultFailurePct, MinSamples: EventCircuitDefaultSamples, WindowSeconds: EventCircuitDefaultWindowSeconds, CooldownSeconds: EventCircuitDefaultCooldownSeconds, ProbeSuccesses: EventCircuitDefaultProbeSuccesses, RecoveryMaxRate: EventCircuitDefaultRecoveryRate, RecoverySeconds: EventCircuitDefaultRecoverySeconds}
}
func (p EventCircuitBreakerPolicy) Validate() error {
	if math.IsNaN(p.FailureThresholdPct) || math.IsInf(p.FailureThresholdPct, 0) || p.FailureThresholdPct <= 0 || p.FailureThresholdPct > 100 {
		return fmt.Errorf("failure_threshold_pct must be greater than zero and at most 100")
	}
	if p.MinSamples < 1 || p.MinSamples > EventCircuitMaxSamples {
		return fmt.Errorf("min_samples must be between 1 and %d", EventCircuitMaxSamples)
	}
	if p.WindowSeconds < 1 || p.WindowSeconds > EventCircuitMaxSeconds || p.CooldownSeconds < 1 || p.CooldownSeconds > EventCircuitMaxSeconds || p.RecoverySeconds < 1 || p.RecoverySeconds > EventCircuitMaxSeconds {
		return fmt.Errorf("window, cooldown and recovery seconds must be between 1 and %d", EventCircuitMaxSeconds)
	}
	if p.ProbeSuccesses < 1 || p.ProbeSuccesses > EventCircuitMaxProbeSuccesses || p.RecoveryMaxRate < 1 || p.RecoveryMaxRate > EventSubscriptionDrainRateMax {
		return fmt.Errorf("probe_successes must be 1..%d and recovery_max_rate_per_second 1..%d", EventCircuitMaxProbeSuccesses, EventSubscriptionDrainRateMax)
	}
	return nil
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

func eventCircuitPath(app, id string) string {
	return "/v1/apps/" + url.PathEscape(app) + "/event-subscriptions/" + url.PathEscape(id) + "/circuit-breaker"
}
func (c *Client) GetEventCircuitBreaker(ctx context.Context, app, id string) (EventCircuitBreakerResponse, error) {
	var out EventCircuitBreakerResponse
	err := c.do(ctx, http.MethodGet, eventCircuitPath(app, id), nil, &out)
	return out, err
}
func (c *Client) SetEventCircuitBreaker(ctx context.Context, app, id string, p EventCircuitBreakerPolicy) (EventCircuitBreakerResponse, error) {
	var out EventCircuitBreakerResponse
	err := c.do(ctx, http.MethodPut, eventCircuitPath(app, id), p, &out)
	return out, err
}
func (c *Client) DisableEventCircuitBreaker(ctx context.Context, app, id string) (EventCircuitBreakerResponse, error) {
	var out EventCircuitBreakerResponse
	err := c.do(ctx, http.MethodDelete, eventCircuitPath(app, id), nil, &out)
	return out, err
}
func (c *Client) ResetEventCircuitBreaker(ctx context.Context, app, id string) (EventCircuitBreakerResponse, error) {
	var out EventCircuitBreakerResponse
	err := c.do(ctx, http.MethodPost, eventCircuitPath(app, id)+"/reset", nil, &out)
	return out, err
}
