package api

import "time"

// OutboundIntegrationOffer is an account-visible managed integration. It
// intentionally contains no provider credential or gateway admission token.
type OutboundIntegrationOffer struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Origin               string   `json:"origin"`
	AllowedMethods       []string `json:"allowed_methods"`
	AllowedPathPrefixes  []string `json:"allowed_path_prefixes"`
	Enabled              bool     `json:"enabled"`
	CredentialSource     string   `json:"credential_source"`
	CredentialConfigured bool     `json:"credential_configured"`
}

// PutOutboundCredentialRequest sets or rotates the provider Authorization
// value. The response intentionally has no equivalent secret field.
type PutOutboundCredentialRequest struct {
	Authorization string `json:"authorization"`
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
