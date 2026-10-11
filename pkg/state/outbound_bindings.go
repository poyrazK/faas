package state

import (
	"context"
	"errors"
	"math"
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
	RunsEnabled          bool
	DailyRequestLimit    *int64
	CredentialSource     string
	CredentialConfigured bool
	OwnerKind            string
	RequestPolicy        api.OutboundRequestPolicy
	AIProvider           string
	AIDailyTokenLimit    *int64
	AIDailyCostLimit     *int64
	AIMonthlyCostLimit   *int64
}

// OutboundAIUsageItem is one UTC-day AI usage rollup written by outboundd
// for a caller (an app ID or "runs") and model (ADR-979).
type OutboundAIUsageItem struct {
	Caller               string
	Model                string
	Requests             int64
	ErrorRequests        int64
	UsageUnknownRequests int64
	InputTokens          int64
	CachedInputTokens    int64
	CacheWriteTokens     int64
	OutputTokens         int64
	CostMillicents       int64
}

// OutboundAIUsage is one UTC day of AI usage for an integration.
// DailyTokenCount is the live admission counter for today and the sum of
// rollup tokens for an earlier day. Costs are settled rollup sums for the
// day and for its calendar month through that day (ADR-983).
type OutboundAIUsage struct {
	UsageDate             string
	AIProvider            string
	DailyTokenCount       int64
	DailyTokenLimit       *int64
	ResetsAt              time.Time
	Items                 []OutboundAIUsageItem
	DailyCostMillicents   int64
	DailyCostLimit        *int64
	Month                 string
	MonthlyCostMillicents int64
	MonthlyCostLimit      *int64
}

// OutboundAIPolicy is the complete customer AI gateway setting (ADR-979,
// ADR-983). An empty Provider turns AI handling off and requires no limits.
type OutboundAIPolicy struct {
	Provider         string
	DailyTokenLimit  *int64
	DailyCostLimit   *int64
	MonthlyCostLimit *int64
}

func (p OutboundAIPolicy) valid() bool {
	return api.ValidOutboundAIPolicy(p.Provider, p.DailyTokenLimit) &&
		api.ValidOutboundAICostLimits(p.Provider, p.DailyCostLimit, p.MonthlyCostLimit)
}

// OutboundAIPriceList is the current price list; EffectiveFrom is nil when
// the integration has never had one.
type OutboundAIPriceList struct {
	EffectiveFrom *time.Time
	Entries       []api.OutboundAIPriceEntry
}

// OutboundAIStore owns customer AI gateway settings and reads the usage
// rollups that outboundd writes. It is separate from OutboundBindingStore so
// existing binding implementations do not change.
type OutboundAIStore interface {
	SetOutboundAIPolicy(ctx context.Context, accountID, integrationID string, policy OutboundAIPolicy) error
	GetOutboundAIUsage(ctx context.Context, accountID, integrationID, usageDate string) (OutboundAIUsage, error)
	SetOutboundBindingAITokenLimit(ctx context.Context, accountID, appID, integrationID string, limit *int64) error
	GetOutboundAIPrices(ctx context.Context, accountID, integrationID string) (OutboundAIPriceList, error)
	SetOutboundAIPrices(ctx context.Context, accountID, integrationID string, entries []api.OutboundAIPriceEntry) error
}

// validOutboundAIUsageDate accepts a YYYY-MM-DD UTC date inside the rollup
// retention window, so reads cannot scan beyond what outboundd keeps.
func validOutboundAIUsageDate(usageDate string, now time.Time) (time.Time, bool) {
	day, err := time.Parse("2006-01-02", usageDate)
	if err != nil {
		return time.Time{}, false
	}
	utcNow := now.UTC()
	today := time.Date(utcNow.Year(), utcNow.Month(), utcNow.Day(), 0, 0, 0, 0, time.UTC)
	oldest := today.AddDate(0, 0, -api.OutboundAIUsageRetentionDays)
	return day, !day.Before(oldest) && !day.After(today)
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

// OutboundBindingUsage is the UTC-day request usage and optional cap for one
// app-to-integration binding. It counts calls granted by outbound admission.
type OutboundBindingUsage struct {
	DailyRequestCount int64
	DailyRequestLimit *int64
	UsageDate         string
	ResetsAt          time.Time
}

type OutboundAppBinding struct {
	OutboundIntegrationOffer
	AppID                    string
	BindingDailyRequestLimit *int64
	// BindingAIDailyTokenLimit is this app's own AI token limit (ADR-983).
	BindingAIDailyTokenLimit *int64
	RouteMethods             []string
	RoutePathPrefixes        []string
	CreatedAt                time.Time
}

// OutboundBindingStore owns customer-created integration lifecycle, app
// bindings, and sealed-credential intent. outboundd provisions only operator
// integrations and operator app attachments.
type OutboundBindingStore interface {
	ListOutboundIntegrationOffers(context.Context, string) ([]OutboundIntegrationOffer, error)
	CreateOutboundIntegration(context.Context, OutboundIntegrationOffer) (OutboundIntegrationOffer, error)
	DeleteOutboundIntegration(context.Context, string, string) error
	SetOutboundDailyRequestLimit(context.Context, string, string, *int64) error
	SetOutboundRequestPolicy(context.Context, string, string, api.OutboundRequestPolicy) error
	GetOutboundIntegrationUsage(context.Context, string, string) (OutboundIntegrationUsage, error)
	ListOutboundAppBindings(context.Context, string, string) ([]OutboundAppBinding, error)
	BindOutboundIntegration(context.Context, string, string, string) (OutboundAppBinding, error)
	UnbindOutboundIntegration(context.Context, string, string, string) error
	UpdateOutboundBindingPolicy(context.Context, string, string, string, []string, []string) error
	SetOutboundBindingDailyRequestLimit(context.Context, string, string, string, *int64) error
	GetOutboundBindingUsage(context.Context, string, string, string) (OutboundBindingUsage, error)
	SetOutboundCredential(context.Context, string, string, []byte) error
	DeleteOutboundCredential(context.Context, string, string) error
}

// OutboundRunsGrantStore manages account-owned grants for future stateless
// Run calls. Run grants remain independent of existing app bindings.
type OutboundRunsGrantStore interface {
	SetOutboundIntegrationRunsEnabled(context.Context, string, string, bool) error
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
	if !api.ValidOutboundAIPolicy(offer.AIProvider, offer.AIDailyTokenLimit) {
		return ErrInvalidArgument
	}
	policy := offer.RequestPolicy
	if policy.RatePerSecond <= 0 || math.IsNaN(policy.RatePerSecond) || math.IsInf(policy.RatePerSecond, 0) ||
		policy.Burst < 1 || policy.MaxInFlight < 1 || policy.RequestTimeoutMS < 1 ||
		policy.MaxRetries < 0 || policy.MaxRetries > api.MaxOutboundRetries ||
		policy.ResponseCacheTTLSeconds < 0 || policy.ResponseCacheTTLSeconds > api.MaxOutboundResponseCacheTTLSeconds ||
		policy.RetryBudgetPerMinute < 0 || policy.RetryBudgetPerMinute > api.MaxOutboundRetryBudgetPerMinute ||
		!api.ValidOutboundCircuitBreakerPolicy(policy.CircuitBreakerFailureThreshold, policy.CircuitBreakerOpenSeconds) {
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
	if len(name) == 0 {
		return false
	}
	for i := 0; i < len(name); i++ {
		ch := name[i]
		valid := ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || i > 0 && ch == '-'
		if !valid || ch == '-' && i == len(name)-1 {
			return false
		}
	}
	return !strings.HasPrefix(name, "-")
}
