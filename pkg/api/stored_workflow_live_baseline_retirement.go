// adr: 1305
package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

type RetireStoredWorkflowLiveBaselineRequest struct {
	ExpectedRevision int64 `json:"expected_revision"`
}

// Retirement reserves the name permanently and releases its evidence pin.
type StoredWorkflowLiveBaselineRetirement struct {
	RetirementID        string    `json:"retirement_id"`
	AppID               string    `json:"app_id"`
	Name                string    `json:"name"`
	BaselineName        string    `json:"baseline_name"`
	Revision            int64     `json:"revision"`
	PreviousPromotionID string    `json:"previous_promotion_id"`
	ActorAccountID      string    `json:"actor_account_id"`
	ActorAPIKeyID       string    `json:"actor_api_key_id,omitempty"`
	RetiredAt           time.Time `json:"retired_at"`
}

func (c *Client) RetireStoredWorkflowLiveBaseline(ctx context.Context, slug, name, baseline, retirementID string, body RetireStoredWorkflowLiveBaselineRequest) (StoredWorkflowLiveBaselineRetirement, error) {
	var out StoredWorkflowLiveBaselineRetirement
	err := c.do(ctx, http.MethodPut, storedWorkflowLiveBaselinePath(slug, name, baseline)+"/retirements/"+url.PathEscape(retirementID), body, &out)
	return out, err
}
