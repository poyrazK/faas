// Package outbound implements explicit, integration-scoped admission for
// third-party HTTP requests. Callers opt in by sending requests through the
// gateway; Gregale never attempts to inspect arbitrary encrypted egress.
package outbound

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrIntegrationNotFound = errors.New("outbound integration not found")
	ErrIntegrationDisabled = errors.New("outbound integration disabled")
	ErrUnauthorized        = errors.New("outbound integration token is invalid")
	ErrAppNotAttached      = errors.New("app is not attached to outbound integration")
	ErrInvalidIntegration  = errors.New("invalid outbound integration")
)

const (
	ReasonRate        = "rate_limit"
	ReasonConcurrency = "concurrency_limit"
	ReasonDailyLimit  = "daily_request_limit"

	ProviderAuthApplication        = "application"
	ProviderAuthManaged            = "managed"
	CredentialSourceOperatorEnv    = "operator_env"
	CredentialSourceCustomerSealed = "customer_sealed"
	IntegrationOwnerOperator       = "operator"
	IntegrationOwnerCustomer       = "customer"
)

// Integration is the immutable policy used for one provider. TokenHash is a
// SHA-256 digest for application-auth integrations; managed integrations use
// workload identity and may leave it zero. Raw bearer tokens are never logged.
type Integration struct {
	ID                  string
	Origin              *url.URL
	TokenHash           [32]byte
	AppIDs              map[string]struct{}
	OperatorAppIDs      map[string]struct{}
	CustomerAppRoutes   map[string]RoutePolicy
	RatePerSecond       float64
	Burst               int
	MaxInFlight         int
	DailyRequestLimit   *int64
	RequestTimeout      time.Duration
	ProviderAuthMode    string
	CredentialSource    string
	OwnerKind           string
	AllowedMethods      []string
	AllowedPathPrefixes []string
	Enabled             bool
}

// NewIntegration validates and constructs an integration from a raw token.
// The raw token is only held by the caller and is hashed immediately.
func NewIntegration(id, origin, token string, appIDs []string, ratePerSecond float64, burst, maxInFlight int, requestTimeout time.Duration) (Integration, error) {
	if token == "" {
		return Integration{}, fmt.Errorf("%w: token is required", ErrInvalidIntegration)
	}
	u, err := url.Parse(origin)
	if err != nil {
		return Integration{}, fmt.Errorf("%w: origin: %w", ErrInvalidIntegration, err)
	}
	apps := make(map[string]struct{}, len(appIDs))
	for _, appID := range appIDs {
		if strings.TrimSpace(appID) != "" {
			apps[appID] = struct{}{}
		}
	}
	sum := sha256.Sum256([]byte(token))
	i := Integration{
		ID:               id,
		Origin:           u,
		TokenHash:        sum,
		AppIDs:           apps,
		RatePerSecond:    ratePerSecond,
		Burst:            burst,
		MaxInFlight:      maxInFlight,
		RequestTimeout:   requestTimeout,
		ProviderAuthMode: ProviderAuthApplication,
		OwnerKind:        IntegrationOwnerOperator,
		Enabled:          true,
	}
	if err := i.Validate(); err != nil {
		return Integration{}, err
	}
	return i, nil
}

func (i Integration) Validate() error {
	if strings.TrimSpace(i.ID) == "" || i.Origin == nil {
		return fmt.Errorf("%w: id and origin are required", ErrInvalidIntegration)
	}
	if i.TokenHash == ([32]byte{}) && i.ProviderAuthMode != ProviderAuthManaged {
		return fmt.Errorf("%w: token hash is required", ErrInvalidIntegration)
	}
	if i.Origin.Scheme != "https" || i.Origin.Host == "" || i.Origin.User != nil || i.Origin.RawQuery != "" || i.Origin.Fragment != "" {
		return fmt.Errorf("%w: origin must be an https URL without credentials, query, or fragment", ErrInvalidIntegration)
	}
	if math.IsNaN(i.RatePerSecond) || math.IsInf(i.RatePerSecond, 0) || i.RatePerSecond <= 0 || i.Burst < 1 || i.MaxInFlight < 1 {
		return fmt.Errorf("%w: rate, burst, and max_in_flight must be positive", ErrInvalidIntegration)
	}
	if i.DailyRequestLimit != nil && (*i.DailyRequestLimit < 1 || *i.DailyRequestLimit > api.MaxOutboundRequestsPerDay) {
		return fmt.Errorf("%w: daily request limit is outside the supported range", ErrInvalidIntegration)
	}
	// An operator may provision an integration with no initial app. Customer
	// attachments live in apid-owned outbound_app_bindings and are loaded by
	// the resolver at request time.
	if i.RequestTimeout <= 0 {
		return fmt.Errorf("%w: request timeout must be positive", ErrInvalidIntegration)
	}
	if i.ProviderAuthMode != "" && i.ProviderAuthMode != ProviderAuthApplication && i.ProviderAuthMode != ProviderAuthManaged {
		return fmt.Errorf("%w: provider authentication mode is invalid", ErrInvalidIntegration)
	}
	if i.CredentialSource != "" && i.CredentialSource != CredentialSourceOperatorEnv && i.CredentialSource != CredentialSourceCustomerSealed {
		return fmt.Errorf("%w: credential source is invalid", ErrInvalidIntegration)
	}
	if i.OwnerKind != "" && i.OwnerKind != IntegrationOwnerOperator && i.OwnerKind != IntegrationOwnerCustomer {
		return fmt.Errorf("%w: integration owner is invalid", ErrInvalidIntegration)
	}
	if i.CredentialSource == CredentialSourceCustomerSealed && i.ProviderAuthMode != ProviderAuthManaged {
		return fmt.Errorf("%w: customer credential source requires managed authentication", ErrInvalidIntegration)
	}
	if i.OwnerKind == IntegrationOwnerCustomer && (i.ProviderAuthMode != ProviderAuthManaged || i.CredentialSource != CredentialSourceCustomerSealed) {
		return fmt.Errorf("%w: customer-owned integration requires managed customer credentials", ErrInvalidIntegration)
	}
	return i.validateRoutePolicy()
}

