package api

import (
	"context"
	"encoding/json"
	"net/url"
	"time"
)

// Policy applies to dashboard publication. YAML deployment remains a separate surface.
type AutomationPublishPolicy struct {
	Mode    string `json:"mode"`
	Version int64  `json:"version"`
}
type SetAutomationPublishPolicyRequest struct {
	Mode            string `json:"mode"`
	ExpectedVersion int64  `json:"expected_version"`
}
type AutomationCheckExpectation struct {
	Step         string                    `json:"step"`
	Loop         string                    `json:"loop,omitempty"`
	ItemIndex    *int                      `json:"item_index,omitempty"`
	State        string                    `json:"state,omitempty"`
	Reason       *string                   `json:"reason,omitempty"`
	WhenMatched  *bool                     `json:"when_matched,omitempty"`
	Output       json.RawMessage           `json:"output,omitempty"`
	AttemptCount *int                      `json:"attempt_count,omitempty"`
	Attempts     *[]AutomationCheckAttempt `json:"attempts,omitempty"`
}
type AutomationCheckAttempt struct {
	Outcome    string `json:"outcome"`
	HTTPStatus *int   `json:"http_status,omitempty"`
}
type AutomationPublishCheckScenario struct {
	Name         string                       `json:"name"`
	Simulation   SimulateAutomationRequest    `json:"simulation"`
	Expectations []AutomationCheckExpectation `json:"expectations"`
}
type CheckAutomationPublicationRequest struct {
	ExpectedVersion int64                            `json:"expected_version"`
	RequireCoverage bool                             `json:"require_coverage,omitempty"`
	Scenarios       []AutomationPublishCheckScenario `json:"scenarios"`
	Exclusions      []AutomationCheckExclusion       `json:"exclusions"`
}
type CheckAutomationPublicationResponse struct {
	Receipt   string                  `json:"receipt"`
	ExpiresAt time.Time               `json:"expires_at"`
	Evidence  AutomationCheckEvidence `json:"evidence"`
}

func (c *Client) GetAutomationPublishPolicy(ctx context.Context, slug string) (AutomationPublishPolicy, error) {
	var out AutomationPublishPolicy
	err := c.do(ctx, "GET", automationPath(slug)+":publish-policy", nil, &out)
	return out, err
}
func (c *Client) SetAutomationPublishPolicy(ctx context.Context, slug string, body SetAutomationPublishPolicyRequest) (AutomationPublishPolicy, error) {
	var out AutomationPublishPolicy
	err := c.do(ctx, "PUT", automationPath(slug)+":publish-policy", body, &out)
	return out, err
}
func (c *Client) CheckAutomationPublication(ctx context.Context, slug, name string, body CheckAutomationPublicationRequest) (CheckAutomationPublicationResponse, error) {
	var out CheckAutomationPublicationResponse
	err := c.do(ctx, "POST", automationPath(slug)+"/"+url.PathEscape(name)+"/publish-check", body, &out)
	return out, err
}
