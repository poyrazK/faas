package api

import (
	"context"
	"encoding/json"
	"net/url"
	"time"
)

// Policy applies to dashboard publication. YAML deployment remains a separate surface.
type StoredWorkflowPublishPolicy struct {
	Mode    string `json:"mode"`
	Version int64  `json:"version"`
}
type SetStoredWorkflowPublishPolicyRequest struct {
	Mode            string `json:"mode"`
	ExpectedVersion int64  `json:"expected_version"`
}
type StoredWorkflowCheckExpectation struct {
	Step         string                        `json:"step"`
	Loop         string                        `json:"loop,omitempty"`
	ItemIndex    *int                          `json:"item_index,omitempty"`
	State        string                        `json:"state,omitempty"`
	Reason       *string                       `json:"reason,omitempty"`
	WhenMatched  *bool                         `json:"when_matched,omitempty"`
	Output       json.RawMessage               `json:"output,omitempty"`
	AttemptCount *int                          `json:"attempt_count,omitempty"`
	Attempts     *[]StoredWorkflowCheckAttempt `json:"attempts,omitempty"`
}
type StoredWorkflowCheckAttempt struct {
	Outcome    string `json:"outcome"`
	HTTPStatus *int   `json:"http_status,omitempty"`
}
type StoredWorkflowPublishCheckScenario struct {
	Name         string                           `json:"name"`
	Simulation   SimulateStoredWorkflowRequest    `json:"simulation"`
	Expectations []StoredWorkflowCheckExpectation `json:"expectations"`
	Live         *StoredWorkflowLiveTest          `json:"live,omitempty"`
}
type CheckStoredWorkflowPublicationRequest struct {
	ExpectedVersion int64                                `json:"expected_version"`
	RequireCoverage bool                                 `json:"require_coverage,omitempty"`
	Scenarios       []StoredWorkflowPublishCheckScenario `json:"scenarios"`
	Exclusions      []StoredWorkflowCheckExclusion       `json:"exclusions"`
}
type CheckStoredWorkflowPublicationResponse struct {
	Receipt   string                      `json:"receipt"`
	ExpiresAt time.Time                   `json:"expires_at"`
	Evidence  StoredWorkflowCheckEvidence `json:"evidence"`
}

func (c *Client) GetStoredWorkflowPublishPolicy(ctx context.Context, slug string) (StoredWorkflowPublishPolicy, error) {
	var out StoredWorkflowPublishPolicy
	err := c.do(ctx, "GET", storedWorkflowPath(slug)+":publish-policy", nil, &out)
	return out, err
}
func (c *Client) SetStoredWorkflowPublishPolicy(ctx context.Context, slug string, body SetStoredWorkflowPublishPolicyRequest) (StoredWorkflowPublishPolicy, error) {
	var out StoredWorkflowPublishPolicy
	err := c.do(ctx, "PUT", storedWorkflowPath(slug)+":publish-policy", body, &out)
	return out, err
}
func (c *Client) CheckStoredWorkflowPublication(ctx context.Context, slug, name string, body CheckStoredWorkflowPublicationRequest) (CheckStoredWorkflowPublicationResponse, error) {
	var out CheckStoredWorkflowPublicationResponse
	err := c.do(ctx, "POST", storedWorkflowPath(slug)+"/"+url.PathEscape(name)+"/publish-check", body, &out)
	return out, err
}