// MetricLabel bounds Prometheus cardinality for customer-created integrations.
// Operator IDs remain configuration-owned and customer integrations share one
// label regardless of how many customers create.
func (i Integration) MetricLabel() string {
	if i.OwnerKind == IntegrationOwnerCustomer {
		return "customer_managed"
	}
	return i.ID
}

func (i Integration) AllowsApp(appID string) bool {
	_, ok := i.AppIDs[appID]
	return ok
}

// Resolver supplies the policy for an integration ID.
type Resolver interface {
	Integration(context.Context, string) (Integration, error)
}

// AdmissionSpec is the policy snapshot supplied to the shared admission
// backend. Backends must enforce it atomically across all gateway processes.
type AdmissionSpec struct {
	IntegrationID     string
	RatePerSecond     float64
	Burst             int
	MaxInFlight       int
	DailyRequestLimit *int64
	LeaseTTL          time.Duration
}

// Decision describes an admission or a deterministic rejection. A granted
// decision always has a lease ID which must be released exactly once.
type Decision struct {
	Granted    bool
	LeaseID    string
	RetryAfter time.Duration
	Reason     string
}

// Backend is the shared state boundary. Implementations must fail closed on
// storage errors: callers should not send an upstream request without a grant.
type Backend interface {
	Admit(context.Context, AdmissionSpec) (Decision, error)
	Release(context.Context, string, string) error
}

// StaticResolver is useful for a dedicated gateway process configured at
// startup. The API/state integration can replace it without changing the
// request path.
type StaticResolver struct {
	items map[string]Integration
}

func NewStaticResolver(items []Integration) (*StaticResolver, error) {
	r := &StaticResolver{items: make(map[string]Integration, len(items))}
	for _, item := range items {
		if err := item.Validate(); err != nil {
			return nil, err
		}
		copyItem := item
		copyItem.Origin = cloneURL(item.Origin)
		copyItem.AppIDs = make(map[string]struct{}, len(item.AppIDs))
		for appID := range item.AppIDs {
			copyItem.AppIDs[appID] = struct{}{}
		}
		copyItem.OperatorAppIDs = copyStringSet(item.OperatorAppIDs)
		copyItem.CustomerAppRoutes = copyRoutePolicies(item.CustomerAppRoutes)
		copyItem.DailyRequestLimit = copyInt64Pointer(item.DailyRequestLimit)
		copyItem.AllowedMethods = append([]string(nil), item.AllowedMethods...)
		copyItem.AllowedPathPrefixes = append([]string(nil), item.AllowedPathPrefixes...)
		r.items[item.ID] = copyItem
	}
	return r, nil
}

func (r *StaticResolver) Integration(ctx context.Context, id string) (Integration, error) {
	if err := ctx.Err(); err != nil {
		return Integration{}, err
	}
	i, ok := r.items[id]
	if !ok {
		return Integration{}, ErrIntegrationNotFound
	}
	if !i.Enabled {
		return Integration{}, ErrIntegrationDisabled
	}
	i.Origin = cloneURL(i.Origin)
	i.AppIDs = copyStringSet(i.AppIDs)
	i.OperatorAppIDs = copyStringSet(i.OperatorAppIDs)
	i.CustomerAppRoutes = copyRoutePolicies(i.CustomerAppRoutes)
	i.DailyRequestLimit = copyInt64Pointer(i.DailyRequestLimit)
	i.AllowedMethods = append([]string(nil), i.AllowedMethods...)
	i.AllowedPathPrefixes = append([]string(nil), i.AllowedPathPrefixes...)
	return i, nil
}

func copyInt64Pointer(in *int64) *int64 {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneURL(in *url.URL) *url.URL {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func copyStringSet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for key := range in {
		out[key] = struct{}{}
	}
	return out
}

func copyRoutePolicies(in map[string]RoutePolicy) map[string]RoutePolicy {
	out := make(map[string]RoutePolicy, len(in))
	for appID, policy := range in {
		out[appID] = RoutePolicy{
			AllowedMethods:      append([]string(nil), policy.AllowedMethods...),
			AllowedPathPrefixes: append([]string(nil), policy.AllowedPathPrefixes...),
		}
	}
	return out
}
