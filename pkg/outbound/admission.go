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
)

// Integration is the immutable policy used for one provider. TokenHash is a
// SHA-256 digest; the raw bearer token is intentionally never stored or logged.
type Integration struct {
	ID             string
	Origin         *url.URL
	TokenHash      [32]byte
	AppIDs         map[string]struct{}
	RatePerSecond  float64
	Burst          int
	MaxInFlight    int
	RequestTimeout time.Duration
	Enabled        bool
}

// NewIntegration validates and constructs an integration from a raw token.
// The raw token is only held by the caller and is hashed immediately.
func NewIntegration(id, origin, token string, appIDs []string, ratePerSecond float64, burst, maxInFlight int, requestTimeout time.Duration) (Integration, error) {
	if token == "" {
		return Integration{}, fmt.Errorf("%w: token is required", ErrInvalidIntegration)
	}
	u, err := url.Parse(origin)
	if err != nil {
		return Integration{}, fmt.Errorf("%w: origin: %v", ErrInvalidIntegration, err)
	}
	apps := make(map[string]struct{}, len(appIDs))
	for _, appID := range appIDs {
		if strings.TrimSpace(appID) != "" {
			apps[appID] = struct{}{}
		}
	}
	sum := sha256.Sum256([]byte(token))
	i := Integration{
		ID:             id,
		Origin:         u,
		TokenHash:      sum,
		AppIDs:         apps,
		RatePerSecond:  ratePerSecond,
		Burst:          burst,
		MaxInFlight:    maxInFlight,
		RequestTimeout: requestTimeout,
		Enabled:        true,
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
	if i.TokenHash == ([32]byte{}) {
		return fmt.Errorf("%w: token hash is required", ErrInvalidIntegration)
	}
	if i.Origin.Scheme != "https" || i.Origin.Host == "" || i.Origin.User != nil || i.Origin.RawQuery != "" || i.Origin.Fragment != "" {
		return fmt.Errorf("%w: origin must be an https URL without credentials, query, or fragment", ErrInvalidIntegration)
	}
	if math.IsNaN(i.RatePerSecond) || math.IsInf(i.RatePerSecond, 0) || i.RatePerSecond <= 0 || i.Burst < 1 || i.MaxInFlight < 1 {
		return fmt.Errorf("%w: rate, burst, and max_in_flight must be positive", ErrInvalidIntegration)
	}
	if len(i.AppIDs) == 0 {
		return fmt.Errorf("%w: at least one attached app is required", ErrInvalidIntegration)
	}
	if i.RequestTimeout <= 0 {
		return fmt.Errorf("%w: request timeout must be positive", ErrInvalidIntegration)
	}
	return nil
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
	IntegrationID string
	RatePerSecond float64
	Burst         int
	MaxInFlight   int
	LeaseTTL      time.Duration
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
	return i, nil
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
