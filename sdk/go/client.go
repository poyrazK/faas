package faas

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/poyrazK/faas/sdk/go/internal/api"
)

// Client is the public SDK client. It embeds *internal/api.Client, so
// every method on the internal client is reachable as a method on the
// public Client. The wrapper exists to (a) keep the public surface
// decoupled from the internal package — when PR 12 splits pkg/api,
// the public package's only dependency is the embedded *api.Client
// and the *APIError wire shape; (b) allow options-pattern construction
// without touching the internal Client; (c) carry the package-level
// public types (Errors sentinels, IdempotencyKey, Decoder) without
// re-exporting 60+ internal DTOs.
type Client struct {
	*api.Client
	// log is the slog logger attached via WithLogger. nil means
	// "no logging"; PR 4 installs the actual logging round-tripper
	// that invokes it.
	log *slog.Logger
	// retryMax and retryBackoff are set by WithRetry. PR 4 wires
	// the actual retry round-tripper that reads them. Stored on
	// the Client (not in package-level vars) so each Client gets
	// its own policy without a global.
	retryMax     int
	retryBackoff time.Duration
}

// NewClient builds a Client for baseURL with the given bearer token.
// Pass functional Options to customize HTTP transport, retry policy,
// deploy timeout, logger, or pre-set the token / base URL.
//
// An empty token disables the Authorization header. The device-code
// flow and the public status page are the only operations that work
// without auth.
//
//	c, err := faas.NewClient("https://api.example.com", os.Getenv("FAAS_TOKEN"),
//	    faas.WithDeployTimeout(5*time.Minute),
//	    faas.WithLogger(slog.Default()),
//	)
func NewClient(baseURL, token string, opts ...Option) (*Client, error) {
	// Build the internal Client first. Default timeout is 30s, matching
	// the existing daemon-side behavior. Options can replace http /
	// deployHTTP / token / baseURL after construction.
	inner := api.NewClient(baseURL, token)

	// Wrap into the public Client so the option chain can mutate
	// fields on the embedded *api.Client (baseURL, token, http,
	// deployHTTP) without ever exposing those internals.
	c := &Client{Client: inner}

	// Run the option chain FIRST. WithHTTPClient replaces
	// HTTPClient().Transport with the caller's; every shim we
	// install below (idempotency, logging, retry) must wrap
	// whatever Transport the option chain left behind. Installing
	// any of them before the options would let WithHTTPClient
	// silently overwrite the shim with the caller's Transport —
	// dropping the opt-in key contract for callers who pass
	// &http.Client{Timeout: …}.
	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	// Install the round-tripper stack LAST, outermost → innermost:
	//
	//   retry RT          (outermost: sees every response, decides
	//     |                 whether to retry; one log per attempt
	//     v                 via the inner logging RT)
	//   logging RT        (one slog.Debug line per attempt)
	//     |
	//     v
	//   idempotency RT    (mints Idempotency-Key so retries are
	//     |                 safe server-side via the apid replay
	//     v                 middleware, 24h window)
	//   underlying RT     (user's via WithHTTPClient, or
	//                       http.DefaultTransport)
	//
	// The order matters: retry must be outermost so its decision
	// is based on the immediate response from the chain below;
	// logging is one step inside so it sees every attempt
	// individually (a retried request emits two log lines);
	// idempotency is innermost so the Idempotency-Key header is
	// set on the actual outgoing request regardless of retries.
	httpClient := c.Client.HTTPClient()
	// The internal *api.Client leaves Transport unset
	// (&http.Client{Timeout: …} → typed-nil *http.Transport).
	// If we wrap a typed-nil here, the first request panics.
	// Fall back to http.DefaultTransport for the round-tripper
	// chain; the Timeout on the http.Client still applies.
	if httpClient.Transport == nil {
		httpClient.Transport = http.DefaultTransport
	}
	// retry first (outermost)
	httpClient.Transport = newRetryRoundTripper(httpClient.Transport, c.retryMax, c.retryBackoff)
	// logging second (sees each attempt)
	httpClient.Transport = newLoggingRoundTripper(httpClient.Transport, c.log)
	// idempotency last (innermost; sets the header on the
	// outgoing request)
	httpClient.Transport = newIdempotencyRoundTripper(httpClient.Transport)

	return c, nil
}

// APIError is re-exported as a type alias so callers can write
// errors.As(err, &faas.APIError{}) without importing the internal
// package. The wire shape is defined in internal/api/apierror.go.
type APIError = api.APIError

// Problem is re-exported as a type alias. It carries the canonical
// RFC 7807 fields (Type, Title, Status, Code, Detail) plus the
// platform-specific extensions (Limit, Observed, DocsURL,
// BillingPortalURL, CheckoutURL, PaddleCheckoutURL, TxID).
type Problem = api.Problem

// ErrNoBody is re-exported as a value alias so errors.Is(err,
// faas.ErrNoBody) works without reaching into internal/api.
var ErrNoBody = api.ErrNoBody
