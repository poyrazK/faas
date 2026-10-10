package api

import (
	"context"
	"time"
)

// CreateAPIConsumerPlanRequest creates a named consumer plan (ADR-938).
// Zero limits mean unlimited. Prices are the plan's own rate cards
// (CreateAPIConsumerRateCardRequest.PlanID).
type CreateAPIConsumerPlanRequest struct {
	Name                 string `json:"name"`
	MaxRequestsPerMinute int64  `json:"max_requests_per_minute,omitempty"`
	MaxUnitsPerMonth     int64  `json:"max_units_per_month,omitempty"`
	// AlertThresholdsPercent are percentages of MaxUnitsPerMonth at which a
	// consumer.usage_threshold webhook fires (ADR-849), at most 5.
	AlertThresholdsPercent []int32 `json:"alert_thresholds_percent,omitempty"`
}

// UpdateAPIConsumerPlanLimitsRequest replaces a plan's limits. Limits are
// enforcement, not prices, so they change in place; prices change through
// new rate-card versions.
//
// AlertThresholdsPercent is optional: omitted keeps the plan's thresholds,
// an empty list clears them.
type UpdateAPIConsumerPlanLimitsRequest struct {
	MaxRequestsPerMinute   int64    `json:"max_requests_per_minute"`
	MaxUnitsPerMonth       int64    `json:"max_units_per_month"`
	AlertThresholdsPercent *[]int32 `json:"alert_thresholds_percent,omitempty"`
}

// APIConsumerPlanResponse is one consumer plan.
type APIConsumerPlanResponse struct {
	ID                   string `json:"id"`
	AppID                string `json:"app_id"`
	Name                 string `json:"name"`
	MaxRequestsPerMinute int64  `json:"max_requests_per_minute"`
	MaxUnitsPerMonth     int64  `json:"max_units_per_month"`
	// AlertThresholdsPercent lists the plan's usage alert thresholds,
	// ascending; empty means no alerts.
	AlertThresholdsPercent []int32   `json:"alert_thresholds_percent"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

// APIConsumerPlanListResponse lists an app's plans by name.
type APIConsumerPlanListResponse struct {
	Plans []APIConsumerPlanResponse `json:"plans"`
}

// AssignAPIConsumerPlanRequest moves a consumer onto a plan from a UTC
// minute (default: the next minute; never in the past). An empty plan_id
// returns the consumer to the app's default plan.
type AssignAPIConsumerPlanRequest struct {
	PlanID        string     `json:"plan_id,omitempty"`
	EffectiveFrom *time.Time `json:"effective_from,omitempty"`
}

// APIConsumerPlanAssignmentResponse is one append-only plan assignment; an
// empty plan_id is the default plan.
type APIConsumerPlanAssignmentResponse struct {
	ID            string    `json:"id"`
	ConsumerID    string    `json:"consumer_id"`
	PlanID        string    `json:"plan_id,omitempty"`
	EffectiveFrom time.Time `json:"effective_from"`
	CreatedAt     time.Time `json:"created_at"`
}

// APIConsumerPlanAssignmentListResponse lists a consumer's assignments,
// oldest first.
type APIConsumerPlanAssignmentListResponse struct {
	Assignments []APIConsumerPlanAssignmentResponse `json:"assignments"`
}

// Error codes for plan-limited admission at the gateway (ADR-938).
const (
	CodeConsumerPlanLimitExceeded    = "consumer_plan_limit_exceeded"
	CodeConsumerPlanLimitUnavailable = "consumer_plan_limit_unavailable"
)

// ListAPIConsumerPlans returns an app's consumer plans.
func (c *Client) ListAPIConsumerPlans(ctx context.Context, slug string) (APIConsumerPlanListResponse, error) {
	var out APIConsumerPlanListResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/consumer-plans", nil, &out)
}

// CreateAPIConsumerPlan creates a named consumer plan.
func (c *Client) CreateAPIConsumerPlan(ctx context.Context, slug string, req CreateAPIConsumerPlanRequest) (APIConsumerPlanResponse, error) {
	var out APIConsumerPlanResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/consumer-plans", req, &out)
}

// UpdateAPIConsumerPlanLimits replaces a plan's limits.
func (c *Client) UpdateAPIConsumerPlanLimits(ctx context.Context, slug, planID string, req UpdateAPIConsumerPlanLimitsRequest) (APIConsumerPlanResponse, error) {
	var out APIConsumerPlanResponse
	return out, c.do(ctx, "PUT", "/v1/apps/"+slug+"/consumer-plans/"+planID, req, &out)
}

// ListAPIConsumerPlanAssignments returns a consumer's plan history.
func (c *Client) ListAPIConsumerPlanAssignments(ctx context.Context, slug, consumerID string) (APIConsumerPlanAssignmentListResponse, error) {
	var out APIConsumerPlanAssignmentListResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/consumers/"+consumerID+"/plan-assignments", nil, &out)
}

// AssignAPIConsumerPlan moves a consumer onto a plan from a UTC minute.
func (c *Client) AssignAPIConsumerPlan(ctx context.Context, slug, consumerID string, req AssignAPIConsumerPlanRequest) (APIConsumerPlanAssignmentResponse, error) {
	var out APIConsumerPlanAssignmentResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/consumers/"+consumerID+"/plan-assignments", req, &out)
}

// APIConsumerUsageAlertResponse is one plan alert threshold a consumer
// crossed in one UTC month (ADR-849). UsedUnits is the consumer's weighted
// monthly usage when the threshold was crossed.
type APIConsumerUsageAlertResponse struct {
	ID               string    `json:"id"`
	ConsumerID       string    `json:"consumer_id"`
	PlanID           string    `json:"plan_id"`
	MonthStart       time.Time `json:"month_start"`
	ThresholdPercent int32     `json:"threshold_percent"`
	LimitUnits       int64     `json:"limit_units"`
	UsedUnits        int64     `json:"used_units"`
	CrossedAt        time.Time `json:"crossed_at"`
}

// APIConsumerUsageAlertListResponse lists a consumer's usage alerts, newest
// first.
type APIConsumerUsageAlertListResponse struct {
	Alerts []APIConsumerUsageAlertResponse `json:"alerts"`
}

// APIConsumerUsageThresholdWebhookPayload is the data of a
// consumer.usage_threshold webhook. AlertID is stable across retries.
type APIConsumerUsageThresholdWebhookPayload struct {
	AlertID          string    `json:"alert_id"`
	AppID            string    `json:"app_id"`
	ConsumerID       string    `json:"consumer_id"`
	ExternalRef      string    `json:"external_ref"`
	PlanID           string    `json:"plan_id"`
	ThresholdPercent int32     `json:"threshold_percent"`
	LimitUnits       int64     `json:"limit_units"`
	UsedUnits        int64     `json:"used_units"`
	MonthStart       time.Time `json:"month_start"`
	CrossedAt        time.Time `json:"crossed_at"`
}

// ListAPIConsumerUsageAlerts returns a consumer's recent usage alerts.
func (c *Client) ListAPIConsumerUsageAlerts(ctx context.Context, slug, consumerID string) (APIConsumerUsageAlertListResponse, error) {
	var out APIConsumerUsageAlertListResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/consumers/"+consumerID+"/usage-alerts", nil, &out)
}
