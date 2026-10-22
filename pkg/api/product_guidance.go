package api

import "time"

// Guidance observes current-month admission counts and assigned allowances.
// It reserves no starts and makes no execution or billing guarantee.
type ProductUsageGuidance struct {
	AccountID        string                        `json:"account_id"`
	AppID            string                        `json:"app_id"`
	TenantID         string                        `json:"tenant_id"`
	Revision         string                        `json:"revision"`
	PolicyRevision   string                        `json:"policy_revision"`
	ObservedAt       time.Time                     `json:"observed_at"`
	Period           string                        `json:"period"`
	ResetsAt         time.Time                     `json:"resets_at"`
	NearLimitPercent int64                         `json:"near_limit_percent"`
	RunPermission    bool                          `json:"run_permission"`
	Suspended        bool                          `json:"suspended"`
	PlanAction       string                        `json:"plan_action"`
	Features         []ProductFeatureUsageGuidance `json:"features"`
	// InvoiceOverdue reports an overdue-invoice hold (ADR-20261018212918);
	// runnable features then have state invoice_overdue.
	InvoiceOverdue bool `json:"invoice_overdue,omitempty"`
}

type ProductFeatureUsageGuidance struct {
	Feature        string `json:"feature"`
	Label          string `json:"label"`
	Limit          int64  `json:"limit"`
	Starts         int64  `json:"starts"`
	Remaining      int64  `json:"remaining"`
	AllowanceState string `json:"allowance_state"`
	State          string `json:"state"`
}
