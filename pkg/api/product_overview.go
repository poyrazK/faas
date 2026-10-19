package api

import "time"

// An owner-only observation window over assigned customers, not an admission
// decision, live membership check, or native qualification.
type ProductCustomerOverviewPage struct {
	AppID              string                    `json:"app_id"`
	ObservedAt         time.Time                 `json:"observed_at"`
	ObservationScope   string                    `json:"observation_scope"`
	QualificationState string                    `json:"qualification_state"`
	Period             string                    `json:"period"`
	Customers          []ProductCustomerOverview `json:"customers"`
	NextCursor         string                    `json:"next_cursor,omitempty"`
}

type ProductCustomerOverview struct {
	Name         string                      `json:"name"`
	TenantStatus string                      `json:"tenant_status"`
	Customer     ProductCustomer             `json:"customer"`
	Readiness    ProductDoctorReport         `json:"readiness"`
	Activation   ProductCustomerActivation   `json:"activation"`
	Work         ProductCustomerWorkSummary  `json:"work"`
	NextActions  []ProductCustomerNextAction `json:"next_actions"`
}

// Activation is evidence after the latest guided setup. An assignment made
// without guided onboarding has no activation record, rather than zero usage.
type ProductCustomerActivation struct {
	State            string                    `json:"state"`
	OnboardingID     string                    `json:"onboarding_id,omitempty"`
	SetupAt          *time.Time                `json:"setup_at,omitempty"`
	SetupCurrent     bool                      `json:"setup_current"`
	AccessConfigured bool                      `json:"access_configured"`
	AccessVerifiedAt *time.Time                `json:"access_verified_at,omitempty"`
	FirstSuccess     *ProductOnboardingSuccess `json:"first_success,omitempty"`
}

// Counts include retained, unwaived, unresolved receipts from every admission
// month. Ordinary accepted/running work is pending, not work needing review.
type ProductCustomerWorkSummary struct {
	Unresolved   int64  `json:"unresolved"`
	NeedsReview  int64  `json:"needs_review"`
	ReviewPeriod string `json:"review_period,omitempty"`
}

type ProductCustomerNextAction struct {
	Code  string `json:"code"`
	Label string `json:"label"`
	URL   string `json:"url"`
}
