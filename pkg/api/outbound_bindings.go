package api

import "time"

// OutboundIntegrationOffer is an account-visible managed integration. It
// intentionally contains no provider credential or gateway admission token;
// DailyRequestLimit is the effective configured limit after plan clamping.
type OutboundIntegrationOffer struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Origin               string   `json:"origin"`
	AllowedMethods       []string `json:"allowed_methods"`
	AllowedPathPrefixes  []string `json:"allowed_path_prefixes"`
	Enabled              bool     `json:"enabled"`
	CredentialSource     string   `json:"credential_source"`
	CredentialConfigured bool     `json:"credential_configured"`
	OwnerKind            string   `json:"owner_kind"`
	DailyRequestLimit    *int64   `json:"daily_request_limit"`
}

// CreateOutboundIntegrationRequest creates a customer-owned managed
// integration. Its provider Authorization value is uploaded separately.
type CreateOutboundIntegrationRequest struct {
	Name                string   `json:"name"`
	Origin              string   `json:"origin"`
	AllowedMethods      []string `json:"allowed_methods"`
	AllowedPathPrefixes []string `json:"allowed_path_prefixes"`
	DailyRequestLimit   *int64   `json:"daily_request_limit,omitempty"`
}

// PutOutboundCredentialRequest sets or rotates the provider Authorization
// value. The response intentionally has no equivalent secret field.
type PutOutboundCredentialRequest struct {
	Authorization string `json:"authorization"`
}

// PutOutboundDailyRequestBudgetRequest sets a per-integration daily limit;
// null removes the customer-selected limit.
type PutOutboundDailyRequestBudgetRequest struct {
	DailyRequestLimit *int64 `json:"daily_request_limit"`
}

// OutboundIntegrationUsageResponse reports UTC-day gateway admissions. Calls
// count when admitted, including those whose upstream request later fails.
// DailyRequestLimit is the effective optional cap after applying the account
// plan ceiling.
type OutboundIntegrationUsageResponse struct {
	DailyRequestCount int64     `json:"daily_request_count"`
	DailyRequestLimit *int64    `json:"daily_request_limit"`
	UsageDate         string    `json:"usage_date"`
	ResetsAt          time.Time `json:"resets_at"`
}

type OutboundIntegrationOfferList struct {
	Items []OutboundIntegrationOffer `json:"items"`
}

type OutboundAppBinding struct {
	Integration         OutboundIntegrationOffer `json:"integration"`
	AppID               string                   `json:"app_id"`
	AllowedMethods      []string                 `json:"allowed_methods"`
	AllowedPathPrefixes []string                 `json:"allowed_path_prefixes"`
	CreatedAt           time.Time                `json:"created_at"`
}

// UpdateOutboundBindingPolicyRequest narrows an app's route access within
// the integration policy. It cannot change the provider origin or widen it.
type UpdateOutboundBindingPolicyRequest struct {
	AllowedMethods      []string `json:"allowed_methods"`
	AllowedPathPrefixes []string `json:"allowed_path_prefixes"`
}

type OutboundAppBindingList struct {
	Items []OutboundAppBinding `json:"items"`
}
