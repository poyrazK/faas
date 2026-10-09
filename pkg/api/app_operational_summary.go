package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// AppOperationalSummary joins read-only, independently observed operational
// facts. A deployment smoke receipt is deliberately not current health.
type AppOperationalSummary struct {
	Version         int                            `json:"version"`
	AppID           string                         `json:"app_id"`
	CheckedAt       time.Time                      `json:"checked_at"`
	Monitoring      AppOperationalMonitoring       `json:"monitoring"`
	Recovery        AppOperationalRecovery         `json:"recovery"`
	Recommendations []AppOperationalRecommendation `json:"recommendations"`
}

type AppOperationalMonitoring struct {
	Available          bool                    `json:"available"`
	Status             string                  `json:"status"`
	Reason             string                  `json:"reason"`
	Coverage           string                  `json:"coverage"`
	DeploymentID       string                  `json:"deployment_id,omitempty"`
	CheckedAt          *time.Time              `json:"checked_at,omitempty"`
	WindowStart        *time.Time              `json:"window_start,omitempty"`
	WindowEnd          *time.Time              `json:"window_end,omitempty"`
	IncidentsAvailable bool                    `json:"incidents_available"`
	Incident           *AppOperationalIncident `json:"incident,omitempty"`
}

// Incident is metadata only: customer identities and saved request evidence
// remain on the separately authorized production-monitor surfaces.
type AppOperationalIncident struct {
	ID           string    `json:"id"`
	DeploymentID string    `json:"deployment_id"`
	OpenedAt     time.Time `json:"opened_at"`
}

type AppOperationalRecovery struct {
	RollbacksAvailable bool                                 `json:"rollbacks_available"`
	RestartsAvailable  bool                                 `json:"restarts_available"`
	RollbacksTruncated bool                                 `json:"rollbacks_truncated"`
	RestartsTruncated  bool                                 `json:"restarts_truncated"`
	Rollbacks          []AppOperationalRollback             `json:"rollbacks"`
	Restarts           []RuntimeConfigRestartStatusResponse `json:"restarts"`
}

// Rollback omits free-form reasons and blocker details; stable codes are enough
// to lead the customer to the exact operation without leaking diagnostics.
type AppOperationalRollback struct {
	ID                  string    `json:"id"`
	Scope               string    `json:"scope"`
	Status              string    `json:"status"`
	Code                string    `json:"code,omitempty"`
	TargetDeploymentID  string    `json:"target_deployment_id"`
	CurrentDeploymentID string    `json:"current_deployment_id"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type AppOperationalRecommendation struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Next     string `json:"next"`
}

func (c *Client) GetAppOperationalSummary(ctx context.Context, slug string) (AppOperationalSummary, error) {
	var out AppOperationalSummary
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/operational-summary", nil, &out)
	return out, err
}
