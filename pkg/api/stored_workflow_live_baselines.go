// adr: 1302
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Baseline names identify release tracks within an app and stored workflow. They do
// not change execution routing or grant access to another environment.
func ValidStoredWorkflowLiveBaselineName(name string) bool {
	if name == "" || len(name) > StoredWorkflowLiveBaselineNameMaxBytes || strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
		return false
	}
	return name[0] != '-' && name[len(name)-1] != '-'
}

type PromoteStoredWorkflowLiveBaselineRequest struct {
	SuiteID                  string          `json:"suite_id"`
	ExpectedRevision         int64           `json:"expected_revision"`
	ExpectedVersion          int64           `json:"expected_version"`
	ExpectedPublishedVersion int64           `json:"expected_published_version"`
	ExpectedDeploymentID     string          `json:"expected_deployment_id"`
	Policy                   json.RawMessage `json:"policy"`
}

// StoredWorkflowLiveBaseline is an immutable promotion receipt. Reading it does not
// certify that its linked evidence is still retained or valid today.
type StoredWorkflowLiveBaseline struct {
	PromotionID         string                         `json:"promotion_id"`
	AppID               string                         `json:"app_id"`
	Name                string                         `json:"name"`
	BaselineName        string                         `json:"baseline_name"`
	Revision            int64                          `json:"revision"`
	PreviousPromotionID string                         `json:"previous_promotion_id,omitempty"`
	ActorAccountID      string                         `json:"actor_account_id"`
	ActorAPIKeyID       string                         `json:"actor_api_key_id,omitempty"`
	PromotedAt          time.Time                      `json:"promoted_at"`
	Suite               StoredWorkflowLiveSuiteSummary `json:"suite"`
	Policy              json.RawMessage                `json:"policy"`
	PolicyHash          string                         `json:"policy_hash"`
	CasesPassed         int                            `json:"cases_passed"`
	OldestRunCreatedAt  time.Time                      `json:"oldest_run_created_at"`
	EvidenceAgeSeconds  float64                        `json:"evidence_age_seconds"`
}

type StoredWorkflowLiveBaselineHistory struct {
	Promotions []StoredWorkflowLiveBaseline          `json:"promotions"`
	Retirement *StoredWorkflowLiveBaselineRetirement `json:"retirement,omitempty"`
}

func storedWorkflowLiveBaselinePath(slug, name, baseline string) string {
	return storedWorkflowPath(slug) + "/" + url.PathEscape(name) + "/live-baselines/" + url.PathEscape(baseline)
}
func (c *Client) GetStoredWorkflowLiveBaseline(ctx context.Context, slug, name, baseline string) (StoredWorkflowLiveBaseline, error) {
	var out StoredWorkflowLiveBaseline
	err := c.do(ctx, http.MethodGet, storedWorkflowLiveBaselinePath(slug, name, baseline), nil, &out)
	return out, err
}
func (c *Client) GetStoredWorkflowLiveBaselineHistory(ctx context.Context, slug, name, baseline string) (StoredWorkflowLiveBaselineHistory, error) {
	var out StoredWorkflowLiveBaselineHistory
	err := c.do(ctx, http.MethodGet, storedWorkflowLiveBaselinePath(slug, name, baseline)+"/history", nil, &out)
	return out, err
}
func (c *Client) PromoteStoredWorkflowLiveBaseline(ctx context.Context, slug, name, baseline, promotionID string, body PromoteStoredWorkflowLiveBaselineRequest) (StoredWorkflowLiveBaseline, error) {
	var out StoredWorkflowLiveBaseline
	err := c.do(ctx, http.MethodPut, storedWorkflowLiveBaselinePath(slug, name, baseline)+"/promotions/"+url.PathEscape(promotionID), body, &out)
	return out, err
}
