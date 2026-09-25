package state

import (
	"context"
	"time"
)

// OutboundIntegrationOffer is safe account-scoped policy metadata. Provider
// authorization values and gateway tokens never appear in this projection.
type OutboundIntegrationOffer struct {
	ID                   string
	AccountID            string
	Name                 string
	Origin               string
	AllowedMethods       []string
	AllowedPathPrefixes  []string
	Enabled              bool
	CredentialSource     string
	CredentialConfigured bool
}

type OutboundAppBinding struct {
	OutboundIntegrationOffer
	AppID             string
	RouteMethods      []string
	RoutePathPrefixes []string
	CreatedAt         time.Time
}

// OutboundBindingStore owns customer app-binding and sealed-credential intent.
// outboundd still owns outbound_integrations and operator app attachments.
type OutboundBindingStore interface {
	ListOutboundIntegrationOffers(context.Context, string) ([]OutboundIntegrationOffer, error)
	ListOutboundAppBindings(context.Context, string, string) ([]OutboundAppBinding, error)
	BindOutboundIntegration(context.Context, string, string, string) (OutboundAppBinding, error)
	UnbindOutboundIntegration(context.Context, string, string, string) error
	UpdateOutboundBindingPolicy(context.Context, string, string, string, []string, []string) error
	SetOutboundCredential(context.Context, string, string, []byte) error
	DeleteOutboundCredential(context.Context, string, string) error
}
