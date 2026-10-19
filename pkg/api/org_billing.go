package api

import "time"

// OrgPlanResponse describes provider-confirmed organization entitlements.
type OrgPlanResponse struct {
	OrgID            string     `json:"org_id"`
	Plan             Plan       `json:"plan"`
	PlanChangeStatus string     `json:"plan_change_status,omitempty"`
	RequestedPlan    string     `json:"requested_plan,omitempty"`
	EffectiveAt      *time.Time `json:"effective_at,omitempty"`
}
