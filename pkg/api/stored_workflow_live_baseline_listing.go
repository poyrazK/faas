// adr: 1304
package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Baseline health is an observation, not a CI gate verdict. Age and target
// freshness constrain promotion/candidate evidence, not historical baselines.
type StoredWorkflowLiveBaselineCaseHealth struct {
	Name   string `json:"name"`
	RunID  string `json:"run_id,omitempty"`
	Status string `json:"status"`
}
type StoredWorkflowLiveBaselineHealth struct {
	Baseline           StoredWorkflowLiveBaseline             `json:"baseline"`
	EvidenceStatus     string                                 `json:"evidence_status"`
	RenewalStatus      string                                 `json:"renewal_status"`
	Reasons            []string                               `json:"reasons"`
	Outdated           bool                                   `json:"outdated"`
	Cases              []StoredWorkflowLiveBaselineCaseHealth `json:"cases"`
	AgeSeconds         *float64                               `json:"age_seconds,omitempty"`
	OldestRunCreatedAt *time.Time                             `json:"oldest_run_created_at,omitempty"`
	MaxAgeSeconds      *int64                                 `json:"max_age_seconds,omitempty"`
}
type ListStoredWorkflowLiveBaselinesResponse struct {
	Baselines  []StoredWorkflowLiveBaselineHealth `json:"baselines"`
	ObservedAt time.Time                          `json:"observed_at"`
}

func (c *Client) ListStoredWorkflowLiveBaselines(ctx context.Context, slug, name string) (ListStoredWorkflowLiveBaselinesResponse, error) {
	var out ListStoredWorkflowLiveBaselinesResponse
	err := c.do(ctx, http.MethodGet, storedWorkflowPath(slug)+"/"+url.PathEscape(name)+"/live-baselines", nil, &out)
	return out, err
}
