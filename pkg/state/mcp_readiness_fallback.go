package state

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type MCPReadinessFallbackConfig struct {
	Provider             string
	Region               string
	IntegrationKeySealed []byte
	Fingerprint          string
	PrimaryPagerDuty     MCPReadinessPagerDutyConfig
	Version              int64
	Enabled              bool
	WebhookURL           string
	WebhookSecretSealed  []byte
	AfterFailures        int
}
type MCPReadinessFallbackStore interface {
	GetMCPReadinessFallback(context.Context, string, string, string) (api.MCPReadinessFallbackPolicy, error)
	SetMCPReadinessFallback(context.Context, string, string, string, int64, MCPReadinessFallbackConfig) (api.MCPReadinessFallbackPolicy, error)
}
type readinessDestination struct {
	Provider   string `json:"provider,omitempty"`
	ReceiverID string `json:"receiver_id,omitempty"`
	URL        string `json:"url"`
	Secret     []byte `json:"secret"`
}
type readinessIncidentRouting struct {
	PagerDutyVersion int64                `json:"pagerduty_version,omitempty"`
	Initialized      bool                 `json:"initialized"`
	PolicyVersion    int64                `json:"policy_version"`
	Primary          readinessDestination `json:"primary"`
	Fallback         readinessDestination `json:"fallback"`
	AfterFailures    int                  `json:"after_failures"`
	Activated        bool                 `json:"activated"`
	QueueBlocked     bool                 `json:"queue_blocked"`
	PrimaryInformed  bool                 `json:"primary_informed"`
}

