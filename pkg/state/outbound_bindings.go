package state

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound/routepolicy"
)

const MaxCustomerOutboundIntegrations = 25

var ErrOutboundIntegrationLimit = errors.New("state: outbound integration limit reached")

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
	DailyRequestLimit    *int64
	CredentialSource     string
	CredentialConfigured bool
	OwnerKind            string
}

// OutboundIntegrationUsage is the durable UTC-day counter for one managed
// integration. It counts gateway-admitted calls, whether or not the provider
// ultimately returns a successful response.
type OutboundIntegrationUsage struct {
	DailyRequestCount int64
	DailyRequestLimit *int64
	UsageDate         string
	ResetsAt          time.Time
}

type OutboundAppBinding struct {
	OutboundIntegrationOffer
	AppID             string
	RouteMethods      []string
	RoutePathPrefixes []string
	CreatedAt         time.Time
}

// OutboundBindingStore owns customer-created integration lifecycle, app
// bindings, and sealed-credential intent. outboundd provisions only operator
// integrations and operator app attachments.
type OutboundBindingStore interface {
	ListOutboundIntegrationOffers(context.Context, string) ([]OutboundIntegrationOffer, error)
	CreateOutboundIntegration(context.Context, OutboundIntegrationOffer) (OutboundIntegrationOffer, error)
	DeleteOutboundIntegration(context.Context, string, string) error
	SetOutboundDailyRequestLimit(context.Context, string, string, *int64) error
	GetOutboundIntegrationUsage(context.Context, string, string) (OutboundIntegrationUsage, error)
	ListOutboundAppBindings(context.Context, string, string) ([]OutboundAppBinding, error)
	BindOutboundIntegration(context.Context, string, string, string) (OutboundAppBinding, error)
	UnbindOutboundIntegration(context.Context, string, string, string) error
	UpdateOutboundBindingPolicy(context.Context, string, string, string, []string, []string) error
	SetOutboundCredential(context.Context, string, string, []byte) error
	DeleteOutboundCredential(context.Context, string, string) error
}

func validateCustomerOutboundIntegration(offer OutboundIntegrationOffer) error {
	if _, err := uuid.Parse(offer.ID); err != nil {
		return ErrInvalidArgument
	}
	if _, err := uuid.Parse(offer.AccountID); err != nil {
		return ErrInvalidArgument
	}
	if offer.OwnerKind != "customer" || !offer.Enabled ||
		offer.CredentialSource != "customer_sealed" || offer.CredentialConfigured {
		return ErrInvalidArgument
	}
	if offer.DailyRequestLimit != nil && (*offer.DailyRequestLimit < 1 || *offer.DailyRequestLimit > api.MaxOutboundRequestsPerDay) {
		return ErrInvalidArgument
	}
	if len(offer.Name) < 1 || len(offer.Name) > 63 || !isOutboundIntegrationName(offer.Name) {
		return ErrInvalidArgument
	}
	origin, err := url.Parse(offer.Origin)
	if err != nil || origin.Scheme != "https" || origin.Hostname() == "" || origin.User != nil ||
		strings.ContainsAny(offer.Origin, "?#") || origin.ForceQuery || origin.RawQuery != "" || origin.Fragment != "" || origin.RawFragment != "" {
		return ErrInvalidArgument
	}
	if originPath := origin.EscapedPath(); originPath != "" && !routepolicy.CanonicalPath(originPath) {
		return ErrInvalidArgument
	}
	if err := routepolicy.Validate(routepolicy.Policy{
		AllowedMethods: offer.AllowedMethods, AllowedPathPrefixes: offer.AllowedPathPrefixes,
	}); err != nil {
		return ErrInvalidArgument
	}
	return nil
}

func isOutboundIntegrationName(name string) bool {
	for i := 0; i < len(name); i++ {
		ch := name[i]
		valid := ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || i > 0 && ch == '-'
		if !valid || ch == '-' && i == len(name)-1 {
			return false
		}
	}
	return !strings.HasPrefix(name, "-")
}
