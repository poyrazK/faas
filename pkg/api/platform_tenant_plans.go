package api

import (
	"context"
	"time"
)

// PlatformTenantPlanBudget is the request budget a tenant plan sets
// (ADR-240); zero disables a ceiling.
type PlatformTenantPlanBudget struct {
	MaxRequestsPerMinute int64 `json:"max_requests_per_minute"`
	MaxRequestsPerDay    int64 `json:"max_requests_per_day"`
}

// PlatformTenantPlanRequest creates or replaces a tenant plan (ADR-1307):
// tenant card terms and an optional request budget that assigning the plan
// applies to a tenant.
type PlatformTenantPlanRequest struct {
	Name                   string                    `json:"name"`
	Currency               string                    `json:"currency"`
	PriceMillicentsPerUnit int64                     `json:"price_millicents_per_unit"`
	IncludedUnitsPerMonth  int64                     `json:"included_units_per_month,omitempty"`
	Tiers                  []APIConsumerRateCardTier `json:"tiers,omitempty"`
	RouteWeights           map[string]int64          `json:"route_weights,omitempty"`
	RequestBudget          *PlatformTenantPlanBudget `json:"request_budget,omitempty"`
	// Unit (ADR-1310) and TierMode (ADR-1309) default to request and
	// graduated.
	Unit     string `json:"unit,omitempty"`
	TierMode string `json:"tier_mode,omitempty"`
}

// PlatformTenantPlanResponse is one tenant plan.
type PlatformTenantPlanResponse struct {
	ID                     string                    `json:"id"`
	Name                   string                    `json:"name"`
	Currency               string                    `json:"currency"`
	PriceMillicentsPerUnit int64                     `json:"price_millicents_per_unit"`
	IncludedUnitsPerMonth  int64                     `json:"included_units_per_month"`
	Tiers                  []APIConsumerRateCardTier `json:"tiers,omitempty"`
	RouteWeights           map[string]int64          `json:"route_weights,omitempty"`
	RequestBudget          *PlatformTenantPlanBudget `json:"request_budget,omitempty"`
	Unit                   string                    `json:"unit"`
	TierMode               string                    `json:"tier_mode"`
	CreatedAt              time.Time                 `json:"created_at"`
	UpdatedAt              time.Time                 `json:"updated_at"`
}

// PlatformTenantPlanListResponse lists an account's tenant plans by name.
type PlatformTenantPlanListResponse struct {
	Plans []PlatformTenantPlanResponse `json:"plans"`
}

// AssignPlatformTenantPlanRequest moves a tenant onto a plan from the next
// minute.
type AssignPlatformTenantPlanRequest struct {
	PlanID string `json:"plan_id"`
}

// PlatformTenantPlanAssignmentResponse is one plan assignment and the rate
// card it created.
type PlatformTenantPlanAssignmentResponse struct {
	ID            string    `json:"id"`
	TenantID      string    `json:"tenant_id"`
	PlanID        string    `json:"plan_id"`
	PlanName      string    `json:"plan_name,omitempty"`
	RateCardID    string    `json:"rate_card_id"`
	EffectiveFrom time.Time `json:"effective_from"`
	CreatedAt     time.Time `json:"created_at"`
}

// PlatformTenantPlanAssignmentListResponse lists a tenant's assignments,
// newest first.
type PlatformTenantPlanAssignmentListResponse struct {
	Assignments []PlatformTenantPlanAssignmentResponse `json:"assignments"`
}

// CreatePlatformTenantPlan creates a tenant plan.
func (c *Client) CreatePlatformTenantPlan(ctx context.Context, req PlatformTenantPlanRequest) (PlatformTenantPlanResponse, error) {
	var out PlatformTenantPlanResponse
	return out, c.do(ctx, "POST", "/v1/account/platform-tenant-plans", req, &out)
}

// ListPlatformTenantPlans lists the account's tenant plans.
func (c *Client) ListPlatformTenantPlans(ctx context.Context) (PlatformTenantPlanListResponse, error) {
	var out PlatformTenantPlanListResponse
	return out, c.do(ctx, "GET", "/v1/account/platform-tenant-plans", nil, &out)
}

// GetPlatformTenantPlan reads one tenant plan.
func (c *Client) GetPlatformTenantPlan(ctx context.Context, planID string) (PlatformTenantPlanResponse, error) {
	var out PlatformTenantPlanResponse
	return out, c.do(ctx, "GET", "/v1/account/platform-tenant-plans/"+planID, nil, &out)
}

// UpdatePlatformTenantPlan replaces a tenant plan's name and terms; tenants
// get them when the plan is assigned again.
func (c *Client) UpdatePlatformTenantPlan(ctx context.Context, planID string, req PlatformTenantPlanRequest) (PlatformTenantPlanResponse, error) {
	var out PlatformTenantPlanResponse
	return out, c.do(ctx, "PUT", "/v1/account/platform-tenant-plans/"+planID, req, &out)
}

// AssignPlatformTenantPlan moves a tenant onto a plan from the next minute.
func (c *Client) AssignPlatformTenantPlan(ctx context.Context, tenantID string, req AssignPlatformTenantPlanRequest) (PlatformTenantPlanAssignmentResponse, error) {
	var out PlatformTenantPlanAssignmentResponse
	return out, c.do(ctx, "POST", "/v1/account/platform-tenants/"+tenantID+"/plan-assignments", req, &out)
}

// ListPlatformTenantPlanAssignments lists a tenant's plan assignments.
func (c *Client) ListPlatformTenantPlanAssignments(ctx context.Context, tenantID string) (PlatformTenantPlanAssignmentListResponse, error) {
	var out PlatformTenantPlanAssignmentListResponse
	return out, c.do(ctx, "GET", "/v1/account/platform-tenants/"+tenantID+"/plan-assignments", nil, &out)
}
