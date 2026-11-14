package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/retry"
)

// MaxReadRetries bounds the additional attempts accepted by WithReadRetries.
const MaxReadRetries = 5

const maxReadRetryDelay = 30 * time.Second

// WithReadRetries returns a copy with up to retries additional attempts for
// transient failures on selected app, deployment and billing JSON reads.
// Values are clamped to [0, MaxReadRetries]; zero retains existing behavior.
// Uploads, mutations, streams and other routes retain their existing policies.
// Configure clients before use. HTTP timeouts apply separately to each attempt;
// the caller context bounds attempts and cancellable backoff together.
func (c *Client) WithReadRetries(retries int) *Client {
	copy := *c
	copy.readRetries = min(max(retries, 0), MaxReadRetries)
	return &copy
}

// ReadRetryAttempt is transport-only metadata for an opted-in JSON read.
// Delay is the selected backoff before this attempt; it is zero initially.
type ReadRetryAttempt struct {
	Attempt, MaxAttempts int
	Delay                time.Duration
}

type readRetryAttemptKey struct{}

type readRetryResponse struct {
	status  int
	headers http.Header
}

// ReadRetryAttemptFromContext lets transport observers identify read retries.
// It contains no URL, body, credentials or server-supplied text.
func ReadRetryAttemptFromContext(ctx context.Context) (ReadRetryAttempt, bool) {
	attempt, ok := ctx.Value(readRetryAttemptKey{}).(ReadRetryAttempt)
	return attempt, ok
}

// This is a closed route set. A GET elsewhere can invoke code, acquire work or
// stream data, so the HTTP method alone does not establish replay safety.
func retryableReadRoute(method, path string) bool {
	if method != http.MethodGet && method != http.MethodHead {
		return false
	}
	u, err := url.ParseRequestURI(path)
	if err != nil || u.IsAbs() || u.Fragment != "" {
		return false
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) < 3 || parts[0] != "" || parts[1] != "v1" {
		return false
	}
	if u.Path == "/v1/apps" || u.Path == "/v1/usage/summary" {
		return true
	}
	if len(parts) < 4 || parts[3] == "" || parts[3] == "." || parts[3] == ".." {
		return false
	}
	switch parts[2] {
	case "apps":
		if len(parts) == 6 && parts[4] == "deployments" && parts[5] == "latest" {
			return true
		}
		if len(parts) == 7 && parts[4] == "deployments" && parts[5] != "" && parts[5] != "." && parts[5] != ".." && parts[6] == "summary" {
			return true
		}
		return len(parts) == 4 || len(parts) == 5 && (parts[4] == "status" || parts[4] == "deployments" || parts[4] == "usage")
	case "deployments":
		return len(parts) == 4 || len(parts) == 5 && parts[4] == "stages"
	}
	return false
}

func (c *Client) readRetryPause(ctx context.Context, response readRetryResponse, err error, nextRetry int) (time.Duration, bool) {
	if err == nil || ctx.Err() != nil || nextRetry > c.readRetries {
		return 0, false
	}
	pause := retry.Delay(250*time.Millisecond, 5*time.Second, nextRetry)
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if !transientReadProblem(response.status, apiErr.Problem.Code) {
			return 0, false
		}
		values := response.headers.Values("Retry-After")
		if len(values) == 0 {
			return pause, response.status != http.StatusTooManyRequests
		}
		if len(values) != 1 {
			return 0, false
		}
		wait, ok := readRetryAfter(values[0], time.Now())
		return max(pause, wait), ok
	}
	// An unread error body cannot prove a refusal is transient. A broken
	// successful read or a connection failure can be retried safely.
	if response.status != 0 && (response.status < 200 || response.status >= 300) {
		return 0, false
	}
	return pause, transientReadTransport(err)
}

func transientReadProblem(status int, code string) bool {
	if status == http.StatusTooManyRequests {
		return code == CodeAPIRateLimited || code == "rate_limited" || code == "http_error"
	}
	if status != http.StatusBadGateway && status != http.StatusServiceUnavailable && status != http.StatusGatewayTimeout {
		return false
	}
	switch code {
	case "http_error", CodeInternal, CodeGatewayOverloaded, "service_unavailable", "temporarily_unavailable", "bad_gateway", "gateway_timeout":
		return true
	}
	return false
}

func readRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseUint(value, 10, 64); err == nil {
		if seconds > uint64(maxReadRetryDelay/time.Second) {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	date, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	wait := max(date.Sub(now), 0)
	return wait, wait <= maxReadRetryDelay
}

func transientReadTransport(err error) bool {
	var unknown x509.UnknownAuthorityError
	var certificate x509.CertificateInvalidError
	var hostname x509.HostnameError
	var handshake tls.RecordHeaderError
	if errors.As(err, &unknown) || errors.As(err, &certificate) || errors.As(err, &hostname) || errors.As(err, &handshake) {
		return false
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return dns.IsTimeout || dns.IsTemporary
	}
	var connection *net.OpError
	var timeout net.Error
	return errors.As(err, &connection) || errors.As(err, &timeout) && timeout.Timeout() || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.DeadlineExceeded)
}
