package api

import "time"

// OutboundIntegrationOffer is an account-visible managed integration. It
// intentionally contains no provider credential or gateway admission token.
type OutboundIntegrationOffer struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Origin              string   `json:"origin"`
	AllowedMethods      []string `json:"allowed_methods"`
	AllowedPathPrefixes []string `json:"allowed_path_prefixes"`
	Enabled             bool     `json:"enabled"`
}

type OutboundIntegrationOfferList struct {
	Items []OutboundIntegrationOffer `json:"items"`
}

type OutboundAppBinding struct {
	Integration OutboundIntegrationOffer `json:"integration"`
	AppID       string                   `json:"app_id"`
	CreatedAt   time.Time                `json:"created_at"`
}

type OutboundAppBindingList struct {
	Items []OutboundAppBinding `json:"items"`
}
