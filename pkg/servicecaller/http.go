package servicecaller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"golang.org/x/sync/singleflight"
)

const (
	// CallerAssertionHeader is written by Gregale's internal service proxy.
	// Workloads should treat it as untrusted until HTTPMiddleware verifies it.
	CallerAssertionHeader = "X-Faas-Caller-Assertion"

	// DefaultJWKSCacheTTL matches the service-caller key endpoint's
	// Cache-Control policy. Keys are revalidated after five seconds.
	DefaultJWKSCacheTTL = 5 * time.Second
	// MaxAssertionBytes bounds parsing work for untrusted inbound headers.
	MaxAssertionBytes = 16 << 10

	unknownKeyRefreshInterval = time.Second
)

// ErrMissingAssertion identifies an absent assertion rejected by required
// middleware mode.
var ErrMissingAssertion = errors.New("servicecaller: assertion is missing")

// HTTPMiddlewareOptions configures an HTTP handler that consumes Gregale's
// signed caller assertion. Audience must be the receiving app's platform-
// injected FAAS_APP_ID; JWKSURL should be the platform service-caller key URL.
// Require controls whether a verified assertion is mandatory; it does not
// authorize particular caller app IDs, which the application must check.
type HTTPMiddlewareOptions struct {
	JWKSURL    string
	Audience   string
	Require    bool
	HTTPClient *http.Client

	// OnFailure is called when an assertion is present but cannot be verified,
	// or when Require is true and one is absent. It must not block. The token is
	// never included in the error passed to this callback.
	OnFailure func(error)
}

// HTTPMiddleware verifies X-Faas-Caller-Assertion and stores the verified
// Assertion in the request context. In optional mode, requests with no or
// invalid assertions continue without verified caller context. In required
// mode, they receive 401 Unauthorized. Optional mode is suitable for mixed
// signer rollouts; applications must not treat missing identity as verified.
type HTTPMiddleware struct {
	audience string
	require  bool
	onFail   func(error)
	keys     *callerKeyCache
}

// NewHTTPMiddleware validates the trust configuration without making a
// network request. JWKS retrieval is lazy and bounded on the first assertion.
func NewHTTPMiddleware(opts HTTPMiddlewareOptions) (*HTTPMiddleware, error) {
	audience := strings.TrimSpace(opts.Audience)
	if audience == "" {
		return nil, errors.New("servicecaller: middleware audience is required")
	}
	endpoint, err := validateJWKSURL(opts.JWKSURL)
	if err != nil {
		return nil, err
	}
	return &HTTPMiddleware{
		audience: audience,
		require:  opts.Require,
		onFail:   opts.OnFailure,
		keys: &callerKeyCache{
			endpoint: endpoint.String(),
			client:   opts.HTTPClient,
		},
	}, nil
}

// Wrap returns an HTTP handler that attaches verified caller identity to the
// request context and consumes the platform-owned assertion header. A present
// but invalid assertion is never exposed as identity, even in optional mode.
func (m *HTTPMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values := r.Header.Values(CallerAssertionHeader)
		if len(values) == 0 {
			if m.require {
				m.rejectOrContinue(w, r, next, ErrMissingAssertion)
				return
			}
			next.ServeHTTP(w, withoutVerifiedCaller(r))
			return
		}
		if len(values) != 1 {
			m.rejectOrContinue(w, r, next, fmt.Errorf("%w: expected one assertion header", ErrMalformed))
			return
		}

		token := strings.TrimSpace(values[0])
		var kid string
		var err error
		if len(token) > MaxAssertionBytes {
			err = fmt.Errorf("%w: assertion exceeds %d bytes", ErrMalformed, MaxAssertionBytes)
		} else {
			kid, err = assertionKeyID(token)
		}
		if err == nil && kid == "" {
			err = ErrUnknownKey
		}
		var caller Assertion
		if err == nil {
			var keys TrustedKeys
			keys, err = m.keys.forKid(r.Context(), kid)
			if err == nil {
				caller, err = Verify(token, keys, m.audience, time.Now())
			}
		}
		if err != nil {
			m.rejectOrContinue(w, r, next, err)
			return
		}

		ctx := context.WithValue(r.Context(), callerContextKey{}, callerContextValue{assertion: caller, verified: true})
		req := r.Clone(ctx)
		req.Header.Del(CallerAssertionHeader)
		next.ServeHTTP(w, req)
	})
}

