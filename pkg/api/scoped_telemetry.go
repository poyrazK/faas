package api

import (
	"context"
	"net/url"
	"time"
)

func telemetryScopeQuery(environment string) (string, error) {
	if environment == "" {
		return "?scope=default", nil
	}
	if !ValidProjectEnvironmentSlug(environment) {
		return "", ErrValidation("invalid project environment")
	}
	return "?environment=" + url.QueryEscape(environment), nil
}
func (c *Client) GetScopedAppRoutes(ctx context.Context, slug, environment string) (AppRoutesResponse, error) {
	var out AppRoutesResponse
	query, err := telemetryScopeQuery(environment)
	if err != nil {
		return out, err
	}
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/routes"+query, nil, &out)
}
func (c *Client) GetScopedOperationalSummary(ctx context.Context, slug, environment string) (AppOperationalSummary, error) {
	var out AppOperationalSummary
	query, err := telemetryScopeQuery(environment)
	if err != nil {
		return out, err
	}
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/operational-summary"+query, nil, &out)
}

type WorkerRolloutObservation struct {
	ProjectID                string    `json:"project_id,omitempty"`
	EnvironmentID            string    `json:"environment_id,omitempty"`
	AppID                    string    `json:"app_id"`
	DeploymentID             string    `json:"deployment_id"`
	Scope                    string    `json:"scope"`
	CheckedAt                time.Time `json:"checked_at"`
	Basis                    string    `json:"basis"`
	SelectedDeploymentID     string    `json:"selected_deployment_id,omitempty"`
	Running                  int64     `json:"running"`
	Starting                 int64     `json:"starting"`
	Draining                 int64     `json:"draining"`
	OtherResidents           int64     `json:"other_residents"`
	RetiredResidents         int64     `json:"retired_residents"`
	GenerationConverged      bool      `json:"generation_converged"`
	QueueProcessingConfirmed bool      `json:"queue_processing_confirmed"`
}

func (c *Client) GetWorkerRolloutObservation(ctx context.Context, slug, id, environment string) (WorkerRolloutObservation, error) {
	var out WorkerRolloutObservation
	query, err := telemetryScopeQuery(environment)
	if err != nil {
		return out, err
	}
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/rollouts/"+url.PathEscape(id)+"/observation"+query, nil, &out)
}

// ScopedRouteBudgetReport uses inherited or overridden budgets for one scoped
// serving release. It does not enable scoped incident or rollback actions.
type ScopedRouteBudgetReport struct {
	Scope             string             `json:"scope"`
	ProjectID         string             `json:"project_id,omitempty"`
	EnvironmentID     string             `json:"environment_id,omitempty"`
	BudgetRevision    int64              `json:"budget_revision,omitempty"`
	AppBudgetRevision int64              `json:"app_budget_revision,omitempty"`
	BudgetSource      string             `json:"budget_source"`
	Report            RouteMonitorReport `json:"report"`
}