func readinessFallbackView(c MCPReadinessFallbackConfig) api.MCPReadinessFallbackPolicy {
	p := api.MCPReadinessFallbackPolicy{Version: c.Version, Enabled: c.Enabled, WebhookURL: c.WebhookURL, AfterFailures: c.AfterFailures}
	if c.Enabled {
		p.Provider = readinessFallbackProvider(c)
		if p.Provider == "pagerduty" {
			p.Region, p.IntegrationKeyMasked = readinessPagerDutyRegion(fallbackPagerDuty(c)), "***"
		} else {
			p.WebhookSecretSealedMasked = api.AlertRuleWebhookSecretMasked
		}
	}
	return p
}
func validateReadinessFallbackConfig(c MCPReadinessFallbackConfig, expected int64) error {
	if expected < 0 || expected == math.MaxInt64 {
		return ErrInvalidArgument
	}
	if !c.Enabled {
		if c.Provider != "" || c.Region != "" || c.Fingerprint != "" || len(c.IntegrationKeySealed) != 0 || c.WebhookURL != "" || len(c.WebhookSecretSealed) != 0 || c.AfterFailures != 0 {
			return ErrInvalidArgument
		}
		return nil
	}
	if c.AfterFailures < 1 || c.AfterFailures > api.MCPReadinessFallbackMaxFailures {
		return ErrInvalidArgument
	}
	if c.Provider == "pagerduty" {
		if c.WebhookURL != "" || len(c.WebhookSecretSealed) != 0 {
			return ErrInvalidArgument
		}
		return validateReadinessPagerDutyConfig(expected, fallbackPagerDuty(c))
	}
	if c.Provider != "" && c.Provider != "webhook" || c.Region != "" || c.Fingerprint != "" || len(c.IntegrationKeySealed) != 0 {
		return ErrInvalidArgument
	}
	// Use the same URL/threshold validation as the public request without exposing
	// ciphertext to the API validator or depending on plaintext in the state layer.
	v := expected
	if api.ValidateMCPReadinessFallbackRequest(api.SetMCPReadinessFallbackRequest{ExpectedVersion: &v, Enabled: &c.Enabled, WebhookURL: c.WebhookURL, WebhookSecret: "sealed", AfterFailures: c.AfterFailures}) != nil || len(c.WebhookSecretSealed) == 0 {
		return ErrInvalidArgument
	}
	return nil
}
func readinessWebhookRule(rule AlertRule, account, app string) error {
	if rule.AccountID != account || rule.AppID != app {
		return ErrNotFound
	}
	if string(rule.Metric) != api.MetricMCPReadinessUnhealthy || rule.Action != AlertActionWebhook {
		return ErrInvalidArgument
	}
	return nil
}
func initializeReadinessRoute(rule AlertRule, policy MCPReadinessFallbackConfig, route *readinessIncidentRouting, event readinessIncidentEventRecord, blocked bool) {
	if !route.Initialized && event.Attempts > 0 {
		route.Initialized = true
	}
	if !route.Initialized {
		route.Initialized = true
		route.PolicyVersion = policy.Version
		route.PagerDutyVersion = policy.PrimaryPagerDuty.Version
		if policy.Enabled {
			route.Primary = readinessDestination{URL: rule.WebhookURL, Secret: append([]byte(nil), rule.WebhookSecretSealed...)}
			route.Fallback = readinessFallbackDestination(policy)
			route.AfterFailures = policy.AfterFailures
			route.Activated, route.QueueBlocked = blocked, blocked
		}
		if p := policy.PrimaryPagerDuty; p.Enabled {
			endpoint, _ := api.MCPReadinessPagerDutyEndpointForRegion(p.Region)
			route.Primary = readinessDestination{Provider: "pagerduty", URL: endpoint, Secret: append([]byte(nil), p.IntegrationKeySealed...), ReceiverID: p.Fingerprint}
		}
	}
}
func selectReadinessDestination(rule AlertRule, policy MCPReadinessFallbackConfig, route *readinessIncidentRouting, event *readinessIncidentEventRecord, destination string) AlertRule {
	reason := ""
	if destination == "fallback" {
		reason = "incident_sticky_fallback"
		if route.QueueBlocked {
			reason = "primary_queue_blocked"
		} else if event.Routing.FallbackAttempts == 0 && event.Routing.PrimaryFailures >= route.AfterFailures {
			reason = "primary_failure_threshold"
		}
	} else if route.Activated && event.Kind == "alert.resolved" {
		reason = "recovery_to_primary"
	}
	target := route.Primary
	if destination == "fallback" {
		target = route.Fallback
	}
	if destination == "primary" && target.Provider != "pagerduty" && (target.URL == "" || target.URL == rule.WebhookURL) {
		target = readinessDestination{URL: rule.WebhookURL, Secret: rule.WebhookSecretSealed}
	}
	endpoint, _ := api.MCPReadinessPagerDutyEndpointForRegion(policy.PrimaryPagerDuty.Region)
	if destination == "primary" && target.Provider == "pagerduty" && policy.PrimaryPagerDuty.Enabled && target.URL == endpoint && target.ReceiverID == policy.PrimaryPagerDuty.Fingerprint {
		target.Secret = policy.PrimaryPagerDuty.IntegrationKeySealed
	}
	currentFallback := readinessFallbackDestination(policy)
	if destination == "fallback" && policy.Enabled && readinessReceiverIdentity(target) == readinessReceiverIdentity(currentFallback) {
		target.Secret = currentFallback.Secret
	}
	if destination == "fallback" {
		route.Fallback.Secret = append([]byte(nil), target.Secret...)
	} else if route.Primary.URL != "" {
		route.Primary.Secret = append([]byte(nil), target.Secret...)
	}
	rule.WebhookURL = target.URL
	rule.WebhookSecretSealed = append([]byte(nil), target.Secret...)
	event.Routing.PolicyVersion = route.PolicyVersion
	event.Routing.PagerDutyVersion = route.PagerDutyVersion
	event.Routing.LastDestination = destination
	event.Routing.FailoverReason = reason
	if destination == "fallback" {
		event.Routing.FallbackAttempts++
	} else {
		event.Routing.PrimaryAttempts++
	}
	return rule
}
func readinessRouteDelivery(rule AlertRule, event readinessIncidentEventRecord, now time.Time) AlertDelivery {
	return AlertDelivery{ID: uuid.NewString(), RuleID: rule.ID, AccountID: rule.AccountID, AppID: rule.AppID, IdempotencyKey: fmt.Sprintf("%s:readiness-outbox:%s:%d", rule.ID, event.ID, event.Attempts), Payload: append([]byte(nil), event.Payload...), Status: AlertDeliveryPending, ObservedValue: event.Observed, FiredAt: now, ReadinessEventID: event.ID, ReadinessDestination: event.Routing.LastDestination, ReadinessFailoverReason: event.Routing.FailoverReason}
}
func (m *MemStore) GetMCPReadinessFallback(_ context.Context, account, app, id string) (api.MCPReadinessFallbackPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.readinessAppLocked(account, app) {
		return api.MCPReadinessFallbackPolicy{}, ErrNotFound
	}
	rule, ok := m.alertRules[id]
	if !ok {
		return api.MCPReadinessFallbackPolicy{}, ErrNotFound
	}
	if err := readinessWebhookRule(rule, account, app); err != nil {
		return api.MCPReadinessFallbackPolicy{}, err
	}
	return readinessFallbackView(m.readinessFallbackPolicies[id]), nil
}
func (m *MemStore) SetMCPReadinessFallback(_ context.Context, account, app, id string, expected int64, c MCPReadinessFallbackConfig) (api.MCPReadinessFallbackPolicy, error) {
	if err := validateReadinessFallbackConfig(c, expected); err != nil {
		return api.MCPReadinessFallbackPolicy{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.readinessAppLocked(account, app) {
		return api.MCPReadinessFallbackPolicy{}, ErrNotFound
	}
	rule, ok := m.alertRules[id]
	if !ok {
		return api.MCPReadinessFallbackPolicy{}, ErrNotFound
	}
	if err := readinessWebhookRule(rule, account, app); err != nil {
		return api.MCPReadinessFallbackPolicy{}, err
	}
	if m.readinessFallbackPolicies[id].Version != expected {
		return api.MCPReadinessFallbackPolicy{}, ErrConflict
	}
	if c.Enabled && c.WebhookURL == rule.WebhookURL {
		return api.MCPReadinessFallbackPolicy{}, ErrInvalidArgument
	}
	if sameReadinessPagerDuty(m.readinessPagerDutyPolicies[id], c) {
		return api.MCPReadinessFallbackPolicy{}, ErrInvalidArgument
	}
	if c.Provider == "pagerduty" {
		c.Region = readinessPagerDutyRegion(fallbackPagerDuty(c))
	}
	c.Version = expected + 1
	c.WebhookSecretSealed = append([]byte(nil), c.WebhookSecretSealed...)
	c.IntegrationKeySealed = append([]byte(nil), c.IntegrationKeySealed...)
	m.readinessFallbackPolicies[id] = c
	return readinessFallbackView(c), nil
}
