package faas

import (
	"io"
	"log/slog"
	"net/http"
	"time"
)

// loggingRoundTripper wraps an http.RoundTripper, emitting one
// slog.Debug line per request/response. nil log is a true no-op
// (the constructor returns the inner Transport identity so the
// stack is observably identical to a non-wrapped chain).
//
// The slog calls are intentionally simple — a single struct of
// key/value pairs (method, url, status, elapsed_ms, error). This
// matches the convention other cloud SDKs use (cloudflare-go,
// AWS SDK v2). PII redaction is the caller's job; the SDK does
// not log request/response bodies.
type loggingRoundTripper struct {
	next http.RoundTripper
	log  *slog.Logger
}

// newLoggingRoundTripper returns an http.RoundTripper that logs
// each attempt. If log is nil, next is returned unchanged so the
// wrapper is observably a no-op (no log allocations, no stack
// frame on hot path).
func newLoggingRoundTripper(next http.RoundTripper, log *slog.Logger) http.RoundTripper {
	if log == nil {
		return next
	}
	return &loggingRoundTripper{next: next, log: log}
}

func (l *loggingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	l.log.Debug("faas http request",
		"method", req.Method,
		"url", req.URL.String(),
	)
	resp, err := l.next.RoundTrip(req)
	elapsed := time.Since(start)
	if err != nil {
		l.log.Debug("faas http response",
			"method", req.Method,
			"url", req.URL.String(),
			"error", err.Error(),
			"elapsed_ms", elapsed.Milliseconds(),
		)
		return resp, err
	}
	l.log.Debug("faas http response",
		"method", req.Method,
		"url", req.URL.String(),
		"status", resp.StatusCode,
		"elapsed_ms", elapsed.Milliseconds(),
	)
	return resp, nil
}

// retryRoundTripper retries on 5xx and 429 with bounded
// exponential backoff. It does NOT retry on other 4xx (the
// server is telling us the request is wrong; retrying won't
// help) or on network errors (those bubble up immediately so
// the caller's context cancellation / deadline takes effect).
//
// Body rewind: net/http populates req.GetBody for all
// http.NewRequestWithContext-created requests (the SDK uses
// http.NewRequestWithContext in doReq). On retry, we rewind
// via GetBody so the body is replayable. For requests without
// GetBody (rare — only requests built by hand), the retry
// would send an empty body; this is acceptable because the
// SDK only mutates via doReq, which always sets GetBody.
//
// max <= 0 is a no-op identity passthrough (matches
// loggingRoundTripper's nil-log behavior: the option is
// opt-in).
type retryRoundTripper struct {
	next    http.RoundTripper
	max     int
	backoff time.Duration
}

// newRetryRoundTripper returns an http.RoundTripper that retries
// on 5xx and 429 up to max times with exponential backoff
// (backoff, 2*backoff, 4*backoff, ...). If max <= 0, next is
// returned unchanged.
func newRetryRoundTripper(next http.RoundTripper, max int, backoff time.Duration) http.RoundTripper {
	if max <= 0 {
		return next
	}
	return &retryRoundTripper{next: next, max: max, backoff: backoff}
}

// RoundTrip is the http.RoundTripper contract. It loops up to
// max+1 times (initial attempt + max retries), each time checking
// the response and either returning success, returning a non-
// retriable response, or sleeping with backoff before the next
// attempt.
//
// The sleep is context-aware: a cancelled or expired request
// context aborts the wait and returns the context error.
func (r *retryRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var lastResp *http.Response
	for attempt := 0; attempt <= r.max; attempt++ {
		if attempt > 0 {
			delay := r.backoff << (attempt - 1)
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-req.Context().Done():
				timer.Stop()
				return nil, req.Context().Err()
			}
			// Rewind body for retry. GetBody is set by
			// http.NewRequestWithContext (which the SDK uses)
			// and by any caller that built a body explicitly.
			if req.GetBody != nil {
				body, err := req.GetBody()
				if err != nil {
					return lastResp, err
				}
				req.Body = body
			}
		}
		resp, err := r.next.RoundTrip(req)
		if err != nil {
			// Network error — surface immediately. The SDK's
			// outer context is the cancellation source; we
			// don't try to retry a network blip.
			return resp, err
		}
		lastResp = resp
		// Retry on 5xx and 429 only. 4xx other than 429 is
		// a client-side problem; the server is not going to
		// change its mind on a retry.
		if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}
		// Drain + close before retrying so the connection can
		// be reused (Go's http.Transport will close unclosed
		// bodies, but explicit drain is friendlier and avoids
		// a "body not drained" log line on retry).
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	return lastResp, nil
}
