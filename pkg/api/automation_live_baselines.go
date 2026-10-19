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

// Baseline names identify release tracks within an app and automation. They do
// not change execution routing or grant access to another environment.
func ValidAutomationLiveBaselineName(name string) bool {
	if name == "" || len(name) > AutomationLiveBaselineNameMaxBytes || strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
		return false
	}
	return name[0] != '-' && name[len(name)-1] != '-'
}

type PromoteAutomationLiveBaselineRequest struct {
	SuiteID                  string          `json:"suite_id"`
	ExpectedRevision         int64           `json:"expected_revision"`
	ExpectedVersion          int64           `json:"expected_version"`
	ExpectedPublishedVersion int64           `json:"expected_published_version"`
	ExpectedDeploymentID     string          `json:"expected_deployment_id"`
	Policy                   json.RawMessage `json:"policy"`
}

// AutomationLiveBaseline is an immutable promotion receipt. Reading it does not
// certify that its linked evidence is still retained or valid today.
type AutomationLiveBaseline struct {
	PromotionID         string                     `json:"promotion_id"`
	AppID               string                     `json:"app_id"`
	Name                string                     `json:"name"`
	BaselineName        string                     `json:"baseline_name"`
	Revision            int64                      `json:"revision"`
	PreviousPromotionID string                     `json:"previous_promotion_id,omitempty"`
	ActorAccountID      string                     `json:"actor_account_id"`
	ActorAPIKeyID       string                     `json:"actor_api_key_id,omitempty"`
	PromotedAt          time.Time                  `json:"promoted_at"`
	Suite               AutomationLiveSuiteSummary `json:"suite"`
	Policy              json.RawMessage            `json:"policy"`
	PolicyHash          string                     `json:"policy_hash"`
	CasesPassed         int                        `json:"cases_passed"`
	OldestRunCreatedAt  time.Time                  `json:"oldest_run_created_at"`
	EvidenceAgeSeconds  float64                    `json:"evidence_age_seconds"`
}

type AutomationLiveBaselineHistory struct {
	Promotions []AutomationLiveBaseline          `json:"promotions"`
	Retirement *AutomationLiveBaselineRetirement `json:"retirement,omitempty"`
}

func automationLiveBaselinePath(slug, name, baseline string) string {
	return automationPath(slug) + "/" + url.PathEscape(name) + "/live-baselines/" + url.PathEscape(baseline)
}
func (c *Client) GetAutomationLiveBaseline(ctx context.Context, slug, name, baseline string) (AutomationLiveBaseline, error) {
	var out AutomationLiveBaseline
	err := c.do(ctx, http.MethodGet, automationLiveBaselinePath(slug, name, baseline), nil, &out)
	return out, err
}
func (c *Client) GetAutomationLiveBaselineHistory(ctx context.Context, slug, name, baseline string) (AutomationLiveBaselineHistory, error) {
	var out AutomationLiveBaselineHistory
	err := c.do(ctx, http.MethodGet, automationLiveBaselinePath(slug, name, baseline)+"/history", nil, &out)
	return out, err
}
func (c *Client) PromoteAutomationLiveBaseline(ctx context.Context, slug, name, baseline, promotionID string, body PromoteAutomationLiveBaselineRequest) (AutomationLiveBaseline, error) {
	var out AutomationLiveBaseline
	err := c.do(ctx, http.MethodPut, automationLiveBaselinePath(slug, name, baseline)+"/promotions/"+url.PathEscape(promotionID), body, &out)
	return out, err
}
