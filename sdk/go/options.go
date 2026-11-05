package faas

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// errOptionUnsupported is returned by Options that cannot mutate the
// embedded *api.Client because its fields are unexported. PR 12
// reconciles this by promoting the relevant fields; until then the
// options return an error so callers see the limitation rather than
// silently dropping the value.
type errOptionUnsupported struct{ option string }

func (e errOptionUnsupported) Error() string {
	return fmt.Sprintf("faas: option %s is not yet wired (lands in PR 12)", e.option)
}

// Option is a functional option for NewClient. Options mutate the
// Client during construction. An Option that returns a non-nil error
// aborts NewClient and surfaces the error to the caller.
type Option func(*Client) error

// WithHTTPClient replaces the underlying *http.Client used for JSON
// requests. Use this to set a custom Transport (TLS config, proxy,
// dialer, etc.). The Idempotency-Key round-tripper installed by
// NewClient is preserved — it sits between the caller's Transport
// and the wire, so the caller's retry/auth middleware runs AFTER
// the header is injected.
//
// Passing nil is a no-op; the existing client is kept. A nil
// hc.Transport (the default for &http.Client{Timeout: …}) is
// also a no-op so the round-tripper stack doesn't end up wrapping
// a typed-nil Transport (which panics on first use).
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) error {
		if hc == nil {
			return nil
		}
		if hc.Transport != nil {
			c.Client.HTTPClient().Transport = hc.Transport
		}
		if hc.Timeout > 0 {
			c.Client.HTTPClient().Timeout = hc.Timeout
		}
		return nil
	}
}

// WithRetry configures the exponential-backoff round-tripper. A negative max disables retries; at
// most eight retries are made. Nonpositive backoff uses 100ms, with an
// eight-second cap. A longer Retry-After still respects the request deadline.
func WithRetry(max int, backoff time.Duration) Option {
	return func(c *Client) error {
		if max < 0 {
			max = 0
		}
		c.retryMax = max
		c.retryBackoff = backoff
		return nil
	}
}

// WithBaseURL rebinds the base URL after construction. Useful for
// tests that point the same Client at multiple sandboxes, and for
// environment switching (staging vs production).
//
// Deprecated: the internal Client's baseURL field is unexported;
// the option returns errOptionUnsupported. Until PR 12 lands, callers
// needing to switch base URL should reconstruct the Client. The
// Deprecated tag is for the option's current "always returns an
// error" shape, not the underlying concept — PR 12 will un-deprecate
// it.
func WithBaseURL(u string) Option {
	return func(c *Client) error {
		_ = u
		return errOptionUnsupported{option: "WithBaseURL"}
	}
}

// WithToken sets the bearer token on the Client. Useful for daemons
// that need a non-empty token at construction time (the SDK already
// supports NewClient(baseURL, token); WithToken is the post-PR-12
// functional-option form).
//
// For long-lived clients that need to rotate the token after
// construction, use (*api.Client).SetToken directly — Option funcs
// only run during NewClient, so they can't rotate mid-session.
func WithToken(token string) Option {
	return func(c *Client) error {
		c.Client.SetToken(token)
		return nil
	}
}

// WithDeployTimeout sets the upload HTTP client timeout. The default
// is 30s. Pass a longer duration for multi-MB tarball deploys.
//
// Deprecated: the internal Client's deployHTTP field is unexported,
// so this option returns errOptionUnsupported. Callers needing a
// non-default timeout today must construct via
// NewClientWithDeployTimeout (re-exported as a free function — see
// the package-level alias once PR 12 lands). The Deprecated tag is
// for the option's current "always returns an error" shape, not
// the underlying concept — PR 12 will un-deprecate it.
func WithDeployTimeout(d time.Duration) Option {
	return func(c *Client) error {
		_ = d // accepted for future PR 12 wiring; today any value errors.
		return errOptionUnsupported{option: "WithDeployTimeout"}
	}
}

// WithLogger attaches a slog.Logger for request/response logging.
// A nil logger is a no-op. The logger is stored on the Client; PR 4
// will install the actual logging round-tripper that invokes it
// once per request with structured attributes. Until PR 4 lands the
// logger is held but unused — no test should depend on observing
// log output yet.
func WithLogger(log *slog.Logger) Option {
	return func(c *Client) error {
		if log == nil {
			return nil
		}
		c.log = log
		return nil
	}
}
