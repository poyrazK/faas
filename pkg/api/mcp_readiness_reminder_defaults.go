package api

import (
	"context"
	"errors"
	"math"
	"net/url"
)

// Defaults are captured only when an incident opens. Version zero is unconfigured.
type MCPReadinessReminderDefaults struct {
	Version        int64                      `json:"version"`
	ReminderPolicy MCPReadinessReminderPolicy `json:"reminder_policy"`
}
type SetMCPReadinessReminderDefaultsRequest struct {
	ExpectedVersion *int64                      `json:"expected_version"`
	ReminderPolicy  *MCPReadinessReminderPolicy `json:"reminder_policy"`
}

func ValidateMCPReadinessReminderPolicy(p MCPReadinessReminderPolicy) error {
	if p.IntervalSeconds == 0 && p.MaxReminders == 0 {
		return nil
	}
	if p.IntervalSeconds < MCPReadinessReminderMinIntervalSeconds || p.IntervalSeconds > MCPReadinessReminderMaxIntervalSeconds || p.MaxReminders < 1 || p.MaxReminders > MCPReadinessReminderMaxCount {
		return errors.New("use a 60–86400 second interval and 1–10 reminders, or zero for both to disable")
	}
	return nil
}
func ValidateMCPReadinessReminderDefaultsRequest(r SetMCPReadinessReminderDefaultsRequest) error {
	if r.ExpectedVersion == nil || *r.ExpectedVersion < 0 || *r.ExpectedVersion == math.MaxInt64 || r.ReminderPolicy == nil {
		return errors.New("supply a current expected_version and a reminder_policy")
	}
	return ValidateMCPReadinessReminderPolicy(*r.ReminderPolicy)
}
func (c *Client) GetMCPReadinessReminderDefaults(ctx context.Context, app, rule string) (MCPReadinessReminderDefaults, error) {
	var out MCPReadinessReminderDefaults
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(app)+"/alerts/"+url.PathEscape(rule)+"/mcp-readiness-reminder-defaults", nil, &out)
	return out, err
}
func (c *Client) SetMCPReadinessReminderDefaults(ctx context.Context, app, rule string, request SetMCPReadinessReminderDefaultsRequest) (MCPReadinessReminderDefaults, error) {
	var out MCPReadinessReminderDefaults
	err := c.do(ctx, "PUT", "/v1/apps/"+url.PathEscape(app)+"/alerts/"+url.PathEscape(rule)+"/mcp-readiness-reminder-defaults", request, &out)
	return out, err
}
