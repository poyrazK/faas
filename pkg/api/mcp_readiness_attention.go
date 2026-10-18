package api

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// Zero interval and budget disable reminders; an enabled policy is finite.
type MCPReadinessReminderPolicy struct {
	IntervalSeconds int `json:"interval_seconds"`
	MaxReminders    int `json:"max_reminders"`
}
type MCPReadinessAcknowledgement struct {
	Source             string     `json:"source,omitempty"`
	ProviderEventID    string     `json:"provider_event_id,omitempty"`
	ProviderIncidentID string     `json:"provider_incident_id,omitempty"`
	ProviderOccurredAt *time.Time `json:"provider_occurred_at,omitempty"`
	RequestID          string     `json:"request_id"`
	CreatedBy          string     `json:"created_by"`
	CreatedAt          time.Time  `json:"created_at"`
	Reason             string     `json:"reason"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
}
type MCPReadinessAttentionAction struct {
	RequestID       string                      `json:"request_id"`
	ExpectedVersion *int64                      `json:"expected_version"`
	Action          string                      `json:"action"`
	Reason          string                      `json:"reason"`
	ExpiresAt       *time.Time                  `json:"expires_at,omitempty"`
	ReminderPolicy  *MCPReadinessReminderPolicy `json:"reminder_policy,omitempty"`
}
type MCPReadinessAttentionReceipt struct {
	Source             string                      `json:"source,omitempty"`
	ProviderEventID    string                      `json:"provider_event_id,omitempty"`
	ProviderIncidentID string                      `json:"provider_incident_id,omitempty"`
	ProviderOccurredAt *time.Time                  `json:"provider_occurred_at,omitempty"`
	RequestID          string                      `json:"request_id"`
	IncidentID         string                      `json:"incident_id"`
	Action             string                      `json:"action"`
	Reason             string                      `json:"reason"`
	CreatedBy          string                      `json:"created_by"`
	CreatedAt          time.Time                   `json:"created_at"`
	Version            int64                       `json:"version"`
	ExpiresAt          *time.Time                  `json:"expires_at,omitempty"`
	ReminderPolicy     *MCPReadinessReminderPolicy `json:"reminder_policy,omitempty"`
}
type MCPReadinessIncidentAttention struct {
	ProviderAcknowledgements []MCPReadinessPagerDutyAcknowledgementHold `json:"provider_acknowledgements,omitempty"`
	ReminderPolicySource     string                                     `json:"reminder_policy_source,omitempty"`
	ReminderDefaultsVersion  *int64                                     `json:"reminder_defaults_version,omitempty"`
	IncidentID               string                                     `json:"incident_id"`
	RuleID                   string                                     `json:"rule_id"`
	RuleName                 string                                     `json:"rule_name"`
	RuleEnabled              bool                                       `json:"rule_enabled"`
	ReadinessScope           string                                     `json:"mcp_readiness_scope,omitempty"`
	OpenedAt                 time.Time                                  `json:"opened_at"`
	ResolvedAt               *time.Time                                 `json:"resolved_at,omitempty"`
	Version                  int64                                      `json:"version"`
	State                    string                                     `json:"state"`
	Acknowledgement          *MCPReadinessAcknowledgement               `json:"acknowledgement,omitempty"`
	ReminderPolicy           MCPReadinessReminderPolicy                 `json:"reminder_policy"`
	RemindersCreated         int                                        `json:"reminders_created"`
	NextReminderAt           *time.Time                                 `json:"next_reminder_at,omitempty"`
	LastObservedAt           *time.Time                                 `json:"last_observed_at,omitempty"`
	LastUnhealthyAt          *time.Time                                 `json:"last_unhealthy_at,omitempty"`
	LastAction               *MCPReadinessAttentionReceipt              `json:"last_action,omitempty"`
}
type MCPReadinessAttentionPage struct {
	Incidents []MCPReadinessIncidentAttention `json:"incidents"`
	NextAfter string                          `json:"next_after,omitempty"`
}
type MCPReadinessAttentionHistory struct {
	Actions    []MCPReadinessAttentionReceipt `json:"actions"`
	NextBefore string                         `json:"next_before,omitempty"`
}

func ValidateMCPReadinessAttentionAction(r MCPReadinessAttentionAction) error {
	if uuid.Validate(r.RequestID) != nil || r.ExpectedVersion == nil || *r.ExpectedVersion < 0 || !ValidMCPMaintenanceReason(r.Reason) {
		return errors.New("supply a request UUID, current version and reason of 1–500 UTF-8 bytes")
	}
	switch r.Action {
	case "acknowledge":
		if r.ReminderPolicy != nil || r.ExpiresAt != nil && r.ExpiresAt.IsZero() {
			return errors.New("acknowledge accepts only an optional expiry")
		}
	case "release":
		if r.ReminderPolicy != nil || r.ExpiresAt != nil {
			return errors.New("release accepts no policy or expiry")
		}
	case "configure_reminders":
		if r.ExpiresAt != nil || r.ReminderPolicy == nil {
			return errors.New("configure_reminders requires a reminder policy")
		}
		if err := ValidateMCPReadinessReminderPolicy(*r.ReminderPolicy); err != nil {
			return err
		}
	default:
		return errors.New("use acknowledge, release or configure_reminders")
	}
	return nil
}
func (c *Client) ListMCPReadinessAttention(ctx context.Context, app, scope, after string, limit int, includeResolved bool) (MCPReadinessAttentionPage, error) {
	q := url.Values{}
	if includeResolved {
		q.Set("include_resolved", "true")
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	if after != "" {
		q.Set("after", after)
	}
	if limit != 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out MCPReadinessAttentionPage
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(app)+"/mcp-readiness-attention?"+q.Encode(), nil, &out)
	return out, err
}
func (c *Client) ActMCPReadinessAttention(ctx context.Context, app, incident string, r MCPReadinessAttentionAction) (MCPReadinessAttentionReceipt, error) {
	var out MCPReadinessAttentionReceipt
	err := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(app)+"/mcp-readiness-incidents/"+url.PathEscape(incident)+"/attention-actions", r, &out)
	return out, err
}
func (c *Client) ListMCPReadinessAttentionHistory(ctx context.Context, app, incident, before string, limit int) (MCPReadinessAttentionHistory, error) {
	q := url.Values{}
	if before != "" {
		q.Set("before", before)
	}
	if limit != 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out MCPReadinessAttentionHistory
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(app)+"/mcp-readiness-incidents/"+url.PathEscape(incident)+"/attention-actions?"+q.Encode(), nil, &out)
	return out, err
}