// VerifiedCaller returns the caller assertion placed in ctx by HTTPMiddleware.
// The bool is false when the request had no assertion or verification failed
// in optional mode.
func VerifiedCaller(ctx context.Context) (Assertion, bool) {
	value, ok := ctx.Value(callerContextKey{}).(callerContextValue)
	return value.assertion, ok && value.verified
}

type callerContextKey struct{}

type callerContextValue struct {
	assertion Assertion
	verified  bool
}

func withoutVerifiedCaller(r *http.Request) *http.Request {
	ctx := context.WithValue(r.Context(), callerContextKey{}, callerContextValue{})
	if len(r.Header.Values(CallerAssertionHeader)) == 0 {
		return r.WithContext(ctx)
	}
	req := r.Clone(ctx)
	req.Header.Del(CallerAssertionHeader)
	return req
}

func (m *HTTPMiddleware) rejectOrContinue(w http.ResponseWriter, r *http.Request, next http.Handler, err error) {
	if m.onFail != nil {
		m.onFail(err)
	}
	if m.require {
		http.Error(w, "service caller assertion required or invalid", http.StatusUnauthorized)
		return
	}
	next.ServeHTTP(w, withoutVerifiedCaller(r))
}

func assertionKeyID(token string) (string, error) {
	if token == "" {
		return "", ErrMalformed
	}
	parsed, err := jose.ParseSigned(token, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil {
		return "", fmt.Errorf("%w: parse header: %w", ErrMalformed, err)
	}
	if len(parsed.Signatures) != 1 {
		return "", fmt.Errorf("%w: want exactly one signature", ErrMalformed)
	}
	return parsed.Signatures[0].Header.KeyID, nil
}

type callerKeyCache struct {
	endpoint string
	client   *http.Client

	mu                  sync.RWMutex
	keys                TrustedKeys
	expiresAt           time.Time
	unknownKidRefresh   time.Time
	retryAfter          time.Time
	lastRefreshError    error
	refreshSingleflight singleflight.Group
}

func (c *callerKeyCache) forKid(ctx context.Context, kid string) (TrustedKeys, error) {
	now := time.Now()
	c.mu.RLock()
	if now.Before(c.expiresAt) {
		if _, ok := c.keys[kid]; ok || now.Before(c.unknownKidRefresh) {
			keys := c.keys
			c.mu.RUnlock()
			return keys, nil
		}
	}
	if now.Before(c.retryAfter) && c.lastRefreshError != nil {
		err := c.lastRefreshError
		c.mu.RUnlock()
		return nil, err
	}
	c.mu.RUnlock()

	result := c.refreshSingleflight.DoChan("jwks", func() (any, error) {
		now := time.Now()
		c.mu.Lock()
		if now.Before(c.expiresAt) {
			if _, ok := c.keys[kid]; ok || now.Before(c.unknownKidRefresh) {
				keys := c.keys
				c.mu.Unlock()
				return keys, nil
			}
		}
		if now.Before(c.retryAfter) && c.lastRefreshError != nil {
			err := c.lastRefreshError
			c.mu.Unlock()
			return nil, err
		}
		c.mu.Unlock()

		keys, err := FetchTrustedKeys(ctx, c.client, c.endpoint)
		refreshedAt := time.Now()
		c.mu.Lock()
		defer c.mu.Unlock()
		if err != nil {
			c.lastRefreshError = err
			c.retryAfter = refreshedAt.Add(unknownKeyRefreshInterval)
			return nil, err
		}
		c.keys = keys
		c.expiresAt = refreshedAt.Add(DefaultJWKSCacheTTL)
		c.lastRefreshError = nil
		c.retryAfter = time.Time{}
		if _, ok := keys[kid]; !ok {
			c.unknownKidRefresh = refreshedAt.Add(unknownKeyRefreshInterval)
		} else {
			c.unknownKidRefresh = time.Time{}
		}
		return keys, nil
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case response := <-result:
		if response.Err != nil {
			return nil, response.Err
		}
		keys, ok := response.Val.(TrustedKeys)
		if !ok {
			return nil, errors.New("servicecaller: unexpected JWKS cache result")
		}
		return keys, nil
	}
}
